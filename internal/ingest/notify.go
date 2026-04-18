package ingest

import (
	"encoding/json"
	"io"
	"net/http"
)

const (
	maxNotifyBodyBytes = 1 << 10 // 1 KB — just a short message
	maxMessageLen      = 500     // characters
)

// NotifyRequest is the JSON body for POST /notify.
type NotifyRequest struct {
	Message string `json:"message"`
}

// NotifyHandler receives HMAC-signed alert messages and forwards them
// to the group chat via a write function.
type NotifyHandler struct {
	Secret string
	Write  func(text string) error
}

func (h *NotifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Secret == "" {
		http.Error(w, "notify not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxNotifyBodyBytes)
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

	var req NotifyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if len([]rune(req.Message)) > maxMessageLen {
		http.Error(w, "message too long", http.StatusBadRequest)
		return
	}

	if err := h.Write(req.Message); err != nil {
		http.Error(w, "send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"sent"}`))
}
