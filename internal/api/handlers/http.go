package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Standard HTTP value for X-Content-Type-Options.
// noinspection SpellCheckingInspection
const contentTypeOptionsNoSniff = "nosniff"

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer logActionError("close request body", r.Body.Close)
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		writeBadRequest(w, err)
		return false
	}
	return true
}

func idFromPath(w http.ResponseWriter, r *http.Request, prefix string) (int64, bool) {
	id, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/"), 10, 64)
	if err != nil {
		writeBadRequest(w, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode JSON response: %v", err)
	}
}

func writeBadRequest(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func cors(trustProxy bool, onboardingComplete func(context.Context) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		if !allowCORSRequest(w, r, trustProxy, onboardingComplete) {
			return
		}
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", contentTypeOptionsNoSniff)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Faro-Timezone")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
}

func allowCORSRequest(w http.ResponseWriter, r *http.Request, trustProxy bool, onboardingComplete func(context.Context) bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	crossOrigin := origin != "" && !sameOrigin(r, origin, trustProxy)
	crossSite := strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site")
	if r.URL.Path == "/api/auth/setup" || !crossOrigin && !crossSite {
		return true
	}
	if allowCrossOriginDuringOnboarding(r, onboardingComplete) {
		return true
	}
	if crossOrigin {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin requests are not allowed"})
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-site requests are not allowed"})
	return false
}

func allowCrossOriginDuringOnboarding(r *http.Request, onboardingComplete func(context.Context) bool) bool {
	if r.Method == http.MethodDelete && r.URL.Path == "/api/redundancy" {
		return false
	}
	return !onboardingComplete(r.Context())
}

func logActionError(operation string, action func() error) {
	if err := action(); err != nil {
		log.Printf("%s: %v", operation, err)
	}
}

func sameOrigin(r *http.Request, origin string, trustProxy bool) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil || trustProxy && strings.EqualFold(firstForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	host := r.Host
	if trustProxy {
		if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
		}
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, host)
}

func firstForwardedValue(value string) string {
	value, _, _ = strings.Cut(value, ",")
	return strings.TrimSpace(value)
}
