package db

import (
	"database/sql"
	"fmt"
)

// migrate001Groups creates the multi-tenancy schema:
//   - groups table (one row per WhatsApp group; id is the JID directly)
//   - members table (composite PK (group_id, whatsapp_id))
//   - group_id column added (nullable) to tasks, metadata, transactions
//   - indexes on every group_id column
//
// All columns are nullable except where noted; backfill of existing
// tenant-zero rows happens in migrate_002 (Story 1.4).
func migrate001Groups(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE groups (
			id                TEXT PRIMARY KEY,
			name              TEXT,
			language          TEXT,
			timezone          TEXT,
			digest_hour       INTEGER,
			onboarding_state  TEXT,
			financial_enabled INTEGER NOT NULL DEFAULT 0,
			created_at        TEXT NOT NULL,
			last_active_at    TEXT
		)`,
		`CREATE TABLE members (
			group_id     TEXT NOT NULL,
			whatsapp_id  TEXT NOT NULL,
			display_name TEXT,
			created_at   TEXT NOT NULL,
			PRIMARY KEY (group_id, whatsapp_id)
		)`,
		`ALTER TABLE tasks ADD COLUMN group_id TEXT`,
		`ALTER TABLE metadata ADD COLUMN group_id TEXT`,
		`ALTER TABLE transactions ADD COLUMN group_id TEXT`,
		`CREATE INDEX idx_tasks_group_id ON tasks(group_id)`,
		`CREATE INDEX idx_metadata_group_id ON metadata(group_id)`,
		`CREATE INDEX idx_transactions_group_id ON transactions(group_id)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(s), err)
		}
	}
	return nil
}
