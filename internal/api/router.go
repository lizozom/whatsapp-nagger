package api

import (
	"net/http"
	"time"
)

// NewServer creates an *http.Server with the given mux. The caller (main.go)
// owns the mux and registers all routes before calling ListenAndServe.
func NewServer(addr string, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}
