package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// migrate002Setup builds a fresh DB with base tables (via NewTaskStore + NewTxStore),
// optionally pre-populates tenant-zero rows, writes a fake personas.md, and sets
// the env vars the migration reads. Returns the DB path and the tenant-zero JID.
type migrate002Setup struct {
	dbPath string
	jid    string
}

func setupTenantZeroMigrationTest(t *testing.T, opts ...func(*migrate002SetupOpts)) migrate002Setup {
	t.Helper()
	o := &migrate002SetupOpts{
		jid:           "120363999999@g.us",
		timezone:      "Asia/Jerusalem",
		digestHour:    "9",
		language:      "he",
		name:          "tenant-zero-test",
		personasBody:  "## Alice\n- **Phone:** 972541234567\n\n## Bob\n- **Phone:** 972549876543\n",
		preTasks:      []seedTask{},
		preMetadata:   []seedMeta{},
		preTransactions: false,
	}
	for _, opt := range opts {
		opt(o)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	taskStore, err := NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	for _, st := range o.preTasks {
		if _, err := taskStore.AddTask(st.content, st.assignee, st.dueDate); err != nil {
			t.Fatalf("seed AddTask: %v", err)
		}
	}
	for _, m := range o.preMetadata {
		if err := taskStore.SetMeta(m.key, m.value); err != nil {
			t.Fatalf("seed SetMeta: %v", err)
		}
	}
	taskStore.Close()

	txStore, err := NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	if o.preTransactions {
		if _, _, err := txStore.UpsertBatch([]Transaction{{
			ID:          ComputeTxID("max", "1234", "2026-04-01", -42.50, "Test merchant", ""),
			Provider:    "max",
			CardLast4:   "1234",
			PostedAt:    "2026-04-01",
			AmountILS:   -42.50,
			Description: "Test merchant",
			Status:      "posted",
		}}); err != nil {
			t.Fatalf("seed UpsertBatch: %v", err)
		}
	}
	txStore.Close()

	if o.personasBody != "" {
		personasPath := filepath.Join(dir, "personas.md")
		if err := os.WriteFile(personasPath, []byte(o.personasBody), 0o600); err != nil {
			t.Fatalf("write personas: %v", err)
		}
		t.Setenv("PERSONAS_FILE", personasPath)
	}

	t.Setenv("WHATSAPP_GROUP_JID", o.jid)
	t.Setenv("TIMEZONE", o.timezone)
	t.Setenv("DIGEST_HOUR", o.digestHour)
	t.Setenv("TENANT_ZERO_LANGUAGE", o.language)
	t.Setenv("TENANT_ZERO_NAME", o.name)

	migrationDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer migrationDB.Close()
	if err := RunMigrations(migrationDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	return migrate002Setup{dbPath: dbPath, jid: o.jid}
}

type migrate002SetupOpts struct {
	jid             string
	timezone        string
	digestHour      string
	language        string
	name            string
	personasBody    string
	preTasks        []seedTask
	preMetadata     []seedMeta
	preTransactions bool
}

type seedTask struct {
	content, assignee, dueDate string
}
type seedMeta struct {
	key, value string
}

func withPreTasks(tasks ...seedTask) func(*migrate002SetupOpts) {
	return func(o *migrate002SetupOpts) { o.preTasks = tasks }
}
func withPreMetadata(metas ...seedMeta) func(*migrate002SetupOpts) {
	return func(o *migrate002SetupOpts) { o.preMetadata = metas }
}
func withPreTransactions() func(*migrate002SetupOpts) {
	return func(o *migrate002SetupOpts) { o.preTransactions = true }
}
func withNoPersonas() func(*migrate002SetupOpts) {
	return func(o *migrate002SetupOpts) { o.personasBody = "" }
}
func withJID(jid string) func(*migrate002SetupOpts) {
	return func(o *migrate002SetupOpts) { o.jid = jid }
}

func TestMigrate002CreatesTenantZeroGroup(t *testing.T) {
	s := setupTenantZeroMigrationTest(t)

	store, _ := NewGroupStore(s.dbPath)
	defer store.Close()
	g, err := store.Get(context.Background(), s.jid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g == nil {
		t.Fatal("expected tenant-zero group, got nil")
	}
	if g.Name != "tenant-zero-test" {
		t.Errorf("Name: got %q", g.Name)
	}
	if g.Language != "he" {
		t.Errorf("Language: got %q", g.Language)
	}
	if g.Timezone != "Asia/Jerusalem" {
		t.Errorf("Timezone: got %q", g.Timezone)
	}
	if g.DigestHour != 9 {
		t.Errorf("DigestHour: got %d", g.DigestHour)
	}
	if g.OnboardingState != "complete" {
		t.Errorf("OnboardingState: got %q", g.OnboardingState)
	}
	if !g.FinancialEnabled {
		t.Error("FinancialEnabled: expected true")
	}
	if g.CreatedAt == "" {
		t.Error("CreatedAt should be populated")
	}
	if g.LastActiveAt == "" {
		t.Error("LastActiveAt should be populated")
	}
}

func TestMigrate002BackfillsMembersFromPersonas(t *testing.T) {
	s := setupTenantZeroMigrationTest(t)

	memberStore, _ := NewMemberStore(s.dbPath)
	defer memberStore.Close()
	members, err := memberStore.List(context.Background(), s.jid)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members from personas, got %d", len(members))
	}

	// Check both names are present (order isn't guaranteed since map iteration).
	got := map[string]string{}
	for _, m := range members {
		got[m.DisplayName] = m.WhatsAppID
	}
	if got["Alice"] != "972541234567" {
		t.Errorf("Alice phone: got %q", got["Alice"])
	}
	if got["Bob"] != "972549876543" {
		t.Errorf("Bob phone: got %q", got["Bob"])
	}
}

func TestMigrate002BackfillsTasksWithGroupID(t *testing.T) {
	s := setupTenantZeroMigrationTest(t,
		withPreTasks(
			seedTask{content: "Fix the sink", assignee: "Bob"},
			seedTask{content: "Buy milk", assignee: "Alice"},
		),
	)

	d, _ := sql.Open("sqlite", s.dbPath)
	defer d.Close()

	var nullCount int
	d.QueryRow("SELECT COUNT(*) FROM tasks WHERE group_id IS NULL").Scan(&nullCount)
	if nullCount != 0 {
		t.Errorf("expected 0 tasks with NULL group_id, got %d", nullCount)
	}

	var wrongCount int
	d.QueryRow("SELECT COUNT(*) FROM tasks WHERE group_id != ?", s.jid).Scan(&wrongCount)
	if wrongCount != 0 {
		t.Errorf("expected 0 tasks with group_id != tenant-zero, got %d", wrongCount)
	}

	var totalCount int
	d.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&totalCount)
	if totalCount != 2 {
		t.Errorf("expected 2 tasks total, got %d", totalCount)
	}
}

func TestMigrate002BackfillsMetadataWithGroupID(t *testing.T) {
	s := setupTenantZeroMigrationTest(t,
		withPreMetadata(seedMeta{key: "last_digest_date", value: "2026-05-01"}),
	)

	d, _ := sql.Open("sqlite", s.dbPath)
	defer d.Close()

	var nullCount int
	d.QueryRow("SELECT COUNT(*) FROM metadata WHERE group_id IS NULL").Scan(&nullCount)
	if nullCount != 0 {
		t.Errorf("expected 0 metadata rows with NULL group_id, got %d", nullCount)
	}
}

func TestMigrate002BackfillsTransactionsWithGroupID(t *testing.T) {
	s := setupTenantZeroMigrationTest(t, withPreTransactions())

	d, _ := sql.Open("sqlite", s.dbPath)
	defer d.Close()

	var nullCount int
	d.QueryRow("SELECT COUNT(*) FROM transactions WHERE group_id IS NULL").Scan(&nullCount)
	if nullCount != 0 {
		t.Errorf("expected 0 transactions with NULL group_id, got %d", nullCount)
	}

	var wrongCount int
	d.QueryRow("SELECT COUNT(*) FROM transactions WHERE group_id != ?", s.jid).Scan(&wrongCount)
	if wrongCount != 0 {
		t.Errorf("expected 0 transactions with wrong group_id, got %d", wrongCount)
	}
}

func TestMigrate002IsIdempotent(t *testing.T) {
	s := setupTenantZeroMigrationTest(t,
		withPreTasks(seedTask{content: "Fix sink", assignee: "Bob"}),
	)

	// Re-run migrations on the same DB (the framework should skip migrate_002 since it's already recorded).
	migrationDB, _ := sql.Open("sqlite", s.dbPath)
	defer migrationDB.Close()
	if err := RunMigrations(migrationDB); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	var groupCount int
	migrationDB.QueryRow("SELECT COUNT(*) FROM groups WHERE id = ?", s.jid).Scan(&groupCount)
	if groupCount != 1 {
		t.Errorf("expected exactly 1 tenant-zero group after second run, got %d", groupCount)
	}

	var memberCount int
	migrationDB.QueryRow("SELECT COUNT(*) FROM members WHERE group_id = ?", s.jid).Scan(&memberCount)
	if memberCount != 2 {
		t.Errorf("expected 2 members after second run, got %d", memberCount)
	}
}

func TestMigrate002NoOpWhenJIDEmpty(t *testing.T) {
	s := setupTenantZeroMigrationTest(t, withJID(""))

	migrationDB, _ := sql.Open("sqlite", s.dbPath)
	defer migrationDB.Close()

	var schemaVersionCount int
	migrationDB.QueryRow("SELECT COUNT(*) FROM schema_version WHERE id = 2").Scan(&schemaVersionCount)
	if schemaVersionCount != 1 {
		t.Errorf("expected migrate_002 to be recorded as applied even when no-op, got %d rows", schemaVersionCount)
	}

	var groupCount int
	migrationDB.QueryRow("SELECT COUNT(*) FROM groups").Scan(&groupCount)
	if groupCount != 0 {
		t.Errorf("expected no groups when JID is empty, got %d", groupCount)
	}
}

func TestMigrate002NoMembersWhenPersonasMissing(t *testing.T) {
	s := setupTenantZeroMigrationTest(t, withNoPersonas())

	memberStore, _ := NewMemberStore(s.dbPath)
	defer memberStore.Close()
	members, err := memberStore.List(context.Background(), s.jid)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members when personas missing, got %d", len(members))
	}

	// Group should still exist.
	groupStore, _ := NewGroupStore(s.dbPath)
	defer groupStore.Close()
	g, _ := groupStore.Get(context.Background(), s.jid)
	if g == nil {
		t.Error("expected tenant-zero group to be created even without personas")
	}
}

