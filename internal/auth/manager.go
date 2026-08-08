package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName                    = "faro_session"
	sessionLifetime               = 7 * 24 * time.Hour
	maxFailures                   = 5
	lockoutDuration               = 5 * time.Minute
	authenticationRequiredMessage = "authentication required"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

type Manager struct {
	store      *db.Store
	now        func() time.Time
	dummyHash  []byte
	trustProxy bool
	mu         sync.Mutex
	failures   map[string]failureState
}

type failureState struct {
	Count       int
	LockedUntil time.Time
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type passwordChange struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type sessionUser struct {
	ID       int64
	Username string
}

type contextKey string

const userContextKey contextKey = "authenticated-user"

func UserID(ctx context.Context) (int64, bool) {
	user, ok := ctx.Value(userContextKey).(sessionUser)
	return user.ID, ok && user.ID > 0
}

func NewManager(store *db.Store) *Manager {
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte("faro-invalid-login-comparison"), bcrypt.DefaultCost)
	return &Manager{store: store, now: time.Now, dummyHash: dummyHash, trustProxy: strings.EqualFold(os.Getenv("FARO_TRUST_PROXY"), "true"), failures: map[string]failureState{}}
}

func (manager *Manager) Status(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	configured, err := manager.configured(request.Context())
	if err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	user, authenticated := manager.authenticate(request)
	payload := map[string]any{
		"configured":          configured,
		"authenticated":       authenticated,
		"onboarding_complete": manager.OnboardingComplete(request.Context()),
	}
	if authenticated {
		payload["username"] = user.Username
	}
	writeJSON(responseWriter, http.StatusOK, payload)
}

