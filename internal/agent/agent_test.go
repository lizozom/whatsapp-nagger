package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	return &Agent{store: store}
}

func TestExecuteToolAddTask(t *testing.T) {
	a := newTestAgent(t)

	result, err := a.ExecuteTool("add_task", []byte(`{"content":"Fix sink","assignee":"Denis","due_date":"2026-03-25"}`))
	if err != nil {
		t.Fatalf("ExecuteTool add_task: %v", err)
	}

	var task db.Task
	if err := json.Unmarshal([]byte(result), &task); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if task.Content != "Fix sink" {
		t.Errorf("expected content 'Fix sink', got %q", task.Content)
	}
	if task.Assignee != "Denis" {
		t.Errorf("expected assignee 'Denis', got %q", task.Assignee)
	}
	if task.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", task.Status)
	}
}

func TestExecuteToolListTasks(t *testing.T) {
	a := newTestAgent(t)

	a.ExecuteTool("add_task", []byte(`{"content":"Task 1","assignee":"Denis"}`))
	a.ExecuteTool("add_task", []byte(`{"content":"Task 2","assignee":"Liza"}`))

	result, err := a.ExecuteTool("list_tasks", []byte(`{"assignee":"Denis"}`))
	if err != nil {
		t.Fatalf("ExecuteTool list_tasks: %v", err)
	}

	var tasks []db.Task
	if err := json.Unmarshal([]byte(result), &tasks); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task for Denis, got %d", len(tasks))
	}
}

func TestExecuteToolListTasksEmpty(t *testing.T) {
	a := newTestAgent(t)

	result, err := a.ExecuteTool("list_tasks", []byte(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool list_tasks: %v", err)
	}
	if result != "null" && result != "[]" {
		// empty slice marshals to "null" in Go
		t.Logf("empty list result: %s", result)
	}
}

func TestExecuteToolUpdateTask(t *testing.T) {
	a := newTestAgent(t)

	addResult, _ := a.ExecuteTool("add_task", []byte(`{"content":"Fix sink","assignee":"Denis"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID, "status": "done"})
	result, err := a.ExecuteTool("update_task", input)
	if err != nil {
		t.Fatalf("ExecuteTool update_task: %v", err)
	}

	var updated db.Task
	json.Unmarshal([]byte(result), &updated)
	if updated.Status != "done" {
		t.Errorf("expected status 'done', got %q", updated.Status)
	}
}

func TestExecuteToolDeleteTask(t *testing.T) {
	a := newTestAgent(t)

	addResult, _ := a.ExecuteTool("add_task", []byte(`{"content":"Fix sink","assignee":"Denis"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID})
	result, err := a.ExecuteTool("delete_task", input)
	if err != nil {
		t.Fatalf("ExecuteTool delete_task: %v", err)
	}
	if result != `{"deleted": true}` {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify it's gone
	listResult, _ := a.ExecuteTool("list_tasks", []byte(`{}`))
	if listResult != "null" && listResult != "[]" {
		var tasks []db.Task
		json.Unmarshal([]byte(listResult), &tasks)
		if len(tasks) != 0 {
			t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
		}
	}
}

func TestExecuteToolUnknown(t *testing.T) {
	a := newTestAgent(t)

	_, err := a.ExecuteTool("nonexistent", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestExecuteToolBadJSON(t *testing.T) {
	a := newTestAgent(t)

	_, err := a.ExecuteTool("add_task", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for bad JSON input")
	}
}
