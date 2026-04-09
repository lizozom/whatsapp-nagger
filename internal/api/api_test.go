package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	tasks, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	tx, err := db.NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	t.Cleanup(func() { tasks.Close(); tx.Close() })
	return NewRouter(tasks, tx, "test-key")
}

func get(rt *Router, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	rt.Register(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-API-Key", "test-key")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAuthRejectsNoKey(t *testing.T) {
	rt := newTestRouter(t)
	mux := http.NewServeMux()
	rt.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthRejectsWrongKey(t *testing.T) {
	rt := newTestRouter(t)
	mux := http.NewServeMux()
	rt.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	req.Header.Set("X-API-Key", "wrong")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestTasksEndpoint(t *testing.T) {
	rt := newTestRouter(t)
	rt.Tasks.AddTask("Fix the sink", "Alice", "2026-04-10")
	rt.Tasks.AddTask("Buy groceries", "Bob", "")

	rec := get(rt, "/api/tasks")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Tasks []db.Task `json:"tasks"`
		Count int       `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("expected 2 tasks, got %d", resp.Count)
	}
}

func TestTasksFilterByAssignee(t *testing.T) {
	rt := newTestRouter(t)
	rt.Tasks.AddTask("Task 1", "Alice", "")
	rt.Tasks.AddTask("Task 2", "Bob", "")

	rec := get(rt, "/api/tasks?assignee=Alice")
	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Errorf("expected 1 task for Alice, got %d", resp.Count)
	}
}

func TestTaskStats(t *testing.T) {
	rt := newTestRouter(t)
	rt.Tasks.AddTask("Pending task", "Alice", "")
	task, _ := rt.Tasks.AddTask("Done task", "Alice", "")
	rt.Tasks.UpdateTask(task.ID, "done", "")

	rec := get(rt, "/api/tasks/stats")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ByAssignee []assigneeStats `json:"by_assignee"`
		TotalDone  int             `json:"total_done"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.TotalDone != 1 {
		t.Errorf("expected 1 done, got %d", resp.TotalDone)
	}
	if len(resp.ByAssignee) != 1 || resp.ByAssignee[0].Pending != 1 || resp.ByAssignee[0].Done != 1 {
		t.Errorf("unexpected by_assignee: %+v", resp.ByAssignee)
	}
}

func TestTransactionsTotals(t *testing.T) {
	rt := newTestRouter(t)
	rt.Tx.UpsertBatch([]db.Transaction{
		{Provider: "max", PostedAt: "2026-04-01", AmountILS: -100, Description: "A"},
		{Provider: "max", PostedAt: "2026-04-02", AmountILS: -200, Description: "B"},
		{Provider: "max", PostedAt: "2026-04-03", AmountILS: 50, Description: "C"},
	})

	t.Setenv("BILLING_DAY", "1")
	t.Setenv("TIMEZONE", "UTC")

	rec := get(rt, "/api/transactions/totals?since=2026-04-01&until=2026-04-30")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		SpentILS   float64 `json:"spent_ils"`
		ChargesILS float64 `json:"charges_ils"`
		RefundsILS float64 `json:"refunds_ils"`
		TxCount    int     `json:"tx_count"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.SpentILS != 250 { // 100+200-50
		t.Errorf("spent_ils: expected 250, got %v", resp.SpentILS)
	}
	if resp.ChargesILS != 300 {
		t.Errorf("charges_ils: expected 300, got %v", resp.ChargesILS)
	}
	if resp.RefundsILS != 50 {
		t.Errorf("refunds_ils: expected 50, got %v", resp.RefundsILS)
	}
	if resp.TxCount != 3 {
		t.Errorf("tx_count: expected 3, got %d", resp.TxCount)
	}
}

func TestTransactionsSummary(t *testing.T) {
	rt := newTestRouter(t)
	rt.Tx.UpsertBatch([]db.Transaction{
		{Provider: "max", PostedAt: "2026-04-01", AmountILS: -100, Description: "SHUFERSAL", Category: "food"},
		{Provider: "max", PostedAt: "2026-04-02", AmountILS: -200, Description: "GAS", Category: "fuel"},
	})

	rec := get(rt, "/api/transactions/summary?group_by=category&since=2026-04-01&until=2026-04-30")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		GroupBy string       `json:"group_by"`
		Rows    []db.SumRow  `json:"rows"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.GroupBy != "category" {
		t.Errorf("group_by: expected category, got %q", resp.GroupBy)
	}
	if len(resp.Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(resp.Rows))
	}
}

func TestAuthViaQueryParam(t *testing.T) {
	rt := newTestRouter(t)
	mux := http.NewServeMux()
	rt.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks?api_key=test-key", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 via query param auth, got %d", rec.Code)
	}
}