func (manager *Manager) OnboardingComplete(ctx context.Context) bool {
	var value string
	if err := manager.store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'onboarding_completed'`).Scan(&value); err != nil {
		return false
	}
	return value == "true"
}

func (manager *Manager) Setup(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	var redundancyRole string
	if err := manager.store.DB.QueryRowContext(request.Context(), `SELECT role FROM redundancy_state WHERE id = 1`).Scan(&redundancyRole); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, "could not verify this Faro server's role")
		return
	}
	if redundancyRole == "replica" {
		writeError(responseWriter, http.StatusConflict, "replica servers are managed by the primary Faro server")
		return
	}
	configured, err := manager.configured(request.Context())
	if err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	if configured {
		writeError(responseWriter, http.StatusConflict, "Faro authentication is already configured")
		return
	}
	input, ok := decodeCredentials(responseWriter, request)
	if !ok {
		return
	}
	if err := validateCredentials(input); err != nil {
		writeError(responseWriter, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(responseWriter, http.StatusInternalServerError, "could not secure the password")
		return
	}
	result, err := manager.store.DB.ExecContext(request.Context(), `INSERT INTO users(username, password_hash) VALUES(?, ?)`, strings.TrimSpace(input.Username), string(hash))
	if err != nil {
		writeError(responseWriter, http.StatusConflict, "Faro authentication is already configured")
		return
	}
	userID, err := result.LastInsertId()
	if err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	if err := manager.createSession(responseWriter, request, userID); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(responseWriter, http.StatusCreated, map[string]any{"ok": true, "username": strings.TrimSpace(input.Username)})
}

func (manager *Manager) Login(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	input, ok := decodeCredentials(responseWriter, request)
	if !ok {
		return
	}
	key := manager.failureKey(request, input.Username)
	if retryAfter, blocked := manager.blocked(key); blocked {
		responseWriter.Header().Set("Retry-After", retryAfter)
		writeError(responseWriter, http.StatusTooManyRequests, "too many login attempts; try again shortly")
		return
	}
	var userID int64
	var username, passwordHash string
	err := manager.store.DB.QueryRowContext(request.Context(), `SELECT id, username, password_hash FROM users WHERE username = ?`, strings.TrimSpace(input.Username)).Scan(&userID, &username, &passwordHash)
	hash := manager.dummyHash
	if err == nil {
		hash = []byte(passwordHash)
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(input.Password)) == nil
	if err != nil || !passwordMatches {
		manager.recordFailure(key)
		writeError(responseWriter, http.StatusUnauthorized, "invalid username or password")
		return
	}
	manager.clearFailures(key)
	if err := manager.createSession(responseWriter, request, userID); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true, "username": username})
}

func (manager *Manager) Logout(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	if cookie, err := request.Cookie(cookieName); err == nil && cookie.Value != "" {
		_, _ = manager.store.DB.ExecContext(request.Context(), `DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash(cookie.Value))
	}
	manager.expireCookie(responseWriter, request)
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (manager *Manager) ChangePassword(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	user, authenticated := manager.authenticate(request)
	if !authenticated {
		writeError(responseWriter, http.StatusUnauthorized, authenticationRequiredMessage)
		return
	}
	defer func() { _ = request.Body.Close() }()
	var input passwordChange
	decoder := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 4096))
	if err := decoder.Decode(&input); err != nil {
		writeError(responseWriter, http.StatusBadRequest, "invalid request")
		return
	}
	if err := validatePassword(input.NewPassword); err != nil {
		writeError(responseWriter, http.StatusBadRequest, err.Error())
		return
	}
	if input.CurrentPassword == input.NewPassword {
		writeError(responseWriter, http.StatusBadRequest, "new password must be different from the current password")
		return
	}
	key := manager.failureKey(request, user.Username)
	if retryAfter, blocked := manager.blocked(key); blocked {
		responseWriter.Header().Set("Retry-After", retryAfter)
		writeError(responseWriter, http.StatusTooManyRequests, "too many password attempts; try again shortly")
		return
	}
	var passwordHash string
	if err := manager.store.DB.QueryRowContext(request.Context(), `SELECT password_hash FROM users WHERE id = ?`, user.ID).Scan(&passwordHash); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, "could not verify the current password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.CurrentPassword)) != nil {
		manager.recordFailure(key)
		writeError(responseWriter, http.StatusForbidden, "current password is incorrect")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(responseWriter, http.StatusInternalServerError, "could not secure the new password")
		return
	}
	cookie, err := request.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		writeError(responseWriter, http.StatusUnauthorized, authenticationRequiredMessage)
		return
	}
	tx, err := manager.store.DB.BeginTx(request.Context(), nil)
	if err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(request.Context(), `UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(newHash), user.ID); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, "could not update the password")
		return
	}
	if _, err := tx.ExecContext(request.Context(), `DELETE FROM auth_sessions WHERE user_id = ? AND token_hash <> ?`, user.ID, tokenHash(cookie.Value)); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, "could not close other sessions")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(responseWriter, http.StatusInternalServerError, err.Error())
		return
	}
	manager.clearFailures(key)
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (manager *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		publicRedundancy := request.URL.Path == "/api/redundancy/public" ||
			request.URL.Path == "/api/redundancy/join" ||
			request.URL.Path == "/api/redundancy/pair" ||
			request.URL.Path == "/api/redundancy/replica/snapshot" ||
			request.URL.Path == "/api/redundancy/replica/ack"
		if request.Method == http.MethodOptions || request.URL.Path == "/healthz" || request.URL.Path == "/metrics" || request.URL.Path == "/api/version" || request.URL.Path == "/api/version/check" || request.URL.Path == "/api/upgrade" || strings.HasPrefix(request.URL.Path, "/api/auth/") || publicRedundancy {
			next.ServeHTTP(responseWriter, request)
			return
		}
		if request.Method == http.MethodDelete && request.URL.Path == "/api/redundancy" {
			configured, err := manager.configured(request.Context())
			if err != nil {
				writeError(responseWriter, http.StatusInternalServerError, "could not verify Faro authentication")
				return
			}
			if !configured {
				next.ServeHTTP(responseWriter, request)
				return
			}
		}
		user, ok := manager.authenticate(request)
		if !ok {
			writeError(responseWriter, http.StatusUnauthorized, authenticationRequiredMessage)
			return
		}
		next.ServeHTTP(responseWriter, request.WithContext(context.WithValue(request.Context(), userContextKey, user)))
	})
}

func (manager *Manager) configured(ctx context.Context) (bool, error) {
	var count int
	if err := manager.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (manager *Manager) authenticate(request *http.Request) (sessionUser, bool) {
	cookie, err := request.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return sessionUser{}, false
	}
	now := manager.now().UTC().Format(time.RFC3339)
	var user sessionUser
	err = manager.store.DB.QueryRowContext(request.Context(), `
		SELECT u.id, u.username
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND datetime(s.expires_at) > datetime(?)
	`, tokenHash(cookie.Value), now).Scan(&user.ID, &user.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = manager.store.DB.ExecContext(request.Context(), `DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash(cookie.Value))
		}
		return sessionUser{}, false
	}
	_, _ = manager.store.DB.ExecContext(request.Context(), `UPDATE auth_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE token_hash = ?`, tokenHash(cookie.Value))
	return user, true
}

