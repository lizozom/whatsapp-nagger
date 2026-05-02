package db

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// migrate002BackfillTenantZero materializes Liza's existing single-tenant
// state into the new multi-tenancy schema:
//
//   - Inserts one groups row keyed by WHATSAPP_GROUP_JID with onboarding_state
//     = "complete" and financial_enabled = true.
//   - Inserts members rows from personas.md (one per name → phone mapping).
//   - Backfills group_id on every existing tasks / metadata / transactions row
//     to that JID.
//
// Post-conditions verified: SELECT COUNT(*) WHERE group_id IS NULL = 0 across
// all three scoped tables. Verification failure rolls back the whole migration.
//
// If WHATSAPP_GROUP_JID is empty (e.g. fresh install or friend-only deploy)
// the migration is a no-op; it still records as applied so subsequent runs
// don't re-execute.
func migrate002BackfillTenantZero(tx *sql.Tx) error {
	jid := os.Getenv("WHATSAPP_GROUP_JID")
	if jid == "" {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if err := backfillTenantZeroGroup(tx, jid, now); err != nil {
		return err
	}
	if err := backfillTenantZeroMembers(tx, jid, now); err != nil {
		return err
	}
	if err := backfillScopedTables(tx, jid); err != nil {
		return err
	}
	return verifyZeroNullGroupID(tx)
}

func backfillTenantZeroGroup(tx *sql.Tx, jid, now string) error {
	timezone := envOrDefault("TIMEZONE", "Asia/Jerusalem")
	language := envOrDefault("TENANT_ZERO_LANGUAGE", "he")
	name := envOrDefault("TENANT_ZERO_NAME", "tenant-zero")
	digestHour := parseDigestHour(os.Getenv("DIGEST_HOUR"))

	_, err := tx.Exec(`
		INSERT OR IGNORE INTO groups
			(id, name, language, timezone, digest_hour,
			 onboarding_state, financial_enabled, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, 'complete', 1, ?, ?)`,
		jid, name, language, timezone, digestHour, now, now,
	)
	if err != nil {
		return fmt.Errorf("insert tenant-zero group: %w", err)
	}
	return nil
}

func backfillTenantZeroMembers(tx *sql.Tx, jid, now string) error {
	personas := readPersonasFile()
	if personas == "" {
		return nil
	}
	for name, phone := range parsePersonaPhones(personas) {
		_, err := tx.Exec(`
			INSERT OR IGNORE INTO members
				(group_id, whatsapp_id, display_name, created_at)
			VALUES (?, ?, ?, ?)`,
			jid, phone, name, now,
		)
		if err != nil {
			return fmt.Errorf("insert member %s: %w", name, err)
		}
	}
	return nil
}

func backfillScopedTables(tx *sql.Tx, jid string) error {
	for _, table := range []string{"tasks", "metadata", "transactions"} {
		_, err := tx.Exec(
			"UPDATE "+table+" SET group_id = ? WHERE group_id IS NULL",
			jid,
		)
		if err != nil {
			return fmt.Errorf("backfill %s.group_id: %w", table, err)
		}
	}
	return nil
}

func verifyZeroNullGroupID(tx *sql.Tx) error {
	for _, table := range []string{"tasks", "metadata", "transactions"} {
		var n int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM " + table + " WHERE group_id IS NULL",
		).Scan(&n); err != nil {
			return fmt.Errorf("verify %s: %w", table, err)
		}
		if n != 0 {
			return fmt.Errorf("post-condition failed: %s has %d rows with NULL group_id", table, n)
		}
	}
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseDigestHour accepts "9", "09", or "09:00" and returns the integer hour.
// Returns 0 on parse failure (also the default if env var is unset).
func parseDigestHour(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if i := strings.Index(raw, ":"); i >= 0 {
		raw = raw[:i]
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return v
}

// readPersonasFile reads the personas markdown from disk. Mirrors the
// production lookup order in internal/agent: PERSONAS_FILE env wins, else
// "personas.md" in the working directory. Returns "" on any failure — the
// caller decides whether absence is fatal.
func readPersonasFile() string {
	path := os.Getenv("PERSONAS_FILE")
	if path == "" {
		path = "personas.md"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// parsePersonaPhones extracts name → phone mappings from personas markdown.
// Format: "## Name" headers followed by "**Phone:** 972..." lines anywhere
// in the section. Phone digits are captured verbatim.
//
// Mirrors agent.ParsePersonaPhones — duplicated here intentionally to keep
// the db package free of upward dependencies. Personas parsing is small
// enough that duplication beats a circular import.
func parsePersonaPhones(personas string) map[string]string {
	phones := make(map[string]string)
	nameRe := regexp.MustCompile(`(?m)^## (.+)`)
	phoneRe := regexp.MustCompile(`(?i)\*\*Phone:\*\*\s*(\d+)`)

	matches := nameRe.FindAllStringSubmatchIndex(personas, -1)
	for i, match := range matches {
		name := strings.TrimSpace(personas[match[2]:match[3]])
		end := len(personas)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		section := personas[match[0]:end]
		if pm := phoneRe.FindStringSubmatch(section); len(pm) > 1 {
			phones[name] = pm[1]
		}
	}
	return phones
}
