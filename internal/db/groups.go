package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Group is one tenant — a WhatsApp group the bot operates in. Identified by
// the WhatsApp JID directly (no surrogate key per architecture D4).
type Group struct {
	ID               string
	Name             string
	Language         string // "" if NULL — e.g. mid-onboarding before set_language
	Timezone         string
	DigestHour       int  // 0 if NULL — see DigestHourSet to disambiguate
	DigestHourSet    bool // true iff the column is non-NULL (0 is a valid hour)
	OnboardingState  string // "in_progress" or "complete"
	FinancialEnabled bool
	CreatedAt        string
	LastActiveAt     string
}

// Member is a person within a Group. Composite primary key (group_id, whatsapp_id).
type Member struct {
	GroupID     string
	WhatsAppID  string
	DisplayName string
	CreatedAt   string
}

// GroupStore manages the groups table.
type GroupStore struct {
	db *sql.DB
}

func NewGroupStore(dbPath string) (*GroupStore, error) {
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	return &GroupStore{db: database}, nil
}

func (s *GroupStore) Close() error {
	return s.db.Close()
}

// Get returns the group row for the given JID, or (nil, nil) if no row exists.
// Errors are returned only for unexpected SQL failures.
func (s *GroupStore) Get(ctx context.Context, groupID string) (*Group, error) {
	var (
		g          Group
		name       sql.NullString
		language   sql.NullString
		timezone   sql.NullString
		digestHour sql.NullInt64
		onboarding sql.NullString
		lastActive sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, language, timezone, digest_hour, onboarding_state,
		       financial_enabled, created_at, last_active_at
		FROM groups WHERE id = ?`, groupID,
	).Scan(&g.ID, &name, &language, &timezone, &digestHour, &onboarding,
		&g.FinancialEnabled, &g.CreatedAt, &lastActive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get group %s: %w", groupID, err)
	}
	g.Name = name.String
	g.Language = language.String
	g.Timezone = timezone.String
	if digestHour.Valid {
		g.DigestHour = int(digestHour.Int64)
		g.DigestHourSet = true
	}
	g.OnboardingState = onboarding.String
	g.LastActiveAt = lastActive.String
	return &g, nil
}

// Create inserts a new group row. CreatedAt and LastActiveAt default to now if empty.
func (s *GroupStore) Create(ctx context.Context, g Group) error {
	if g.CreatedAt == "" {
		g.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if g.LastActiveAt == "" {
		g.LastActiveAt = g.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO groups (id, name, language, timezone, digest_hour,
		                    onboarding_state, financial_enabled, created_at, last_active_at)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0),
		        NULLIF(?, ''), ?, ?, ?)`,
		g.ID, g.Name, g.Language, g.Timezone, g.DigestHour,
		g.OnboardingState, boolToInt(g.FinancialEnabled), g.CreatedAt, g.LastActiveAt,
	)
	if err != nil {
		return fmt.Errorf("create group %s: %w", g.ID, err)
	}
	return nil
}

// UpdateLastActive sets last_active_at = now() for the group. Called by the
// dispatcher after each successful inbound message handle (architecture D18).
func (s *GroupStore) UpdateLastActive(ctx context.Context, groupID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE groups SET last_active_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), groupID,
	)
	if err != nil {
		return fmt.Errorf("update last_active_at for %s: %w", groupID, err)
	}
	return nil
}

// MemberCap is the per-group member maximum (NFR3). Enforced by AutoCreate
// (which silently truncates) and by the future add_member tool (which refuses).
const MemberCap = 2

// AutoCreate inserts a fresh groups row in onboarding state plus member
// rows for the supplied allowlisted phones, transactionally. The members
// list is truncated to MemberCap (NFR3 — extras are ignored at this layer;
// add_member will surface the cap as a refusal in Story 2.6).
//
// language/timezone/digest_hour are NULL — onboarding fills them in.
// financial_enabled is 0 — flipping it is operator-only (DB direct, no tool/chat path).
func (s *GroupStore) AutoCreate(ctx context.Context, groupID, name string, allowlistedPhones []string) error {
	if len(allowlistedPhones) > MemberCap {
		allowlistedPhones = allowlistedPhones[:MemberCap]
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO groups (id, name, language, timezone, digest_hour,
		                    onboarding_state, financial_enabled, created_at, last_active_at)
		VALUES (?, NULLIF(?, ''), NULL, NULL, NULL, 'in_progress', 0, ?, ?)`,
		groupID, name, now, now,
	); err != nil {
		return fmt.Errorf("insert group %s: %w", groupID, err)
	}

	for _, phone := range allowlistedPhones {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO members (group_id, whatsapp_id, display_name, created_at)
			VALUES (?, ?, NULL, ?)`,
			groupID, phone, now,
		); err != nil {
			return fmt.Errorf("insert member %s/%s: %w", groupID, phone, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit auto-create %s: %w", groupID, err)
	}
	return nil
}

// ListComplete returns all groups whose onboarding_state = "complete". Used
// by the digest + nag schedulers to iterate live tenants (D14). Mid-onboarding
// groups are excluded — they have no language/timezone/digest_hour yet.
func (s *GroupStore) ListComplete(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, language, timezone, digest_hour,
		       onboarding_state, financial_enabled, created_at, last_active_at
		FROM groups WHERE onboarding_state = 'complete'`,
	)
	if err != nil {
		return nil, fmt.Errorf("list complete groups: %w", err)
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var (
			g          Group
			name       sql.NullString
			language   sql.NullString
			timezone   sql.NullString
			digestHour sql.NullInt64
			onboarding sql.NullString
			lastActive sql.NullString
		)
		if err := rows.Scan(&g.ID, &name, &language, &timezone, &digestHour,
			&onboarding, &g.FinancialEnabled, &g.CreatedAt, &lastActive); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		g.Name = name.String
		g.Language = language.String
		g.Timezone = timezone.String
		if digestHour.Valid {
			g.DigestHour = int(digestHour.Int64)
			g.DigestHourSet = true
		}
		g.OnboardingState = onboarding.String
		g.LastActiveAt = lastActive.String
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetName writes groups.name. Called by the update_group_settings tool.
func (s *GroupStore) SetName(ctx context.Context, groupID, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET name = NULLIF(?, '') WHERE id = ?`, name, groupID)
	if err != nil {
		return fmt.Errorf("set name for %s: %w", groupID, err)
	}
	return nil
}

