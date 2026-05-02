package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Migration is one schema change applied exactly once. Migrations are applied
// in the order they appear in the registry slice. RunMigrations is idempotent —
// migrations recorded in schema_version are skipped on subsequent runs.
type Migration struct {
	ID   int
	Name string
	Up   func(tx *sql.Tx) error
}

// migrations is the production registry. Append new entries at the end.
// Each migrate_NNN_description function lives in its own file.
var migrations = []Migration{
	{ID: 1, Name: "groups", Up: migrate001Groups},
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

// RunMigrations applies the production registry. Call once at process start
// after the base stores have created their CREATE TABLE IF NOT EXISTS schema
// and before the HTTP mux is bound.
func RunMigrations(database *sql.DB) error {
	return runMigrations(database, migrations)
}

func runMigrations(database *sql.DB, ms []Migration) error {
	if _, err := database.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			id INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
	`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	applied, err := loadAppliedVersions(database)
	if err != nil {
		return fmt.Errorf("load applied versions: %w", err)
	}

	for _, m := range ms {
		if applied[m.ID] {
			continue
		}
		if err := applyMigration(database, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.ID, m.Name, err)
		}
		slog.Info("migration applied", slog.Int("id", m.ID), slog.String("name", m.Name))
	}
	return nil
}

func loadAppliedVersions(database *sql.DB) (map[int]bool, error) {
	rows, err := database.Query("SELECT id FROM schema_version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		applied[id] = true
	}
	return applied, rows.Err()
}

func applyMigration(database *sql.DB, m Migration) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := m.Up(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO schema_version (id, applied_at) VALUES (?, ?)",
		m.ID, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit()
}
