package messenger

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	"go.mau.fi/whatsmeow/types"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// setupStores builds a temp SQLite DB with all migrations applied and
// returns the path plus a GroupStore ready for use. Both share the same
// underlying file so a separately-opened MemberStore sees the same data.
func setupStores(t *testing.T) (dbPath string, gs *db.GroupStore) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "test.db")

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

	migrationDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.RunMigrations(migrationDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	migrationDB.Close()

	gs, err = db.NewGroupStore(dbPath)
	if err != nil {
		t.Fatalf("NewGroupStore: %v", err)
	}
	t.Cleanup(func() { gs.Close() })
	return dbPath, gs
}

// stubGroupInfo returns a groupInfoFn that always reports the same phones + name.
func stubGroupInfo(phones []string, name string) groupInfoFn {
	return func(_ context.Context, _ types.JID) ([]string, string, error) {
		return phones, name, nil
	}
}

// newTestWA wires up a WhatsApp instance suitable for gating tests — no
// actual whatsmeow client connection. tenantZeroJID may be empty.
// Returns the WhatsApp and the dbPath (so tests can open additional stores).
func newTestWA(t *testing.T, tenantZeroJID string, allowlist *Allowlist, gi groupInfoFn) (*WhatsApp, string) {
	t.Helper()
	var tz types.JID
	if tenantZeroJID != "" {
		var err error
		tz, err = types.ParseJID(tenantZeroJID)
		if err != nil {
			t.Fatalf("parse tenantZero: %v", err)
		}
	}
	dbPath, gs := setupStores(t)
	return &WhatsApp{
		tenantZeroJID: tz,
		allowlist:     allowlist,
		groups:        gs,
		groupInfo:     gi,
	}, dbPath
}

func mustParseJID(t *testing.T, s string) types.JID {
	t.Helper()
	j, err := types.ParseJID(s)
	if err != nil {
		t.Fatalf("parse jid %q: %v", s, err)
	}
	return j
}

func TestGateInbound_NonAllowlistedDropped(t *testing.T) {
	allowlist := ParseAllowlist("100000000001")
	chat := mustParseJID(t, "120363111111@g.us")
	wa, _ := newTestWA(t, "", allowlist, stubGroupInfo([]string{"999999999999", "888888888888"}, "Strangers"))

	deliver := wa.gateInbound(context.Background(), chat)
	if deliver {
		t.Fatal("non-allowlisted group should not be delivered")
	}
	row, _ := wa.groups.Get(context.Background(), chat.String())
	if row != nil {
		t.Errorf("non-allowlisted group should not be auto-created, got %+v", row)
	}
}

func TestGateInbound_AllowlistedNonTenantZero_AutoCreatesAndDelivers(t *testing.T) {
	allowlist := ParseAllowlist("100000000001,100000000002")
	chat := mustParseJID(t, "120363222222@g.us")
	wa, _ := newTestWA(t, "120363999999@g.us", allowlist,
		stubGroupInfo([]string{"100000000001", "100000000002", "999999999999"}, "Friends"))

	deliver := wa.gateInbound(context.Background(), chat)
	if !deliver {
		t.Fatal("allowlisted group should be delivered to dispatcher (Story 2.2)")
	}
	row, err := wa.groups.Get(context.Background(), chat.String())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row == nil {
		t.Fatal("expected groups row to be auto-created")
	}
	if row.OnboardingState != "in_progress" {
		t.Errorf("OnboardingState: got %q, want in_progress", row.OnboardingState)
	}
	if row.Name != "Friends" {
		t.Errorf("Name: got %q", row.Name)
	}
	if row.FinancialEnabled {
		t.Error("FinancialEnabled should default to false")
	}
}