func (manager *Manager) createSession(responseWriter http.ResponseWriter, request *http.Request, userID int64) error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	expires := manager.now().UTC().Add(sessionLifetime)
	_, _ = manager.store.DB.ExecContext(request.Context(), `DELETE FROM auth_sessions WHERE datetime(expires_at) <= datetime(?)`, manager.now().UTC().Format(time.RFC3339))
	if _, err := manager.store.DB.ExecContext(request.Context(), `INSERT INTO auth_sessions(user_id, token_hash, expires_at) VALUES(?, ?, ?)`, userID, tokenHash(token), expires.Format(time.RFC3339)); err != nil {
		return err
	}
	http.SetCookie(responseWriter, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   requestIsSecure(request),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (manager *Manager) expireCookie(responseWriter http.ResponseWriter, request *http.Request) {
	http.SetCookie(responseWriter, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: requestIsSecure(request), SameSite: http.SameSiteLaxMode})
}

func validateCredentials(input credentials) error {
	username := strings.TrimSpace(input.Username)
	if !usernamePattern.MatchString(username) {
		return errors.New("username must be 3-64 characters using letters, numbers, dots, dashes, or underscores")
	}
	return validatePassword(input.Password)
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len([]byte(password)) > 72 {
		return errors.New("password must be 72 bytes or fewer")
	}
	return nil
}

func decodeCredentials(responseWriter http.ResponseWriter, request *http.Request) (credentials, bool) {
	defer func() { _ = request.Body.Close() }()
	var input credentials
	decoder := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 4096))
	if err := decoder.Decode(&input); err != nil {
		writeError(responseWriter, http.StatusBadRequest, "invalid request")
		return credentials{}, false
	}
	return input, true
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func requestIsSecure(request *http.Request) bool {
	return request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https")
}

func (manager *Manager) failureKey(request *http.Request, username string) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	if manager.trustProxy {
		if forwarded := net.ParseIP(strings.TrimSpace(request.Header.Get("X-Real-IP"))); forwarded != nil {
			host = forwarded.String()
		}
	}
	return strings.ToLower(strings.TrimSpace(username)) + "|" + host
}

func (manager *Manager) blocked(key string) (string, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.failures[key]
	if state.LockedUntil.After(manager.now()) {
		seconds := int(state.LockedUntil.Sub(manager.now()).Seconds())
		seconds = max(seconds, 1)
		return strconv.Itoa(seconds), true
	}
	if !state.LockedUntil.IsZero() {
		delete(manager.failures, key)
	}
	return "", false
}

func (manager *Manager) recordFailure(key string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.failures[key]
	state.Count++
	if state.Count >= maxFailures {
		state.LockedUntil = manager.now().Add(lockoutDuration)
	}
	manager.failures[key] = state
}

func (manager *Manager) clearFailures(key string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.failures, key)
}

func writeJSON(responseWriter http.ResponseWriter, status int, payload any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	_ = json.NewEncoder(responseWriter).Encode(payload)
}

func writeError(responseWriter http.ResponseWriter, status int, message string) {
	writeJSON(responseWriter, status, map[string]any{"error": message})
}

func methodNotAllowed(responseWriter http.ResponseWriter) {
	writeError(responseWriter, http.StatusMethodNotAllowed, "method not allowed")
}
