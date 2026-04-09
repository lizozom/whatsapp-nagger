package api

import (
	"net/http"
	"time"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// Router holds dependencies for all dashboard API handlers.
type Router struct {
	Tasks  *db.TaskStore
	Tx     *db.TxStore
	APIKey string // temporary auth; replaced by JWT in M3
}

// NewRouter creates a Router with the given stores and API key.
func NewRouter(tasks *db.TaskStore, tx *db.TxStore, apiKey string) *Router {
	return &Router{Tasks: tasks, Tx: tx, APIKey: apiKey}
}

// Register mounts all /api/* handlers onto the provided mux.
func (rt *Router) Register(mux *http.ServeMux) {
	// Auth-protected endpoints.
	mux.Handle("/api/tasks", rt.auth(http.HandlerFunc(rt.handleTasks)))
	mux.Handle("/api/tasks/stats", rt.auth(http.HandlerFunc(rt.handleTaskStats)))
	mux.Handle("/api/transactions", rt.auth(http.HandlerFunc(rt.handleTransactions)))
	mux.Handle("/api/transactions/summary", rt.auth(http.HandlerFunc(rt.handleTransactionsSummary)))
	mux.Handle("/api/transactions/totals", rt.auth(http.HandlerFunc(rt.handleTransactionsTotals)))
}

// NewServer creates an *http.Server with the given mux. The caller is
// responsible for calling ListenAndServe.
func NewServer(addr string, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}
