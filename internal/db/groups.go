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
	DigestHour       int    // 0 if NULL
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
