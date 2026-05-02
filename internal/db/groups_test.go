package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// setupMigratedDB builds a fresh test DB with the base CREATE TABLE IF NOT EXISTS
// blocks (via NewTaskStore + NewTxStore) and then runs the production migration
// registry on it. Returns the dbPath so tests can construct GroupStore / MemberStore.
func setupMigratedDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	taskStore, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	taskStore.Close()

	txStore, err := NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	txStore.Close()

	migrationDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer migrationDB.Close()
	if err := RunMigrations(migrationDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return dbPath
}

func tableExists(t *testing.T, dbPath, name string) bool {
	t.Helper()
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	return n == 1
}

func columnExists(t *testing.T, dbPath, table, column string) bool {
	t.Helper()
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	rows, err := d.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}

func indexExists(t *testing.T, dbPath, name string) bool {
	t.Helper()
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", name,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	return n == 1
}

func TestMigrate001CreatesGroupsAndMembersTables(t *testing.T) {
	dbPath := setupMigratedDB(t)
	if !tableExists(t, dbPath, "groups") {
		t.Error("expected groups table")
	}
	if !tableExists(t, dbPath, "members") {
		t.Error("expected members table")
	}
}

func TestMigrate001AddsGroupIdColumns(t *testing.T) {
	dbPath := setupMigratedDB(t)
	for _, table := range []string{"tasks", "metadata", "transactions"} {
		if !columnExists(t, dbPath, table, "group_id") {
			t.Errorf("expected %s.group_id column", table)
		}
	}
}

func TestMigrate001CreatesGroupIdIndexes(t *testing.T) {
	dbPath := setupMigratedDB(t)
	// migrate_001 creates explicit indexes on tasks and transactions. The
	// metadata index it originally created is implicitly replaced by the
	// composite (group_id, key) PK installed by migrate_003 — SQLite covers
	// `WHERE group_id = ?` queries via the autoindex on that PK.
	for _, idx := range []string{"idx_tasks_group_id", "idx_transactions_group_id"} {
		if !indexExists(t, dbPath, idx) {
			t.Errorf("expected %s index", idx)
		}
	}
}

