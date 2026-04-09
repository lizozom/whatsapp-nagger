package api

import (
	"net/http"
)

// auth wraps a handler with API key authentication.
// Checks the X-API-Key header against rt.APIKey.
// This is a temporary mechanism replaced by JWT in M3.
func (rt *Router) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.APIKey == "" {
			http.Error(w, "dashboard API not configured", http.StatusServiceUnavailable)
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key") // allow query param for easy browser testing
		}
		if key != rt.APIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