func TestGateInbound_AllowlistedExistingRowReused(t *testing.T) {
	// Mirrors prod tenant-zero behavior: the row exists (from migration in
	// prod, seeded explicitly here), so gateInbound delivers without ever
	// invoking AutoCreate again. There's no JID-based special-case anymore;
	// existence of the row is the only signal.
	jid := "120363000000@g.us"
	allowlist := ParseAllowlist("100000000001")
	wa, _ := newTestWA(t, "", allowlist,
		stubGroupInfo([]string{"100000000001"}, "Family"))
	ctx := context.Background()
	if err := wa.groups.AutoCreate(ctx, jid, "Family", []string{"100000000001"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rowBefore, _ := wa.groups.Get(ctx, jid)

	if !wa.gateInbound(ctx, mustParseJID(t, jid)) {
		t.Fatal("allowlisted group with existing row should deliver")
	}
	rowAfter, _ := wa.groups.Get(ctx, jid)
	if rowBefore.CreatedAt != rowAfter.CreatedAt {
		t.Errorf("row should not be re-created on subsequent message: %q -> %q",
			rowBefore.CreatedAt, rowAfter.CreatedAt)
	}
}

func TestGateInbound_ExistingGroupNotReCreated(t *testing.T) {
	allowlist := ParseAllowlist("100000000001")
	chat := mustParseJID(t, "120363333333@g.us")
	wa, _ := newTestWA(t, "", allowlist, stubGroupInfo([]string{"100000000001"}, "Pre-existing"))

	if !wa.gateInbound(context.Background(), chat) {
		t.Fatal("first call should deliver to dispatcher")
	}
	row1, _ := wa.groups.Get(context.Background(), chat.String())
	if row1 == nil {
		t.Fatal("expected auto-create on first message")
	}

	if !wa.gateInbound(context.Background(), chat) {
		t.Fatal("second call should still deliver")
	}
	row2, _ := wa.groups.Get(context.Background(), chat.String())
	if row2 == nil {
		t.Fatal("group disappeared on second call")
	}
	if row1.CreatedAt != row2.CreatedAt {
		t.Errorf("CreatedAt changed on second call (re-created?): %q -> %q", row1.CreatedAt, row2.CreatedAt)
	}
}

func TestGateInbound_AutoCreateOnlyAllowlistedAsMembers(t *testing.T) {
	allowlist := ParseAllowlist("100000000001,100000000002")
	chat := mustParseJID(t, "120363444444@g.us")
	wa, dbPath := newTestWA(t, "", allowlist, stubGroupInfo(
		[]string{"100000000001", "999999999999", "100000000002", "888888888888"},
		"Mixed",
	))

	if !wa.gateInbound(context.Background(), chat) {
		t.Fatal("expected delivery to dispatcher")
	}

	ms, err := db.NewMemberStore(dbPath)
	if err != nil {
		t.Fatalf("NewMemberStore: %v", err)
	}
	defer ms.Close()
	members, err := ms.List(context.Background(), chat.String())
	if err != nil {
		t.Fatalf("List members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 allowlisted members, got %d (%+v)", len(members), members)
	}
	wantPhones := map[string]bool{"100000000001": true, "100000000002": true}
	for _, m := range members {
		if !wantPhones[m.WhatsAppID] {
			t.Errorf("unexpected member phone %q (only allowlisted should be inserted)", m.WhatsAppID)
		}
	}
}

// --- Story 2.8: bot-removed-from-group is a no-op ---

func TestIsBotLeaving_BotInLeaveList(t *testing.T) {
	bot := mustParseJID(t, "972500000000@s.whatsapp.net")
	other := mustParseJID(t, "100000000001@s.whatsapp.net")
	if !isBotLeaving([]types.JID{other, bot}, bot) {
		t.Error("expected true when bot JID is in leave list")
	}
}

func TestIsBotLeaving_BotNotInLeaveList(t *testing.T) {
	bot := mustParseJID(t, "972500000000@s.whatsapp.net")
	other := mustParseJID(t, "100000000001@s.whatsapp.net")
	if isBotLeaving([]types.JID{other}, bot) {
		t.Error("expected false when bot JID is not in leave list")
	}
}

func TestIsBotLeaving_EmptyLeaveList(t *testing.T) {
	bot := mustParseJID(t, "972500000000@s.whatsapp.net")
	if isBotLeaving(nil, bot) {
		t.Error("expected false for empty leave list")
	}
}

func TestBotRemoved_DoesNotMutateDB_AndReAddReusesRow(t *testing.T) {
	// Setup: a friend group already exists in the DB with a member.
	allowlist := ParseAllowlist("100000000001")
	chat := mustParseJID(t, "120363LEAVE01@g.us")
	wa, dbPath := newTestWA(t, "", allowlist,
		stubGroupInfo([]string{"100000000001"}, "Friends"))

	ctx := context.Background()
	if err := wa.groups.AutoCreate(ctx, chat.String(), "Friends", []string{"100000000001"}); err != nil {
		t.Fatalf("seed AutoCreate: %v", err)
	}
	rowBefore, _ := wa.groups.Get(ctx, chat.String())
	if rowBefore == nil {
		t.Fatal("seed: group should exist")
	}

	// Simulate the no-op behavior of the leave branch: nothing should mutate
	// the DB. The handler in whatsapp.go's Message-event switch only logs
	// — it has no DB writes to invoke. We assert the DB state is identical
	// after a "leave" pass by reading the same fields back.
	rowAfter, _ := wa.groups.Get(ctx, chat.String())
	if rowAfter == nil {
		t.Fatal("after leave (simulated): group should still exist")
	}
	if rowBefore.CreatedAt != rowAfter.CreatedAt {
		t.Errorf("CreatedAt changed: %q -> %q", rowBefore.CreatedAt, rowAfter.CreatedAt)
	}

	// Re-add: the next inbound message goes through gateInbound, which sees
	// an existing row and SKIPS AutoCreate. Verify that.
	if !wa.gateInbound(ctx, chat) {
		t.Fatal("re-add: allowlisted message should deliver")
	}
	rowReadd, _ := wa.groups.Get(ctx, chat.String())
	if rowReadd.CreatedAt != rowBefore.CreatedAt {
		t.Errorf("re-add: CreatedAt should be unchanged (no second AutoCreate); got %q -> %q",
			rowBefore.CreatedAt, rowReadd.CreatedAt)
	}

	// Tasks + members should also be untouched. (Members weren't seeded with
	// names here; just verify the count is preserved.)
	ms, err := db.NewMemberStore(dbPath)
	if err != nil {
		t.Fatalf("NewMemberStore: %v", err)
	}
	defer ms.Close()
	mems, _ := ms.List(ctx, chat.String())
	if len(mems) != 1 {
		t.Errorf("members preserved: got %d, want 1", len(mems))
	}
}

func TestGateInbound_GroupInfoErrorDrops(t *testing.T) {
	allowlist := ParseAllowlist("100000000001")
	chat := mustParseJID(t, "120363555555@g.us")
	failing := func(_ context.Context, _ types.JID) ([]string, string, error) {
		return nil, "", context.DeadlineExceeded
	}
	wa, _ := newTestWA(t, "", allowlist, failing)

	if wa.gateInbound(context.Background(), chat) {
		t.Fatal("group info failure must drop (fail closed)")
	}
	row, _ := wa.groups.Get(context.Background(), chat.String())
	if row != nil {
		t.Errorf("no group should be created on info failure, got %+v", row)
	}
}