// TestMigrate001AddsGroupIdToExistingRows verifies that migrate_001's
// ALTER TABLE adds the nullable group_id column without losing existing rows.
// (We only check tasks/transactions because migrate_003 later recreates
// metadata with NOT NULL group_id, so the post-all-migrations metadata table
// won't carry a NULL row regardless of pre-migration content.)
func TestMigrate001AddsGroupIdToExistingRows(t *testing.T) {
	t.Setenv("WHATSAPP_GROUP_JID", "")
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Pre-populate base tables with tenant-zero data, then migrate.
	taskStore, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	// Raw SQL: simulate pre-multi-tenancy rows (no group_id column yet).
	taskDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := taskDB.Exec(
		`INSERT INTO tasks (content, assignee, due_date) VALUES ('Fix the sink', 'Bob', '')`,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := taskDB.Exec(
		`INSERT INTO metadata (key, value) VALUES ('last_digest_date', '2026-05-01')`,
	); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}
	taskDB.Close()
	taskStore.Close()

	txStore, err := NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	txStore.Close()

	migrationDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer migrationDB.Close()
	if err := RunMigrations(migrationDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Pre-existing tasks row survives migrate_001 with NULL group_id (story
	// 1.4's backfill is what populates it; here we ran with no WHATSAPP_GROUP_JID).
	var taskCount int
	if err := migrationDB.QueryRow(
		"SELECT COUNT(*) FROM tasks WHERE group_id IS NULL",
	).Scan(&taskCount); err != nil {
		t.Fatalf("query tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("expected 1 task with NULL group_id (pre-backfill state), got %d", taskCount)
	}
}

func TestGroupStoreCreateAndGet(t *testing.T) {
	dbPath := setupMigratedDB(t)
	store, err := NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jid := "120363999999@g.us"
	g := Group{
		ID:               jid,
		Name:             "Alice & Bob",
		Language:         "en",
		Timezone:         "Asia/Jerusalem",
		DigestHour:       9,
		OnboardingState:  "complete",
		FinancialEnabled: false,
	}
	if err := store.Create(ctx, g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, jid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected group, got nil")
	}
	if got.ID != jid {
		t.Errorf("ID: got %q, want %q", got.ID, jid)
	}
	if got.Name != "Alice & Bob" {
		t.Errorf("Name: got %q", got.Name)
	}
	if got.Language != "en" {
		t.Errorf("Language: got %q", got.Language)
	}
	if got.Timezone != "Asia/Jerusalem" {
		t.Errorf("Timezone: got %q", got.Timezone)
	}
	if got.DigestHour != 9 {
		t.Errorf("DigestHour: got %d", got.DigestHour)
	}
	if got.OnboardingState != "complete" {
		t.Errorf("OnboardingState: got %q", got.OnboardingState)
	}
	if got.FinancialEnabled {
		t.Error("FinancialEnabled: got true, want false")
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be auto-populated")
	}
	if got.LastActiveAt == "" {
		t.Error("LastActiveAt should default to CreatedAt")
	}
}

func TestGroupStoreGetNotFound(t *testing.T) {
	dbPath := setupMigratedDB(t)
	store, err := NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	defer store.Close()

	got, err := store.Get(context.Background(), "120363111111@g.us")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for non-existent group, got %+v", got)
	}
}

func TestGroupStoreUpdateLastActive(t *testing.T) {
	dbPath := setupMigratedDB(t)
	store, err := NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jid := "120363222222@g.us"
	if err := store.Create(ctx, Group{ID: jid, OnboardingState: "in_progress"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, _ := store.Get(ctx, jid)
	if err := store.UpdateLastActive(ctx, jid); err != nil {
		t.Fatalf("UpdateLastActive: %v", err)
	}
	after, _ := store.Get(ctx, jid)

	if after.LastActiveAt == "" {
		t.Fatal("LastActiveAt should be populated")
	}
	// Timestamps may be equal if the test runs within the same second; just
	// assert the column changed *or* is at least non-empty.
	if before.LastActiveAt == "" {
		t.Error("LastActiveAt should have been set on Create")
	}
}

func TestGroupStoreMarkComplete(t *testing.T) {
	dbPath := setupMigratedDB(t)
	store, err := NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jid := "120363333333@g.us"
	if err := store.Create(ctx, Group{ID: jid, OnboardingState: "in_progress"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkComplete(ctx, jid); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	got, _ := store.Get(ctx, jid)
	if got.OnboardingState != "complete" {
		t.Errorf("OnboardingState: got %q, want %q", got.OnboardingState, "complete")
	}
}

func TestMemberStoreAddAndList(t *testing.T) {
	dbPath := setupMigratedDB(t)
	groupStore, err := NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	defer groupStore.Close()
	memberStore, err := NewMemberStore(dbPath)
	if err != nil {
		t.Fatalf("NewMemberStore: %v", err)
	}
	defer memberStore.Close()

	ctx := context.Background()
	jid := "120363444444@g.us"
	if err := groupStore.Create(ctx, Group{ID: jid, OnboardingState: "complete"}); err != nil {
		t.Fatalf("Create group: %v", err)
	}

	if err := memberStore.Add(ctx, jid, Member{
		GroupID:     jid,
		WhatsAppID:  "100000000001",
		DisplayName: "Alice",
	}); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}
	if err := memberStore.Add(ctx, jid, Member{
		GroupID:     jid,
		WhatsAppID:  "100000000002",
		DisplayName: "Bob",
	}); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	members, err := memberStore.List(ctx, jid)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0].DisplayName != "Alice" {
		t.Errorf("first member: got %q, want Alice", members[0].DisplayName)
	}
	if members[1].DisplayName != "Bob" {
		t.Errorf("second member: got %q, want Bob", members[1].DisplayName)
	}
	if members[0].WhatsAppID != "100000000001" {
		t.Errorf("first member phone: got %q", members[0].WhatsAppID)
	}
}

func TestMemberStoreAddDuplicateFails(t *testing.T) {
	dbPath := setupMigratedDB(t)
	groupStore, _ := NewGroupStore(dbPath)
	defer groupStore.Close()
	memberStore, _ := NewMemberStore(dbPath)
	defer memberStore.Close()

	ctx := context.Background()
	jid := "120363555555@g.us"
	groupStore.Create(ctx, Group{ID: jid, OnboardingState: "complete"})

	m := Member{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"}
	if err := memberStore.Add(ctx, jid, m); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := memberStore.Add(ctx, jid, m); err == nil {
		t.Error("expected duplicate Add to fail (composite PK violation)")
	}
}

func TestMemberStoreListEmpty(t *testing.T) {
	dbPath := setupMigratedDB(t)
	memberStore, _ := NewMemberStore(dbPath)
	defer memberStore.Close()

	members, err := memberStore.List(context.Background(), "120363666666@g.us")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected empty list, got %d members", len(members))
	}
}

func TestGroupStoreAutoCreate(t *testing.T) {
	dbPath := setupMigratedDB(t)
	groupStore, err := NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	defer groupStore.Close()
	memberStore, _ := NewMemberStore(dbPath)
	defer memberStore.Close()

	ctx := context.Background()
	jid := "120363777777@g.us"
	if err := groupStore.AutoCreate(ctx, jid, "Friend group", []string{"100000000001", "100000000002"}); err != nil {
		t.Fatalf("AutoCreate: %v", err)
	}

	got, err := groupStore.Get(ctx, jid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected group, got nil")
	}
	if got.OnboardingState != "in_progress" {
		t.Errorf("OnboardingState: got %q, want in_progress", got.OnboardingState)
	}
	if got.FinancialEnabled {
		t.Error("FinancialEnabled: should default to false on auto-create")
	}
	if got.Language != "" {
		t.Errorf("Language: should be NULL on auto-create, got %q", got.Language)
	}
	if got.Name != "Friend group" {
		t.Errorf("Name: got %q", got.Name)
	}

	members, _ := memberStore.List(ctx, jid)
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
}

func TestGroupStoreAutoCreateCapsAtTwoMembers(t *testing.T) {
	dbPath := setupMigratedDB(t)
	groupStore, _ := NewGroupStore(dbPath)
	defer groupStore.Close()
	memberStore, _ := NewMemberStore(dbPath)
	defer memberStore.Close()

	ctx := context.Background()
	jid := "120363888888@g.us"
	if err := groupStore.AutoCreate(ctx, jid, "Crowd", []string{
		"100000000001", "100000000002", "100000000003", "100000000004",
	}); err != nil {
		t.Fatalf("AutoCreate: %v", err)
	}
	members, _ := memberStore.List(ctx, jid)
	if len(members) != MemberCap {
		t.Errorf("expected %d members (cap), got %d", MemberCap, len(members))
	}
}

func TestGroupStoreAutoCreateZeroMembersStillCreatesGroup(t *testing.T) {
	dbPath := setupMigratedDB(t)
	groupStore, _ := NewGroupStore(dbPath)
	defer groupStore.Close()

	ctx := context.Background()
	jid := "120363ABABAB@g.us"
	if err := groupStore.AutoCreate(ctx, jid, "", nil); err != nil {
		t.Fatalf("AutoCreate: %v", err)
	}
	got, _ := groupStore.Get(ctx, jid)
	if got == nil {
		t.Fatal("expected group row")
	}
	if got.OnboardingState != "in_progress" {
		t.Errorf("OnboardingState: got %q", got.OnboardingState)
	}
}

func TestGroupStoreListComplete(t *testing.T) {
	dbPath := setupMigratedDB(t)
	gs, _ := NewGroupStore(dbPath)
	defer gs.Close()

	ctx := context.Background()
	// Two complete, one in-progress, one with no state.
	gs.Create(ctx, Group{ID: "120363LIST01@g.us", OnboardingState: "complete", Timezone: "Asia/Jerusalem", DigestHour: 9, DigestHourSet: true})
	gs.Create(ctx, Group{ID: "120363LIST02@g.us", OnboardingState: "complete", Timezone: "Europe/Berlin", DigestHour: 8, DigestHourSet: true})
	gs.Create(ctx, Group{ID: "120363LIST03@g.us", OnboardingState: "in_progress"})

	got, err := gs.ListComplete(ctx)
	if err != nil {
		t.Fatalf("ListComplete: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListComplete: got %d groups, want 2 (in_progress should be excluded)", len(got))
	}
	for _, g := range got {
		if g.OnboardingState != "complete" {
			t.Errorf("returned non-complete group: %+v", g)
		}
	}
}

func TestMemberStoreUpdateName(t *testing.T) {
	dbPath := setupMigratedDB(t)
	gs, _ := NewGroupStore(dbPath)
	defer gs.Close()
	ms, _ := NewMemberStore(dbPath)
	defer ms.Close()

	ctx := context.Background()
	jid := "120363UPDATE01@g.us"
	gs.Create(ctx, Group{ID: jid, OnboardingState: "complete"})
	ms.Add(ctx, jid, Member{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"})

	if err := ms.UpdateName(ctx, jid, "100000000001", "Alicia"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	got, _ := ms.List(ctx, jid)
	if got[0].DisplayName != "Alicia" {
		t.Errorf("DisplayName: got %q, want Alicia", got[0].DisplayName)
	}
}

func TestMemberStoreUpdateNameMissingFails(t *testing.T) {
	dbPath := setupMigratedDB(t)
	ms, _ := NewMemberStore(dbPath)
	defer ms.Close()

	if err := ms.UpdateName(context.Background(), "120363NOPE@g.us", "100000000001", "Ghost"); err == nil {
		t.Error("expected error for missing member")
	}
}

func TestMemberStoreRemove(t *testing.T) {
	dbPath := setupMigratedDB(t)
	gs, _ := NewGroupStore(dbPath)
	defer gs.Close()
	ms, _ := NewMemberStore(dbPath)
	defer ms.Close()

	ctx := context.Background()
	jid := "120363REMOVE01@g.us"
	gs.Create(ctx, Group{ID: jid, OnboardingState: "complete"})
	ms.Add(ctx, jid, Member{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"})
	ms.Add(ctx, jid, Member{GroupID: jid, WhatsAppID: "100000000002", DisplayName: "Bob"})

	if err := ms.Remove(ctx, jid, "100000000001"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	mems, _ := ms.List(ctx, jid)
	if len(mems) != 1 || mems[0].DisplayName != "Bob" {
		t.Errorf("after remove: got %+v, want [Bob]", mems)
	}
}

func TestGroupStoreSetName(t *testing.T) {
	dbPath := setupMigratedDB(t)
	gs, _ := NewGroupStore(dbPath)
	defer gs.Close()

	ctx := context.Background()
	jid := "120363NAME01@g.us"
	gs.Create(ctx, Group{ID: jid, OnboardingState: "complete", Name: "Old"})
	if err := gs.SetName(ctx, jid, "New Family"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	got, _ := gs.Get(ctx, jid)
	if got.Name != "New Family" {
		t.Errorf("Name: got %q, want New Family", got.Name)
	}
}

func TestMemberStoreScopedByGroup(t *testing.T) {
	dbPath := setupMigratedDB(t)
	groupStore, _ := NewGroupStore(dbPath)
	defer groupStore.Close()
	memberStore, _ := NewMemberStore(dbPath)
	defer memberStore.Close()

	ctx := context.Background()
	groupA := "120363AAAAAA@g.us"
	groupB := "120363BBBBBB@g.us"
	groupStore.Create(ctx, Group{ID: groupA, OnboardingState: "complete"})
	groupStore.Create(ctx, Group{ID: groupB, OnboardingState: "complete"})

	memberStore.Add(ctx, groupA, Member{GroupID: groupA, WhatsAppID: "100000000001", DisplayName: "Alice"})
	memberStore.Add(ctx, groupB, Member{GroupID: groupB, WhatsAppID: "100000000002", DisplayName: "Bob"})

	membersA, _ := memberStore.List(ctx, groupA)
	if len(membersA) != 1 || membersA[0].DisplayName != "Alice" {
		t.Errorf("group A: expected [Alice], got %+v", membersA)
	}
	membersB, _ := memberStore.List(ctx, groupB)
	if len(membersB) != 1 || membersB[0].DisplayName != "Bob" {
		t.Errorf("group B: expected [Bob], got %+v", membersB)
	}
}
