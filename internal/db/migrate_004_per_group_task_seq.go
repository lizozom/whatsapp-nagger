package db

import (
	"database/sql"
	"fmt"
)

// migrate004PerGroupTaskSeq adds a per-group task sequence number so each
// tenant sees their own 1, 2, 3, ... numbering instead of SQLite's globally
// monotonic rowid (which leaks "the operator has 47 other tasks" via the
// numbering jump). The internal `id` column stays as the SQLite rowid for
// any external references, but `group_seq` is what user-facing tools expose.
//
// Backfill: for each existing row, group_seq is row_number() partitioned by
// group_id ordered by id (creation order). Tenant-zero's existing 67 tasks
// become 1..67 in their group's namespace.
//
// Post-migration constraints: every task row has a non-null group_seq, and
// (group_id, group_seq) is unique.
func migrate004PerGroupTaskSeq(tx *sql.Tx) error {
	stmts := []string{
		`ALTER TABLE tasks ADD COLUMN group_seq INTEGER`,
		// Backfill: row_number() over partition by group_id, ordered by id.
		// SQLite supports window functions since 3.25 (modernc.org/sqlite has it).
		`UPDATE tasks SET group_seq = (
			SELECT n FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY id) AS n
				FROM tasks
			) ranked WHERE ranked.id = tasks.id
		)`,
		// Verify backfill — no NULLs allowed.
		// (We can't add NOT NULL via ALTER TABLE in SQLite, so the index +
		//  application-layer invariant carry the constraint instead.)
		`CREATE UNIQUE INDEX idx_tasks_group_seq ON tasks(group_id, group_seq)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(s), err)
		}
	}

	// Post-condition: zero NULL group_seq.
	var nullCount int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE group_seq IS NULL").Scan(&nullCount); err != nil {
		return fmt.Errorf("verify backfill: %w", err)
	}
	if nullCount != 0 {
		return fmt.Errorf("post-condition failed: %d tasks have NULL group_seq after backfill", nullCount)
	}
	return nil
}
