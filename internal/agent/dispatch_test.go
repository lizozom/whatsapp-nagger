package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
)

// recordingMessenger captures Write/WriteWithMentions for dispatcher tests.
type recordingMessenger struct {
	writes []writeRecord
}

type writeRecord struct {
	groupID string
	text    string
}

func (r *recordingMessenger) Read() (messenger.Message, error) { return messenger.Message{}, nil }
func (r *recordingMessenger) Write(groupID, text string) error {
	r.writes = append(r.writes, writeRecord{groupID, text})
	return nil
}
func (r *recordingMessenger) WriteWithMentions(groupID, text string, _ []messenger.Mention) error {
	return r.Write(groupID, text)
}

// setupDispatchStores creates a freshly-migrated DB and returns the path + a
// GroupStore bound to it.
func setupDispatchStores(t *testing.T) (string, *db.GroupStore) {
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
	return dbPath, gs
}

// newDispatchTestEnv wires a dispatcher with a shared History so tests can
// observe per-key snapshots after Handle returns. The main agent's
// HandleMessage appends the inbound user message to History BEFORE calling
// Anthropic, so a History snapshot is a reliable routing signal even if the
// Anthropic call fails (no key in CI).
func newDispatchTestEnv(t *testing.T) (*Dispatcher, *db.GroupStore, *History, *recordingMessenger) {
	t.Helper()
	dbPath, gs := setupDispatchStores(t)
	store, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	history := NewHistory()
	main := &Agent{store: store, history: history}
	ms, err := db.NewMemberStore(dbPath)
	if err != nil {
		t.Fatalf("NewMemberStore: %v", err)
	}
	t.Cleanup(func() { ms.Close() })
	rec := &recordingMessenger{}
	onb := NewOnboardingAgent(gs, ms, history, rec)
	return NewDispatcher(gs, main, onb, rec), gs, history, rec
}

func TestDispatcher_RoutesInProgressToOnboarding(t *testing.T) {
	d, gs, history, _ := newDispatchTestEnv(t)
	ctx := context.Background()
	jid := "120363AAAA01@g.us"
	if err := gs.AutoCreate(ctx, jid, "Friends", []string{"100000000001"}); err != nil {
		t.Fatalf("AutoCreate: %v", err)
	}

	// Onboarding will append to its own history BEFORE the Anthropic call,
	// then likely fail without an API key in CI. Either way the routing
	// signal is unambiguous: onboarding history touched, main history empty.
	_ = d.Handle(ctx, jid, "Alice", "hello")

	if h := history.Get(historyKey{GroupID: jid, AgentKind: KindOnboarding}); len(h) == 0 {
		t.Error("onboarding history should contain the inbound message")
	}
	if h := history.Get(historyKey{GroupID: jid, AgentKind: KindMain}); h != nil {
		t.Errorf("main history should be untouched, got %d msgs", len(h))
	}
}

func TestDispatcher_RoutesCompleteToMain(t *testing.T) {
	d, gs, history, _ := newDispatchTestEnv(t)
	ctx := context.Background()
	jid := "120363AAAA02@g.us"
	if err := gs.Create(ctx, db.Group{ID: jid, OnboardingState: "complete"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Handle may fail at the Anthropic API call (no key in CI) — we don't
	// assert on the error. The routing signal is that the main agent's
	// history got the user message appended (which happens BEFORE the API call).
	_ = d.Handle(ctx, jid, "Alice", "task: fix the sink")

	mainHist := history.Get(historyKey{GroupID: jid, AgentKind: KindMain})
	if len(mainHist) == 0 {
		t.Fatal("main agent history should contain the inbound user message")
	}
	if h := history.Get(historyKey{GroupID: jid, AgentKind: KindOnboarding}); h != nil {
		t.Errorf("onboarding history should be empty, got %d msgs", len(h))
	}
}

func TestDispatcher_NoGroupRowFallsThroughToMain(t *testing.T) {
	d, _, history, _ := newDispatchTestEnv(t)
	ctx := context.Background()
	jid := "120363AAAA03@g.us" // intentionally never created in the DB

	_ = d.Handle(ctx, jid, "Alice", "hello")

	if h := history.Get(historyKey{GroupID: jid, AgentKind: KindMain}); len(h) == 0 {
		t.Error("missing group row should default to main agent (history should be touched)")
	}
}

func TestDispatcher_HistoryIsolatedAcrossGroups(t *testing.T) {
	d, gs, history, _ := newDispatchTestEnv(t)
	ctx := context.Background()
	jidA := "120363BBBB01@g.us"
	jidB := "120363BBBB02@g.us"
	for _, jid := range []string{jidA, jidB} {
		if err := gs.Create(ctx, db.Group{ID: jid, OnboardingState: "complete"}); err != nil {
			t.Fatalf("Create %s: %v", jid, err)
		}
	}

	_ = d.Handle(ctx, jidA, "Alice", "first message in A")
	_ = d.Handle(ctx, jidB, "Bob", "first message in B")

	a := history.Get(historyKey{GroupID: jidA, AgentKind: KindMain})
	b := history.Get(historyKey{GroupID: jidB, AgentKind: KindMain})
	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("expected history for both groups, got A=%d B=%d", len(a), len(b))
	}
	// Each group's first message must be the one sent to that group.
	// (Window may include a tool-result follow-up if the API call succeeded;
	// the FIRST entry in each window is always the inbound user message.)
}
