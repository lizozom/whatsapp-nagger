package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// testGroupID is the placeholder JID used by all task/metadata tests.
const testGroupID = "120363999999@g.us"

// newTestStore creates a TaskStore plus runs migrations so the group_id
// column exists on the tasks/metadata tables. Caller passes testGroupID
// to every TaskStore method.
func newTestStore(t *testing.T) *TaskStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	// migrate_001 ALTERs the transactions table, so it must exist first.
	txStore, err := NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	txStore.Close()

	// Run migrations on a separate connection so the group_id column lands
	// before any test query touches the tables.
	migDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	// Disable migrate_002's tenant-zero backfill — these tests pre-date
	// any tenant-zero data and don't want personas pulled in.
	t.Setenv("WHATSAPP_GROUP_JID", "")
	if err := RunMigrations(migDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	migDB.Close()

	t.Cleanup(func() { store.Close() })
	return store
}

func TestAddTask(t *testing.T) {
	store := newTestStore(t)

	task, err := store.AddTask(testGroupID, "Fix the sink", "Bob", "2026-03-25")
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

	task, err := store.AddTask(testGroupID, "Buy milk", "Alice", "")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if task.DueDate != "" {
		t.Errorf("expected empty due_date, got %q", task.DueDate)
	}
}

func TestListTasksAll(t *testing.T) {
	store := newTestStore(t)

	store.AddTask(testGroupID, "Task 1", "Bob", "")
	store.AddTask(testGroupID, "Task 2", "Alice", "")
	store.AddTask(testGroupID, "Task 3", "Bob", "")

	tasks, err := store.ListTasks(testGroupID, "", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
}

func TestListTasksByAssignee(t *testing.T) {
	store := newTestStore(t)

	store.AddTask(testGroupID, "Task 1", "Bob", "")
	store.AddTask(testGroupID, "Task 2", "Alice", "")
	store.AddTask(testGroupID, "Task 3", "Bob", "")

	tasks, err := store.ListTasks(testGroupID, "Bob", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks for Bob, got %d", len(tasks))
	}
}

func TestListTasksCaseInsensitive(t *testing.T) {
	store := newTestStore(t)

	store.AddTask(testGroupID, "Task 1", "Bob", "")

	tasks, err := store.ListTasks(testGroupID, "bob", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected case-insensitive match, got %d tasks", len(tasks))
	}
}

func TestListTasksByStatus(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Task 1", "Bob", "")
	store.AddTask(testGroupID, "Task 2", "Bob", "")
	store.UpdateTask(testGroupID, task.ID, TaskUpdate{Status: "done"})

	pending, err := store.ListTasks(testGroupID, "", "pending")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending task, got %d", len(pending))
	}

	done, err := store.ListTasks(testGroupID, "", "done")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(done) != 1 {
		t.Errorf("expected 1 done task, got %d", len(done))
	}
}

func TestUpdateTaskContent(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	updated, err := store.UpdateTask(testGroupID, task.ID, TaskUpdate{Content: "Fix the kitchen sink, please"})
	if err != nil {
		t.Fatalf("UpdateTask content: %v", err)
	}
	if updated.Content != "Fix the kitchen sink, please" {
		t.Errorf("Content: got %q", updated.Content)
	}
	if updated.Assignee != "Bob" {
		t.Errorf("Assignee should be unchanged, got %q", updated.Assignee)
	}
}

func TestUpdateTaskAssignee(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	updated, err := store.UpdateTask(testGroupID, task.ID, TaskUpdate{Assignee: "Alice"})
	if err != nil {
		t.Fatalf("UpdateTask assignee: %v", err)
	}
	if updated.Assignee != "Alice" {
		t.Errorf("Assignee: got %q, want Alice", updated.Assignee)
	}
	if updated.Content != "Fix the sink" {
		t.Errorf("Content should be unchanged, got %q", updated.Content)
	}
}

func TestUpdateTaskCombinedFields(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	updated, err := store.UpdateTask(testGroupID, task.ID, TaskUpdate{
		Content:  "Fix the bathroom sink",
		Assignee: "Alice",
		DueDate:  "2026-06-01",
		Status:   "done",
	})
	if err != nil {
		t.Fatalf("UpdateTask combined: %v", err)
	}
	if updated.Content != "Fix the bathroom sink" || updated.Assignee != "Alice" ||
		updated.DueDate != "2026-06-01" || updated.Status != "done" {
		t.Errorf("combined update did not apply all fields: %+v", updated)
	}
}

func TestUpdateTaskEmptyIsNoOp(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	got, err := store.UpdateTask(testGroupID, task.ID, TaskUpdate{})
	if err != nil {
		t.Fatalf("UpdateTask noop: %v", err)
	}
	if got.Content != "Fix the sink" || got.Assignee != "Bob" {
		t.Errorf("noop update should not change row, got %+v", got)
	}
}

