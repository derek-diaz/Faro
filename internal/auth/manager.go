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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName      = "faro_session"
	sessionLifetime = 7 * 24 * time.Hour
	maxFailures     = 5
	lockoutDuration = 5 * time.Minute
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

type Manager struct {
	store     *db.Store
	now       func() time.Time
	dummyHash []byte
	mu        sync.Mutex
	failures  map[string]failureState
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

func NewManager(store *db.Store) *Manager {
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte("faro-invalid-login-comparison"), bcrypt.DefaultCost)
	return &Manager{store: store, now: time.Now, dummyHash: dummyHash, failures: map[string]failureState{}}
}

func (m *Manager) Status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	configured, err := m.configured(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	user, authenticated := m.authenticate(r)
	payload := map[string]any{
		"configured":          configured,
		"authenticated":       authenticated,
		"onboarding_complete": m.onboardingComplete(r.Context()),
	}
	if authenticated {
		payload["username"] = user.Username
	}
	writeJSON(w, http.StatusOK, payload)
}

func (m *Manager) onboardingComplete(ctx context.Context) bool {
	var value string
	if err := m.store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'onboarding_completed'`).Scan(&value); err != nil {
		return false
	}
	return value == "true"
}

func (m *Manager) Setup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	configured, err := m.configured(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if configured {
		writeError(w, http.StatusConflict, "Faro authentication is already configured")
		return
	}
	input, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	if err := validateCredentials(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not secure the password")
		return
	}
	result, err := m.store.DB.ExecContext(r.Context(), `INSERT INTO users(username, password_hash) VALUES(?, ?)`, strings.TrimSpace(input.Username), string(hash))
	if err != nil {
		writeError(w, http.StatusConflict, "Faro authentication is already configured")
		return
	}
	userID, err := result.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.createSession(w, r, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "username": strings.TrimSpace(input.Username)})
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	input, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	key := m.failureKey(r, input.Username)
	if retryAfter, blocked := m.blocked(key); blocked {
		w.Header().Set("Retry-After", retryAfter)
		writeError(w, http.StatusTooManyRequests, "too many login attempts; try again shortly")
		return
	}
	var userID int64
	var username, passwordHash string
	err := m.store.DB.QueryRowContext(r.Context(), `SELECT id, username, password_hash FROM users WHERE username = ?`, strings.TrimSpace(input.Username)).Scan(&userID, &username, &passwordHash)
	hash := m.dummyHash
	if err == nil {
		hash = []byte(passwordHash)
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(input.Password)) == nil
	if err != nil || !passwordMatches {
		m.recordFailure(key)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	m.clearFailures(key)
	if err := m.createSession(w, r, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": username})
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
		_, _ = m.store.DB.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash(cookie.Value))
	}
	m.expireCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Manager) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	user, authenticated := m.authenticate(r)
	if !authenticated {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	defer r.Body.Close()
	var input passwordChange
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := validatePassword(input.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.CurrentPassword == input.NewPassword {
		writeError(w, http.StatusBadRequest, "new password must be different from the current password")
		return
	}
	key := m.failureKey(r, user.Username)
	if retryAfter, blocked := m.blocked(key); blocked {
		w.Header().Set("Retry-After", retryAfter)
		writeError(w, http.StatusTooManyRequests, "too many password attempts; try again shortly")
		return
	}
	var passwordHash string
	if err := m.store.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, user.ID).Scan(&passwordHash); err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify the current password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.CurrentPassword)) != nil {
		m.recordFailure(key)
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not secure the new password")
		return
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	tx, err := m.store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(newHash), user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update the password")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE user_id = ? AND token_hash <> ?`, user.ID, tokenHash(cookie.Value)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not close other sessions")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	m.clearFailures(key)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || strings.HasPrefix(r.URL.Path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := m.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

func (m *Manager) configured(ctx context.Context) (bool, error) {
	var count int
	if err := m.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (m *Manager) authenticate(r *http.Request) (sessionUser, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return sessionUser{}, false
	}
	now := m.now().UTC().Format(time.RFC3339)
	var user sessionUser
	err = m.store.DB.QueryRowContext(r.Context(), `
		SELECT u.id, u.username
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND datetime(s.expires_at) > datetime(?)
	`, tokenHash(cookie.Value), now).Scan(&user.ID, &user.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = m.store.DB.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash(cookie.Value))
		}
		return sessionUser{}, false
	}
	_, _ = m.store.DB.ExecContext(r.Context(), `UPDATE auth_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE token_hash = ?`, tokenHash(cookie.Value))
	return user, true
}

func (m *Manager) createSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	expires := m.now().UTC().Add(sessionLifetime)
	_, _ = m.store.DB.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE datetime(expires_at) <= datetime(?)`, m.now().UTC().Format(time.RFC3339))
	if _, err := m.store.DB.ExecContext(r.Context(), `INSERT INTO auth_sessions(user_id, token_hash, expires_at) VALUES(?, ?, ?)`, userID, tokenHash(token), expires.Format(time.RFC3339)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (m *Manager) expireCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteLaxMode})
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

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	defer r.Body.Close()
	var input credentials
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return credentials{}, false
	}
	return input, true
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (m *Manager) failureKey(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.ToLower(strings.TrimSpace(username)) + "|" + host
}

func (m *Manager) blocked(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.failures[key]
	if state.LockedUntil.After(m.now()) {
		seconds := int(state.LockedUntil.Sub(m.now()).Seconds())
		if seconds < 1 {
			seconds = 1
		}
		return strconv.Itoa(seconds), true
	}
	if !state.LockedUntil.IsZero() {
		delete(m.failures, key)
	}
	return "", false
}

func (m *Manager) recordFailure(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.failures[key]
	state.Count++
	if state.Count >= maxFailures {
		state.LockedUntil = m.now().Add(lockoutDuration)
	}
	m.failures[key] = state
}

func (m *Manager) clearFailures(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.failures, key)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
