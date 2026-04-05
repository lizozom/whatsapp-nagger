package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

const (
	maxBodyBytes = 5 << 20 // 5 MB
	headerSig    = "X-Signature"
)

// RequestBody is the payload scrapers POST to /ingest/transactions.
type RequestBody struct {
	Provider     string           `json:"provider"`
	RunID        string           `json:"run_id,omitempty"`
	FetchedAt    string           `json:"fetched_at,omitempty"`
	Transactions []db.Transaction `json:"transactions"`
}

// ResponseBody summarizes the outcome of a successful ingest.
type ResponseBody struct {
	RunID    int64  `json:"run_id"`
	Inserted int    `json:"inserted"`
	Skipped  int    `json:"skipped"`
	Received int    `json:"received"`
	Status   string `json:"status"`
}

// Handler holds dependencies for the ingest HTTP handler.
type Handler struct {
	Store  *db.TxStore
	Secret string
}

// NewHandler constructs an ingest handler. Secret must be non-empty.
func NewHandler(store *db.TxStore, secret string) *Handler {
	return &Handler{Store: store, Secret: secret}
}

// ServeHTTP implements http.Handler. Mount at POST /ingest/transactions.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Secret == "" {
		http.Error(w, "ingest not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	sig := r.Header.Get(headerSig)
	if !VerifySignature(h.Secret, sig, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var req RequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		http.Error(w, "provider is required", http.StatusBadRequest)
		return
	}

	// Stamp provider on each tx (trusting the envelope over per-row fields).
	for i := range req.Transactions {
		if req.Transactions[i].Provider == "" {
			req.Transactions[i].Provider = req.Provider
		}
	}

	runID, err := h.Store.StartRun(req.Provider)
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	inserted, skipped, upErr := h.Store.UpsertBatch(req.Transactions)
	if upErr != nil {
		_ = h.Store.FinishRun(runID, "error", upErr.Error(), 0)
		http.Error(w, "upsert: "+upErr.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.Store.FinishRun(runID, "ok", "", inserted); err != nil {
		// Non-fatal: data is already saved. Log and continue.
		fmt.Printf("ingest: finish run %d: %v\n", runID, err)
	}

	resp := ResponseBody{
		RunID:    runID,
		Inserted: inserted,
		Skipped:  skipped,
		Received: len(req.Transactions),
		Status:   "ok",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// NewServer returns an *http.Server with the ingest route mounted.
// Caller is responsible for starting/stopping it.
func NewServer(addr string, h *Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/ingest/transactions", h)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}
