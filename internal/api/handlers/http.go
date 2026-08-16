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

func decode(responseWriter http.ResponseWriter, request *http.Request, dst any) bool {
	defer logActionError("close request body", request.Body.Close)
	if err := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 1<<20)).Decode(dst); err != nil {
		writeBadRequest(responseWriter, err)
		return false
	}
	return true
}

func idFromPath(responseWriter http.ResponseWriter, request *http.Request, prefix string) (int64, bool) {
	id, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(request.URL.Path, prefix), "/"), 10, 64)
	if err != nil {
		writeBadRequest(responseWriter, errors.New("invalid id"))
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

func writeJSON(responseWriter http.ResponseWriter, status int, payload any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	// Keep large responses, especially encrypted redundancy snapshots, on the
	// HTTP writer instead of creating a second full-size encoded copy first.
	if err := json.NewEncoder(responseWriter).Encode(payload); err != nil {
		log.Printf("encode JSON response: %v", err)
	}
}

func writeBadRequest(responseWriter http.ResponseWriter, err error) {
	writeJSON(responseWriter, http.StatusBadRequest, map[string]any{"error": err.Error()})
}

func writeError(responseWriter http.ResponseWriter, err error) {
	writeJSON(responseWriter, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func methodNotAllowed(responseWriter http.ResponseWriter) {
	writeJSON(responseWriter, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func cors(trustProxy bool, onboardingComplete func(context.Context) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		setSecurityHeaders(responseWriter)
		if !allowCORSRequest(responseWriter, request, trustProxy, onboardingComplete) {
			return
		}
		setCORSHeaders(responseWriter, request)
		if request.Method == http.MethodOptions {
			responseWriter.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(responseWriter, request)
	})
}

func setSecurityHeaders(responseWriter http.ResponseWriter) {
	responseWriter.Header().Set("X-Content-Type-Options", contentTypeOptionsNoSniff)
	responseWriter.Header().Set("X-Frame-Options", "DENY")
	responseWriter.Header().Set("Referrer-Policy", "no-referrer")
}

func setCORSHeaders(responseWriter http.ResponseWriter, request *http.Request) {
	if origin := strings.TrimSpace(request.Header.Get("Origin")); origin != "" {
		responseWriter.Header().Set("Access-Control-Allow-Origin", origin)
		responseWriter.Header().Set("Vary", "Origin")
	}
	responseWriter.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	responseWriter.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Faro-Timezone")
	responseWriter.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
}

func allowCORSRequest(responseWriter http.ResponseWriter, request *http.Request, trustProxy bool, onboardingComplete func(context.Context) bool) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	crossOrigin := origin != "" && !sameOrigin(request, origin, trustProxy)
	crossSite := strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site")
	if request.URL.Path == "/api/auth/setup" || !crossOrigin && !crossSite {
		return true
	}
	if allowCrossOriginDuringOnboarding(request, onboardingComplete) {
		return true
	}
	if crossOrigin {
		writeJSON(responseWriter, http.StatusForbidden, map[string]any{"error": "cross-origin requests are not allowed"})
		return false
	}
	writeJSON(responseWriter, http.StatusForbidden, map[string]any{"error": "cross-site requests are not allowed"})
	return false
}

func allowCrossOriginDuringOnboarding(request *http.Request, onboardingComplete func(context.Context) bool) bool {
	if request.Method == http.MethodDelete && request.URL.Path == "/api/redundancy" {
		return false
	}
	return !onboardingComplete(request.Context())
}

func logActionError(operation string, action func() error) {
	if err := action(); err != nil {
		log.Printf("%s: %v", operation, err)
	}
}

func sameOrigin(request *http.Request, origin string, trustProxy bool) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := "http"
	if request.TLS != nil || trustProxy && strings.EqualFold(firstForwardedValue(request.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	host := request.Host
	if trustProxy {
		if forwardedHost := firstForwardedValue(request.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
		}
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, host)
}

func firstForwardedValue(value string) string {
	value, _, _ = strings.Cut(value, ",")
	return strings.TrimSpace(value)
}
