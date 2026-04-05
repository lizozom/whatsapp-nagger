package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

func newTestHandler(t *testing.T, secret string) (*Handler, *db.TxStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tx.db")
	store, err := db.NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewHandler(store, secret), store
}

func postSigned(t *testing.T, h *Handler, secret string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/ingest/transactions", bytes.NewReader(raw))
	if secret != "" {
		req.Header.Set("X-Signature", ComputeSignature(secret, raw))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestVerifySignatureRoundTrip(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	sig := ComputeSignature("shhh", body)
	if !VerifySignature("shhh", sig, body) {
		t.Error("expected signature to verify")
	}
	if VerifySignature("shhh", sig, []byte(`{"hello":"tampered"}`)) {
		t.Error("tampered body should not verify")
	}
	if VerifySignature("wrong-secret", sig, body) {
		t.Error("wrong secret should not verify")
	}
}

func TestIngestHandlerHappyPath(t *testing.T) {
	h, store := newTestHandler(t, "topsecret")

	body := RequestBody{
		Provider: "cal",
		Transactions: []db.Transaction{
			{CardLast4: "1234", PostedAt: "2026-04-01", AmountILS: -42.50, Description: "SHUFERSAL"},
			{CardLast4: "1234", PostedAt: "2026-04-02", AmountILS: -12.0, Description: "CAFE"},
		},
	}
	rec := postSigned(t, h, "topsecret", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if resp.Inserted != 2 || resp.Skipped != 0 || resp.Received != 2 {
		t.Errorf("unexpected counts: %+v", resp)
	}

	// Verify rows landed with provider stamped from envelope.
	txs, err := store.ListTransactions("cal", "", "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(txs) != 2 {
		t.Errorf("expected 2 stored tx, got %d", len(txs))
	}
}

func TestIngestHandlerIdempotent(t *testing.T) {
	h, _ := newTestHandler(t, "s")
	body := RequestBody{
		Provider: "max",
		Transactions: []db.Transaction{
			{CardLast4: "9999", PostedAt: "2026-04-01", AmountILS: -100, Description: "A"},
		},
	}
	_ = postSigned(t, h, "s", body)
	rec := postSigned(t, h, "s", body)

	var resp ResponseBody
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Inserted != 0 || resp.Skipped != 1 {
		t.Errorf("expected rerun to be idempotent, got %+v", resp)
	}
}

func TestIngestHandlerRejectsBadSignature(t *testing.T) {
	h, _ := newTestHandler(t, "right")
	body := RequestBody{Provider: "cal"}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/ingest/transactions", bytes.NewReader(raw))
	req.Header.Set("X-Signature", ComputeSignature("wrong", raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestIngestHandlerRejectsMissingSignature(t *testing.T) {
	h, _ := newTestHandler(t, "s")
	req := httptest.NewRequest(http.MethodPost, "/ingest/transactions", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestIngestHandlerRejectsMissingProvider(t *testing.T) {
	h, _ := newTestHandler(t, "s")
	rec := postSigned(t, h, "s", RequestBody{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestIngestHandlerWrongMethod(t *testing.T) {
	h, _ := newTestHandler(t, "s")
	req := httptest.NewRequest(http.MethodGet, "/ingest/transactions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
