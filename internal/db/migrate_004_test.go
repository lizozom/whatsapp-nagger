package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrate004BackfillsExistingTasksWithPerGroupSeq verifies the row_number
// backfill: each group's existing tasks get sequential 1..N numbering ordered
// by SQLite rowid (creation order).
func TestMigrate004BackfillsExistingTasksWithPerGroupSeq(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Bootstrap base tables.
	taskStore, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	taskStore.Close()
	txStore, _ := NewTxStore(dbPath)
	txStore.Close()

	// Run migrations 1+2+3 first (need group_id column on tasks for the seed).
	migDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Setenv("WHATSAPP_GROUP_JID", "")
	t.Cleanup(func() { migDB.Close() })

	if err := runMigrations(migDB, []Migration{
		{ID: 1, Name: "groups", Up: migrate001Groups},
		{ID: 2, Name: "backfill_tenant_zero", Up: migrate002BackfillTenantZero},
		{ID: 3, Name: "metadata_composite_pk", Up: migrate003MetadataCompositePK},
	}); err != nil {
		t.Fatalf("pre-migrations: %v", err)
	}

	// Seed tasks across two groups in interleaved insertion order.
	groupA := "120363AAAA@g.us"
	groupB := "120363BBBB@g.us"
	for _, q := range []struct {
		group, content string
	}{
		{groupA, "A1"},
		{groupA, "A2"},
		{groupB, "B1"},
		{groupA, "A3"},
		{groupB, "B2"},
		{groupB, "B3"},
	} {
		if _, err := migDB.Exec(
			`INSERT INTO tasks (group_id, content, assignee, status) VALUES (?, ?, 'Alice', 'pending')`,
			q.group, q.content,
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Apply migration 004.
	if err := runMigrations(migDB, []Migration{
		{ID: 4, Name: "per_group_task_seq", Up: migrate004PerGroupTaskSeq},
	}); err != nil {
		t.Fatalf("migration 004: %v", err)
	}

	// Verify per-group sequence: A → 1, 2, 3 in insertion order; B → 1, 2, 3.
	rows, err := migDB.Query(`SELECT group_id, content, group_seq FROM tasks ORDER BY group_id, group_seq`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type row struct{ group, content string; seq int64 }
	var got []row
	for rows.Next() {
		var r row
		rows.Scan(&r.group, &r.content, &r.seq)
		got = append(got, r)
	}
	want := []row{
		{groupA, "A1", 1}, {groupA, "A2", 2}, {groupA, "A3", 3},
		{groupB, "B1", 1}, {groupB, "B2", 2}, {groupB, "B3", 3},
	}
	if len(got) != len(want) {
		t.Fatalf("rows: got %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestMigrate004IsIdempotent verifies a second run is a no-op (it's recorded
// in schema_version, not re-applied).
func TestMigrate004IsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	taskStore, _ := NewTaskStore(dbPath)
	taskStore.Close()
	txStore, _ := NewTxStore(dbPath)
	txStore.Close()

	migDB, _ := sql.Open("sqlite", dbPath)
	defer migDB.Close()
	t.Setenv("WHATSAPP_GROUP_JID", "")

	if err := RunMigrations(migDB); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := RunMigrations(migDB); err != nil {
		t.Fatalf("second run (should be no-op): %v", err)
	}
}
