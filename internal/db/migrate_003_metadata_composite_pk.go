package db

import (
	"database/sql"
	"fmt"
)

// migrate003MetadataCompositePK recreates the metadata table with a composite
// (group_id, key) primary key, replacing the original (key) PK that didn't
// allow per-group entries with the same key.
//
// SQLite doesn't support ALTER TABLE ... DROP/ADD CONSTRAINT, so this is the
// standard table-rename dance. By the time this migration runs, migrate_002
// has already populated group_id on every existing row, so the NOT NULL
// constraint on the new table is safe.
func migrate003MetadataCompositePK(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE metadata_new (
			group_id TEXT NOT NULL,
			key      TEXT NOT NULL,
			value    TEXT,
			PRIMARY KEY (group_id, key)
		)`,
		`INSERT INTO metadata_new (group_id, key, value)
		 SELECT group_id, key, value FROM metadata WHERE group_id IS NOT NULL`,
		`DROP TABLE metadata`,
		`ALTER TABLE metadata_new RENAME TO metadata`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(s), err)
		}
	}
	return nil
}
