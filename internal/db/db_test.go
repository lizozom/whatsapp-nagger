package db

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *TaskStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestAddTask(t *testing.T) {
	store := newTestStore(t)

	task, err := store.AddTask("Fix the sink", "Bob", "2026-03-25")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	if task.ID != 1 {
		t.Errorf("expected ID 1, got %d", task.ID)
	}
	if task.Content != "Fix the sink" {
		t.Errorf("expected content 'Fix the sink', got %q", task.Content)
	}
	if task.Assignee != "Bob" {
		t.Errorf("expected assignee 'Bob', got %q", task.Assignee)
	}
	if task.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", task.Status)
	}
	if task.DueDate != "2026-03-25" {
		t.Errorf("expected due_date '2026-03-25', got %q", task.DueDate)
	}
}

func TestAddTaskNoDueDate(t *testing.T) {
	store := newTestStore(t)

	task, err := store.AddTask("Buy milk", "Alice", "")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if task.DueDate != "" {
		t.Errorf("expected empty due_date, got %q", task.DueDate)
	}
}

func TestListTasksAll(t *testing.T) {
	store := newTestStore(t)

	store.AddTask("Task 1", "Bob", "")
	store.AddTask("Task 2", "Alice", "")
	store.AddTask("Task 3", "Bob", "")

	tasks, err := store.ListTasks("", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
}

func TestListTasksByAssignee(t *testing.T) {
	store := newTestStore(t)

	store.AddTask("Task 1", "Bob", "")
	store.AddTask("Task 2", "Alice", "")
	store.AddTask("Task 3", "Bob", "")

	tasks, err := store.ListTasks("Bob", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks for Bob, got %d", len(tasks))
	}
}

func TestListTasksCaseInsensitive(t *testing.T) {
	store := newTestStore(t)

	store.AddTask("Task 1", "Bob", "")

	tasks, err := store.ListTasks("bob", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected case-insensitive match, got %d tasks", len(tasks))
	}
}

func TestListTasksByStatus(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask("Task 1", "Bob", "")
	store.AddTask("Task 2", "Bob", "")
	store.UpdateTask(task.ID, "done", "")

	pending, err := store.ListTasks("", "pending")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending task, got %d", len(pending))
	}

	done, err := store.ListTasks("", "done")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(done) != 1 {
		t.Errorf("expected 1 done task, got %d", len(done))
	}
}

func TestUpdateTask(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask("Fix the sink", "Bob", "")
	updated, err := store.UpdateTask(task.ID, "done", "")
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Status != "done" {
		t.Errorf("expected status 'done', got %q", updated.Status)
	}
	if updated.UpdatedAt == task.CreatedAt {
		t.Log("note: updated_at may equal created_at if test runs within same second")
	}
}

func TestDeleteTask(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask("Fix the sink", "Bob", "")
	err := store.DeleteTask(task.ID)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	tasks, _ := store.ListTasks("", "")
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
	}
}

func TestDeleteTaskNotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteTask(999)
	if err == nil {
		t.Fatal("expected error deleting nonexistent task")
	}
}

func TestNewTaskStoreCreatesFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "new.db")

	store, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	store.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected db file to be created")
	}
}