func TestMigrate002VerifiesZeroNullPostcondition(t *testing.T) {
	// Happy path: with all base tables populated, post-migration NULL count is 0.
	s := setupTenantZeroMigrationTest(t,
		withPreTasks(seedTask{content: "task A", assignee: "Alice"}),
		withPreMetadata(seedMeta{key: "last_digest_date", value: "2026-05-01"}),
		withPreTransactions(),
	)

	d, _ := sql.Open("sqlite", s.dbPath)
	defer d.Close()

	for _, table := range []string{"tasks", "metadata", "transactions"} {
		var n int
		d.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE group_id IS NULL").Scan(&n)
		if n != 0 {
			t.Errorf("%s: expected 0 NULL group_id rows, got %d", table, n)
		}
	}
}

func TestParseDigestHourVariants(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"9", 9},
		{"09", 9},
		{"09:00", 9},
		{"23:30", 23},
		{"  21  ", 21},
		{"not-a-number", 0},
	}
	for _, c := range cases {
		got := parseDigestHour(c.in)
		if got != c.want {
			t.Errorf("parseDigestHour(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParsePersonaPhonesExtractsAllSections(t *testing.T) {
	body := `# Personas

## Alice
- **Phone:** 972541234567
- Some other note

## Bob
Description goes here.
- **Phone:** 972549876543

## Charlie
- No phone here.

## Dana
- **phone:** 972551112222
`
	got := parsePersonaPhones(body)
	if got["Alice"] != "972541234567" {
		t.Errorf("Alice: %q", got["Alice"])
	}
	if got["Bob"] != "972549876543" {
		t.Errorf("Bob: %q", got["Bob"])
	}
	if _, ok := got["Charlie"]; ok {
		t.Errorf("Charlie should not be in map (no phone)")
	}
	// Phone match is case-insensitive.
	if got["Dana"] != "972551112222" {
		t.Errorf("Dana: %q", got["Dana"])
	}
}