// SetLanguage writes groups.language. Called only by the onboarding agent's
// set_language tool (language is locked at first set per NFR4).
func (s *GroupStore) SetLanguage(ctx context.Context, groupID, language string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET language = ? WHERE id = ?`, language, groupID)
	if err != nil {
		return fmt.Errorf("set language for %s: %w", groupID, err)
	}
	return nil
}

// SetTimezone writes groups.timezone. Caller must have validated the IANA name.
func (s *GroupStore) SetTimezone(ctx context.Context, groupID, timezone string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET timezone = ? WHERE id = ?`, timezone, groupID)
	if err != nil {
		return fmt.Errorf("set timezone for %s: %w", groupID, err)
	}
	return nil
}

// SetDigestHour writes groups.digest_hour (0..23). Caller validates the range.
func (s *GroupStore) SetDigestHour(ctx context.Context, groupID string, hour int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET digest_hour = ? WHERE id = ?`, hour, groupID)
	if err != nil {
		return fmt.Errorf("set digest_hour for %s: %w", groupID, err)
	}
	return nil
}

// MarkComplete sets onboarding_state = "complete". Called by the
// complete_onboarding tool once all required fields are captured.
func (s *GroupStore) MarkComplete(ctx context.Context, groupID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE groups SET onboarding_state = 'complete' WHERE id = ?`,
		groupID,
	)
	if err != nil {
		return fmt.Errorf("mark complete for %s: %w", groupID, err)
	}
	return nil
}

// MemberStore manages the members table.
type MemberStore struct {
	db *sql.DB
}

func NewMemberStore(dbPath string) (*MemberStore, error) {
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	return &MemberStore{db: database}, nil
}

func (s *MemberStore) Close() error {
	return s.db.Close()
}

// List returns members of a group ordered by created_at ascending.
func (s *MemberStore) List(ctx context.Context, groupID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT group_id, whatsapp_id, COALESCE(display_name, ''), created_at
		FROM members WHERE group_id = ?
		ORDER BY created_at ASC`, groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("list members for %s: %w", groupID, err)
	}
	defer rows.Close()
	var ms []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.GroupID, &m.WhatsAppID, &m.DisplayName, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		ms = append(ms, m)
	}
	return ms, rows.Err()
}

// Upsert inserts or updates a member row by (group_id, whatsapp_id). Used
// by the onboarding set_member tool to allow correcting names mid-flow.
// Caller is responsible for enforcing the per-group MemberCap.
func (s *MemberStore) Upsert(ctx context.Context, groupID string, m Member) error {
	if m.CreatedAt == "" {
		m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO members (group_id, whatsapp_id, display_name, created_at)
		VALUES (?, ?, NULLIF(?, ''), ?)
		ON CONFLICT (group_id, whatsapp_id) DO UPDATE SET display_name = excluded.display_name`,
		groupID, m.WhatsAppID, m.DisplayName, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert member %s/%s: %w", groupID, m.WhatsAppID, err)
	}
	return nil
}

// UpdateName changes a member's display_name. Returns an error if the row
// doesn't exist. Caller is responsible for cascading to tasks.assignee.
func (s *MemberStore) UpdateName(ctx context.Context, groupID, whatsappID, newName string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE members SET display_name = NULLIF(?, '')
		WHERE group_id = ? AND whatsapp_id = ?`,
		newName, groupID, whatsappID,
	)
	if err != nil {
		return fmt.Errorf("update member name %s/%s: %w", groupID, whatsappID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("member %s not found in group %s", whatsappID, groupID)
	}
	return nil
}

// Remove deletes a member row. Returns an error if the row doesn't exist.
// Caller is responsible for reassigning tasks before calling.
func (s *MemberStore) Remove(ctx context.Context, groupID, whatsappID string) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM members WHERE group_id = ? AND whatsapp_id = ?`,
		groupID, whatsappID,
	)
	if err != nil {
		return fmt.Errorf("remove member %s/%s: %w", groupID, whatsappID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("member %s not found in group %s", whatsappID, groupID)
	}
	return nil
}

// Add inserts a member row. The composite PK (group_id, whatsapp_id) means
// duplicates fail with a constraint error.
func (s *MemberStore) Add(ctx context.Context, groupID string, m Member) error {
	if m.CreatedAt == "" {
		m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO members (group_id, whatsapp_id, display_name, created_at)
		VALUES (?, ?, NULLIF(?, ''), ?)`,
		groupID, m.WhatsAppID, m.DisplayName, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("add member %s/%s: %w", groupID, m.WhatsAppID, err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
