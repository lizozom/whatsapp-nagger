package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// newOnboardingTestAgent builds an OnboardingAgent against a freshly-migrated
// temp DB. The Anthropic client is constructed but never invoked — these tests
// drive the tool handlers directly via executeTool.
func newOnboardingTestAgent(t *testing.T) (*OnboardingAgent, *db.GroupStore, *db.MemberStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	taskStore, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	taskStore.Close()
	txStore, err := db.NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	txStore.Close()

	migDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.RunMigrations(migDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	migDB.Close()

	gs, err := db.NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	t.Cleanup(func() { gs.Close() })
	ms, err := db.NewMemberStore(dbPath)
	if err != nil {
		t.Fatalf("NewMemberStore: %v", err)
	}
	t.Cleanup(func() { ms.Close() })

	rec := &recordingMessenger{}
	o := NewOnboardingAgent(gs, ms, NewHistory(), rec)
	return o, gs, ms
}

func seedFreshGroup(t *testing.T, gs *db.GroupStore, jid string) {
	t.Helper()
	if err := gs.AutoCreate(context.Background(), jid, "Test Group", nil); err != nil {
		t.Fatalf("AutoCreate: %v", err)
	}
}

// --- set_language ---

func TestOnboarding_SetLanguageEN(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB001@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_language", []byte(`{"language":"en"}`))
	if err != nil {
		t.Fatalf("set_language: %v", err)
	}
	g, _ := gs.Get(context.Background(), jid)
	if g.Language != "en" {
		t.Errorf("Language: got %q, want en", g.Language)
	}
}

func TestOnboarding_SetLanguageHE(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB002@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_language", []byte(`{"language":"he"}`))
	if err != nil {
		t.Fatalf("set_language: %v", err)
	}
	g, _ := gs.Get(context.Background(), jid)
	if g.Language != "he" {
		t.Errorf("Language: got %q, want he", g.Language)
	}
}

func TestOnboarding_SetLanguageRefusedOnceLocked(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB003@g.us"
	seedFreshGroup(t, gs, jid)
	_ = gs.SetLanguage(context.Background(), jid, "en")

	_, _, err := o.executeTool(context.Background(), jid, "set_language", []byte(`{"language":"he"}`))
	if err == nil {
		t.Fatal("expected refusal — language already locked")
	}
	if !strings.Contains(err.Error(), "already set") {
		t.Errorf("error should mention locked state: %v", err)
	}
}

func TestOnboarding_SetLanguageRejectsInvalid(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB004@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_language", []byte(`{"language":"fr"}`))
	if err == nil {
		t.Fatal("expected refusal — fr is not he or en")
	}
}

// --- set_member ---

func TestOnboarding_SetMemberFirst(t *testing.T) {
	o, gs, ms := newOnboardingTestAgent(t)
	jid := "120363ONB010@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_member",
		[]byte(`{"name":"Alice","whatsapp_id":"100000000001"}`))
	if err != nil {
		t.Fatalf("set_member: %v", err)
	}
	mems, _ := ms.List(context.Background(), jid)
	if len(mems) != 1 || mems[0].DisplayName != "Alice" {
		t.Errorf("members: got %+v, want [Alice]", mems)
	}
}