func TestReassignPendingOnly(t *testing.T) {
	store := newTestStore(t)
	t1, _ := store.AddTask(testGroupID, "open task", "Alice", "")
	store.AddTask(testGroupID, "another open", "Alice", "")
	doneTask, _ := store.AddTask(testGroupID, "done task", "Alice", "")
	store.UpdateTask(testGroupID, doneTask.ID, TaskUpdate{Status: "done"})

	n, err := store.ReassignPending(testGroupID, "Alice", "Bob")
	if err != nil {
		t.Fatalf("ReassignPending: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 pending reassignments, got %d", n)
	}

	pending, _ := store.ListTasks(testGroupID, "Bob", "pending")
	if len(pending) != 2 {
		t.Errorf("Bob's pending tasks: got %d, want 2", len(pending))
	}
	// Done task keeps its original assignee.
	doneList, _ := store.ListTasks(testGroupID, "", "done")
	if len(doneList) != 1 || doneList[0].Assignee != "Alice" {
		t.Errorf("done task should keep Alice as assignee: %+v", doneList)
	}
	_ = t1 // reference to silence unused var
}

func TestUpdateTask(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	updated, err := store.UpdateTask(testGroupID, task.ID, TaskUpdate{Status: "done"})
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

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	err := store.DeleteTask(testGroupID, task.ID)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	tasks, _ := store.ListTasks(testGroupID, "", "")
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
	}
}

func TestDeleteTaskNotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteTask(testGroupID, 999)
	if err == nil {
		t.Fatal("expected error deleting nonexistent task")
	}
}

func TestDeleteTaskWrongGroup(t *testing.T) {
	store := newTestStore(t)

	task, _ := store.AddTask(testGroupID, "Fix the sink", "Bob", "")
	err := store.DeleteTask("120363111111@g.us", task.ID)
	if err == nil {
		t.Fatal("expected error: task should not be visible from a different group")
	}

	// Task still exists in its original group.
	tasks, _ := store.ListTasks(testGroupID, "", "")
	if len(tasks) != 1 {
		t.Errorf("expected task to remain in original group, got %d tasks", len(tasks))
	}
}

func TestListTasksScopedByGroup(t *testing.T) {
	store := newTestStore(t)
	groupA := testGroupID
	groupB := "120363AAAAAA@g.us"

	store.AddTask(groupA, "A1", "Alice", "")
	store.AddTask(groupA, "A2", "Bob", "")
	store.AddTask(groupB, "B1", "Alice", "")

	tasksA, _ := store.ListTasks(groupA, "", "")
	if len(tasksA) != 2 {
		t.Errorf("group A: expected 2 tasks, got %d", len(tasksA))
	}
	tasksB, _ := store.ListTasks(groupB, "", "")
	if len(tasksB) != 1 {
		t.Errorf("group B: expected 1 task, got %d", len(tasksB))
	}
}

func TestMetadataScopedByGroup(t *testing.T) {
	store := newTestStore(t)
	groupA := testGroupID
	groupB := "120363AAAAAA@g.us"

	if err := store.SetMeta(groupA, "last_digest_date", "2026-05-01"); err != nil {
		t.Fatalf("SetMeta A: %v", err)
	}
	if err := store.SetMeta(groupB, "last_digest_date", "2026-05-02"); err != nil {
		t.Fatalf("SetMeta B: %v", err)
	}

	a, _ := store.GetMeta(groupA, "last_digest_date")
	if a != "2026-05-01" {
		t.Errorf("group A: got %q, want %q", a, "2026-05-01")
	}
	b, _ := store.GetMeta(groupB, "last_digest_date")
	if b != "2026-05-02" {
		t.Errorf("group B: got %q, want %q", b, "2026-05-02")
	}
}

func TestSetMetaUpsert(t *testing.T) {
	store := newTestStore(t)

	if err := store.SetMeta(testGroupID, "k", "v1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := store.SetMeta(testGroupID, "k", "v2"); err != nil {
		t.Fatalf("SetMeta upsert: %v", err)
	}
	got, _ := store.GetMeta(testGroupID, "k")
	if got != "v2" {
		t.Errorf("expected upserted value v2, got %q", got)
	}
}

func TestCountOverdueByAssigneeScopedByGroup(t *testing.T) {
	store := newTestStore(t)
	groupA := testGroupID
	groupB := "120363AAAAAA@g.us"

	store.AddTask(groupA, "old", "Alice", "2026-01-01")
	store.AddTask(groupA, "old", "Alice", "2026-01-02")
	store.AddTask(groupB, "old", "Alice", "2026-01-01")

	countsA, _ := store.CountOverdueByAssignee(groupA, "2026-05-02")
	if countsA["Alice"] != 2 {
		t.Errorf("group A: expected 2 overdue for Alice, got %d", countsA["Alice"])
	}
	countsB, _ := store.CountOverdueByAssignee(groupB, "2026-05-02")
	if countsB["Alice"] != 1 {
		t.Errorf("group B: expected 1 overdue for Alice, got %d", countsB["Alice"])
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
