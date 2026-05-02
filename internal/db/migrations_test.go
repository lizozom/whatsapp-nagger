package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestRunMigrationsCreatesSchemaVersionTable(t *testing.T) {
	database := newMigrationTestDB(t)
	if err := runMigrations(database, nil); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	var count int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'",
	).Scan(&count); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if count != 1 {
		t.Errorf("expected schema_version table to exist, got count=%d", count)
	}
}

func TestRunMigrationsAppliesPendingInOrder(t *testing.T) {
	database := newMigrationTestDB(t)
	var applied []int
	ms := []Migration{
		{ID: 1, Name: "first", Up: func(tx *sql.Tx) error {
			applied = append(applied, 1)
			return nil
		}},
		{ID: 2, Name: "second", Up: func(tx *sql.Tx) error {
			applied = append(applied, 2)
			return nil
		}},
		{ID: 3, Name: "third", Up: func(tx *sql.Tx) error {
			applied = append(applied, 3)
			return nil
		}},
	}
	if err := runMigrations(database, ms); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if len(applied) != 3 || applied[0] != 1 || applied[1] != 2 || applied[2] != 3 {
		t.Errorf("expected [1,2,3] applied in order, got %v", applied)
	}

	var rowCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&rowCount); err != nil {
		t.Fatalf("query schema_version count: %v", err)
	}
	if rowCount != 3 {
		t.Errorf("expected 3 schema_version rows, got %d", rowCount)
	}
}

func TestRunMigrationsIdempotentOnSecondRun(t *testing.T) {
	database := newMigrationTestDB(t)
	runs := 0
	ms := []Migration{
		{ID: 1, Name: "first", Up: func(tx *sql.Tx) error {
			runs++
			return nil
		}},
	}
	if err := runMigrations(database, ms); err != nil {
		t.Fatalf("first runMigrations: %v", err)
	}
	if err := runMigrations(database, ms); err != nil {
		t.Fatalf("second runMigrations: %v", err)
	}
	if runs != 1 {
		t.Errorf("expected migration to run exactly once across two RunMigrations calls, got %d", runs)
	}
}

func TestRunMigrationsAppliesOnlyMissing(t *testing.T) {
	database := newMigrationTestDB(t)
	runsByID := map[int]int{}
	ms1 := []Migration{
		{ID: 1, Name: "first", Up: func(tx *sql.Tx) error {
			runsByID[1]++
			return nil
		}},
	}
	if err := runMigrations(database, ms1); err != nil {
		t.Fatalf("first runMigrations: %v", err)
	}

	ms2 := []Migration{
		ms1[0],
		{ID: 2, Name: "second", Up: func(tx *sql.Tx) error {
			runsByID[2]++
			return nil
		}},
	}
	if err := runMigrations(database, ms2); err != nil {
		t.Fatalf("second runMigrations: %v", err)
	}

	if runsByID[1] != 1 {
		t.Errorf("migration 1 should run exactly once, ran %d times", runsByID[1])
	}
	if runsByID[2] != 1 {
		t.Errorf("migration 2 should run exactly once, ran %d times", runsByID[2])
	}
}

func TestRunMigrationsRollsBackFailedMigration(t *testing.T) {
	database := newMigrationTestDB(t)
	ms := []Migration{
		{ID: 1, Name: "first-succeeds", Up: func(tx *sql.Tx) error {
			_, err := tx.Exec("CREATE TABLE evidence_first (x INT)")
			return err
		}},
		{ID: 2, Name: "second-fails-after-write", Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec("CREATE TABLE evidence_second (x INT)"); err != nil {
				return err
			}
			return fmt.Errorf("intentional failure")
		}},
		{ID: 3, Name: "third-should-not-run", Up: func(tx *sql.Tx) error {
			t.Errorf("migration 3 should not run after migration 2 fails")
			return nil
		}},
	}

	err := runMigrations(database, ms)
	if err == nil {
		t.Fatal("expected error from migration 2")
	}

	var firstExists, secondExists int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='evidence_first'",
	).Scan(&firstExists); err != nil {
		t.Fatalf("query evidence_first: %v", err)
	}
	if firstExists != 1 {
		t.Error("migration 1 should have committed; evidence_first table missing")
	}
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='evidence_second'",
	).Scan(&secondExists); err != nil {
		t.Fatalf("query evidence_second: %v", err)
	}
	if secondExists != 0 {
		t.Error("migration 2 failure should have rolled back; evidence_second should not exist")
	}

	var schemaCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM schema_version WHERE id IN (2,3)").Scan(&schemaCount); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if schemaCount != 0 {
		t.Errorf("failed/skipped migrations should not be recorded, found %d rows", schemaCount)
	}
}

func TestRunMigrationsEmptyRegistry(t *testing.T) {
	database := newMigrationTestDB(t)
	if err := runMigrations(database, nil); err != nil {
		t.Fatalf("first run with nil registry: %v", err)
	}
	if err := runMigrations(database, nil); err != nil {
		t.Fatalf("second run with nil registry: %v", err)
	}
}

func TestRunMigrationsRecordsAppliedTimestamp(t *testing.T) {
	database := newMigrationTestDB(t)
	ms := []Migration{
		{ID: 1, Name: "first", Up: func(tx *sql.Tx) error { return nil }},
	}
	if err := runMigrations(database, ms); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	var appliedAt string
	if err := database.QueryRow("SELECT applied_at FROM schema_version WHERE id = 1").Scan(&appliedAt); err != nil {
		t.Fatalf("query applied_at: %v", err)
	}
	if appliedAt == "" {
		t.Error("expected non-empty applied_at timestamp")
	}
}