func TestOnboarding_SetMemberRefusesOverCap(t *testing.T) {
	o, gs, ms := newOnboardingTestAgent(t)
	jid := "120363ONB011@g.us"
	seedFreshGroup(t, gs, jid)
	ctx := context.Background()
	_ = ms.Add(ctx, jid, db.Member{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"})
	_ = ms.Add(ctx, jid, db.Member{GroupID: jid, WhatsAppID: "100000000002", DisplayName: "Bob"})

	_, _, err := o.executeTool(ctx, jid, "set_member",
		[]byte(`{"name":"Carol","whatsapp_id":"100000000003"}`))
	if err == nil {
		t.Fatal("expected refusal — over member cap")
	}
	if !strings.Contains(err.Error(), "v1 supports up to") {
		t.Errorf("error should explain cap: %v", err)
	}
}

func TestOnboarding_SetMemberUpdatesExisting(t *testing.T) {
	o, gs, ms := newOnboardingTestAgent(t)
	jid := "120363ONB012@g.us"
	seedFreshGroup(t, gs, jid)
	ctx := context.Background()
	_ = ms.Add(ctx, jid, db.Member{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"})
	// Re-call with same whatsapp_id but different display name — must not be
	// blocked by the cap and must update the row.
	_ = ms.Add(ctx, jid, db.Member{GroupID: jid, WhatsAppID: "100000000002", DisplayName: "Bob"})

	_, _, err := o.executeTool(ctx, jid, "set_member",
		[]byte(`{"name":"Alicia","whatsapp_id":"100000000001"}`))
	if err != nil {
		t.Fatalf("set_member upsert: %v", err)
	}
	mems, _ := ms.List(ctx, jid)
	var alicia bool
	for _, m := range mems {
		if m.WhatsAppID == "100000000001" && m.DisplayName == "Alicia" {
			alicia = true
		}
	}
	if !alicia {
		t.Errorf("expected Alice → Alicia rename, got %+v", mems)
	}
}

func TestOnboarding_SetMemberRejectsBadPhone(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB013@g.us"
	seedFreshGroup(t, gs, jid)

	cases := []string{
		`{"name":"Alice","whatsapp_id":"+972501234567"}`, // leading +
		`{"name":"Alice","whatsapp_id":"972 50 1234567"}`, // spaces
		`{"name":"Alice","whatsapp_id":"abc"}`,            // letters
		`{"name":"Alice","whatsapp_id":"123"}`,            // too short
	}
	for _, in := range cases {
		_, _, err := o.executeTool(context.Background(), jid, "set_member", []byte(in))
		if err == nil {
			t.Errorf("expected refusal for %q", in)
		}
	}
}

func TestOnboarding_SetMemberRejectsEmptyName(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB014@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_member",
		[]byte(`{"name":"  ","whatsapp_id":"100000000001"}`))
	if err == nil {
		t.Fatal("expected refusal for empty name")
	}
}

// --- set_timezone ---

func TestOnboarding_SetTimezoneValid(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB020@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_timezone", []byte(`{"timezone":"Asia/Jerusalem"}`))
	if err != nil {
		t.Fatalf("set_timezone: %v", err)
	}
	g, _ := gs.Get(context.Background(), jid)
	if g.Timezone != "Asia/Jerusalem" {
		t.Errorf("Timezone: got %q", g.Timezone)
	}
}

func TestOnboarding_SetTimezoneRejectsInvalid(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB021@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_timezone", []byte(`{"timezone":"Asia/Atlantis"}`))
	if err == nil {
		t.Fatal("expected refusal for nonsense timezone")
	}
}

// --- set_digest_hour ---

func TestOnboarding_SetDigestHourValid(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB030@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_digest_hour", []byte(`{"hour":9}`))
	if err != nil {
		t.Fatalf("set_digest_hour: %v", err)
	}
	g, _ := gs.Get(context.Background(), jid)
	if g.DigestHour != 9 || !g.DigestHourSet {
		t.Errorf("DigestHour: got %d (set=%v), want 9 (set=true)", g.DigestHour, g.DigestHourSet)
	}
}

func TestOnboarding_SetDigestHourZeroIsValid(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB031@g.us"
	seedFreshGroup(t, gs, jid)

	_, _, err := o.executeTool(context.Background(), jid, "set_digest_hour", []byte(`{"hour":0}`))
	if err != nil {
		t.Fatalf("set_digest_hour 0: %v", err)
	}
	g, _ := gs.Get(context.Background(), jid)
	if !g.DigestHourSet {
		t.Errorf("DigestHourSet should be true after explicit 0, got false")
	}
}

func TestOnboarding_SetDigestHourRejectsOutOfRange(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB032@g.us"
	seedFreshGroup(t, gs, jid)

	for _, h := range []int{-1, 24, 99} {
		input := []byte(`{"hour":` + itoa(h) + `}`)
		_, _, err := o.executeTool(context.Background(), jid, "set_digest_hour", input)
		if err == nil {
			t.Errorf("expected refusal for hour=%d", h)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// --- complete_onboarding ---

func TestOnboarding_CompleteRefusesMissingFields(t *testing.T) {
	o, gs, _ := newOnboardingTestAgent(t)
	jid := "120363ONB040@g.us"
	seedFreshGroup(t, gs, jid)

	_, completed, err := o.executeTool(context.Background(), jid, "complete_onboarding", []byte(`{}`))
	if err == nil {
		t.Fatal("expected refusal — nothing is set yet")
	}
	if completed {
		t.Error("completed flag should be false on refusal")
	}
	for _, must := range []string{"language", "timezone", "digest_hour", "member"} {
		if !strings.Contains(err.Error(), must) {
			t.Errorf("error should list missing %q, got: %v", must, err)
		}
	}
	g, _ := gs.Get(context.Background(), jid)
	if g.OnboardingState == "complete" {
		t.Error("OnboardingState should NOT be complete when fields missing")
	}
}

func TestOnboarding_CompleteSucceedsAndDiscardsHistory(t *testing.T) {
	o, gs, ms := newOnboardingTestAgent(t)
	jid := "120363ONB041@g.us"
	seedFreshGroup(t, gs, jid)
	ctx := context.Background()
	_ = gs.SetLanguage(ctx, jid, "en")
	_ = gs.SetTimezone(ctx, jid, "Asia/Jerusalem")
	_ = gs.SetDigestHour(ctx, jid, 9)
	_ = ms.Add(ctx, jid, db.Member{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"})
	// Seed some onboarding history to verify discard.
	key := historyKey{GroupID: jid, AgentKind: KindOnboarding}
	o.history.Append(key, userMsg("[Alice]: hi"))
	if h := o.history.Get(key); len(h) == 0 {
		t.Fatal("seed: expected onboarding history")
	}

	result, completed, err := o.executeTool(ctx, jid, "complete_onboarding", []byte(`{}`))
	if err != nil {
		t.Fatalf("complete_onboarding: %v", err)
	}
	if !completed {
		t.Error("completed flag should be true on success")
	}
	if !strings.Contains(result, "complete") {
		t.Errorf("result should indicate completion, got %q", result)
	}
	g, _ := gs.Get(ctx, jid)
	if g.OnboardingState != "complete" {
		t.Errorf("OnboardingState: got %q, want complete", g.OnboardingState)
	}
	if h := o.history.Get(key); h != nil {
		t.Errorf("onboarding history should be discarded, got %d msgs", len(h))
	}
}

// --- happy path integration ---

func TestOnboarding_HappyPathFullFlow(t *testing.T) {
	o, gs, ms := newOnboardingTestAgent(t)
	jid := "120363ONB050@g.us"
	seedFreshGroup(t, gs, jid)
	ctx := context.Background()

	steps := []struct {
		name  string
		tool  string
		input string
	}{
		{"language", "set_language", `{"language":"en"}`},
		{"alice", "set_member", `{"name":"Alice","whatsapp_id":"100000000001"}`},
		{"bob", "set_member", `{"name":"Bob","whatsapp_id":"100000000002"}`},
		{"timezone", "set_timezone", `{"timezone":"Asia/Jerusalem"}`},
		{"digest", "set_digest_hour", `{"hour":9}`},
		{"complete", "complete_onboarding", `{}`},
	}
	for _, s := range steps {
		_, completed, err := o.executeTool(ctx, jid, s.tool, []byte(s.input))
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if s.name == "complete" && !completed {
			t.Error("complete step should set completed=true")
		}
	}

	g, _ := gs.Get(ctx, jid)
	if g.Language != "en" || g.Timezone != "Asia/Jerusalem" || g.DigestHour != 9 || !g.DigestHourSet {
		t.Errorf("final group state: %+v", g)
	}
	if g.OnboardingState != "complete" {
		t.Errorf("OnboardingState: got %q, want complete", g.OnboardingState)
	}
	mems, _ := ms.List(ctx, jid)
	if len(mems) != 2 {
		t.Errorf("members: got %d, want 2 (%+v)", len(mems), mems)
	}
}

// --- system prompt resumption ---

func TestOnboardingPrompt_NextMissingFieldOrdering(t *testing.T) {
	jid := "120363ONB060@g.us"
	cases := []struct {
		name   string
		group  *db.Group
		mems   []db.Member
		expect string // substring expected in the "Next step" line
	}{
		{
			name:   "language first when nothing set",
			group:  &db.Group{ID: jid},
			expect: "Hebrew",
		},
		{
			name:   "members after language set",
			group:  &db.Group{ID: jid, Language: "en"},
			expect: "first member",
		},
		{
			name:   "timezone after one member",
			group:  &db.Group{ID: jid, Language: "en"},
			mems:   []db.Member{{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"}},
			expect: "timezone",
		},
		{
			name:   "digest hour after timezone",
			group:  &db.Group{ID: jid, Language: "en", Timezone: "Asia/Jerusalem"},
			mems:   []db.Member{{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"}},
			expect: "digest",
		},
		{
			name:   "all set leads to complete prompt",
			group:  &db.Group{ID: jid, Language: "en", Timezone: "Asia/Jerusalem", DigestHour: 9, DigestHourSet: true},
			mems:   []db.Member{{GroupID: jid, WhatsAppID: "100000000001", DisplayName: "Alice"}},
			expect: "complete_onboarding",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := buildOnboardingSystemPrompt(tc.group, tc.mems)
			if !strings.Contains(prompt, tc.expect) {
				t.Errorf("prompt missing %q\nfull prompt:\n%s", tc.expect, prompt)
			}
		})
	}
}

func TestOnboardingPrompt_BilingualBeforeLanguageLocked(t *testing.T) {
	prompt := buildOnboardingSystemPrompt(&db.Group{}, nil)
	if !strings.Contains(prompt, "BILINGUALLY") {
		t.Errorf("pre-language prompt should request bilingual replies")
	}
}

func TestOnboardingPrompt_AsksForNamesWhenMembersPreSeeded(t *testing.T) {
	jid := "120363ONB070@g.us"
	// Simulate AutoCreate: phones present, display_name empty.
	mems := []db.Member{
		{GroupID: jid, WhatsAppID: "100000000001", DisplayName: ""},
		{GroupID: jid, WhatsAppID: "100000000002", DisplayName: ""},
	}
	prompt := buildOnboardingSystemPrompt(&db.Group{ID: jid, Language: "en"}, mems)
	if !strings.Contains(prompt, "(unnamed)") {
		t.Error("prompt should mark unnamed members visually")
	}
	if !strings.Contains(prompt, "what to call") {
		t.Errorf("prompt should ask what to call unnamed members; got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Do NOT ask for the phone again") {
		t.Errorf("prompt should explicitly tell LLM not to re-ask for phones; got:\n%s", prompt)
	}
}

func TestOnboardingPrompt_AsksForBothWhenNoMembers(t *testing.T) {
	prompt := buildOnboardingSystemPrompt(&db.Group{Language: "en"}, nil)
	if !strings.Contains(prompt, "ask for both display name and WhatsApp phone") {
		t.Errorf("dev/terminal mode prompt should ask for both name and phone; got:\n%s", prompt)
	}
}

func TestOnboardingPrompt_LocksToHebrewAfterSet(t *testing.T) {
	prompt := buildOnboardingSystemPrompt(&db.Group{Language: "he"}, nil)
	if !strings.Contains(prompt, "ONLY in Hebrew") {
		t.Errorf("post-set prompt should lock to Hebrew")
	}
	if strings.Contains(prompt, "BILINGUALLY") {
		t.Errorf("post-set prompt should not request bilingual")
	}
}
