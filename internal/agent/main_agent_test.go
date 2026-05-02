package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/version"
	_ "modernc.org/sqlite"
)

const testGroupID = "120363999999@g.us"

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	// migrate_001 ALTERs the transactions table, so it must exist first.
	txStore, err := db.NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	txStore.Close()

	migDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	t.Setenv("WHATSAPP_GROUP_JID", "")
	if err := db.RunMigrations(migDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	migDB.Close()

	t.Cleanup(func() { store.Close() })
	return &Agent{store: store, history: NewHistory()}
}

func TestExecuteToolAddTask(t *testing.T) {
	a := newTestAgent(t)

	result, err := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Bob","due_date":"2026-03-25"}`))
	if err != nil {
		t.Fatalf("ExecuteTool add_task: %v", err)
	}

	var task db.Task
	if err := json.Unmarshal([]byte(result), &task); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if task.Content != "Fix sink" {
		t.Errorf("expected content 'Fix sink', got %q", task.Content)
	}
	if task.Assignee != "Bob" {
		t.Errorf("expected assignee 'Bob', got %q", task.Assignee)
	}
	if task.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", task.Status)
	}
}

func TestExecuteToolListTasks(t *testing.T) {
	a := newTestAgent(t)

	a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Task 1","assignee":"Bob"}`))
	a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Task 2","assignee":"Alice"}`))

	result, err := a.ExecuteTool(testGroupID, "list_tasks", []byte(`{"assignee":"Bob"}`))
	if err != nil {
		t.Fatalf("ExecuteTool list_tasks: %v", err)
	}

	var tasks []db.Task
	if err := json.Unmarshal([]byte(result), &tasks); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task for Bob, got %d", len(tasks))
	}
}

func TestExecuteToolListTasksEmpty(t *testing.T) {
	a := newTestAgent(t)

	result, err := a.ExecuteTool(testGroupID, "list_tasks", []byte(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool list_tasks: %v", err)
	}
	if result != "null" && result != "[]" {
		// empty slice marshals to "null" in Go
		t.Logf("empty list result: %s", result)
	}
}

func TestParseCardOwners(t *testing.T) {
	got := parseCardOwners("Alice:max/1234,max/5678,cal/9999;Bob:max/0000")
	if len(got) != 2 {
		t.Fatalf("expected 2 owners, got %d: %+v", len(got), got)
	}
	if len(got["Alice"]) != 3 {
		t.Errorf("Alice: expected 3 cards, got %d", len(got["Alice"]))
	}
	if got["Bob"][0].Provider != "max" || got["Bob"][0].CardLast4 != "0000" {
		t.Errorf("Bob card wrong: %+v", got["Bob"])
	}
}

func TestParseCardOwnersMessyWhitespace(t *testing.T) {
	got := parseCardOwners("  Alice : MAX / 1234 , cal/9999 ;  Bob: max/0000")
	if len(got["Alice"]) != 2 {
		t.Errorf("Alice: expected 2 cards with whitespace tolerance, got %d", len(got["Alice"]))
	}
	if got["Alice"][0].Provider != "max" {
		t.Errorf("provider should be lowercased, got %q", got["Alice"][0].Provider)
	}
}

func TestParseCardOwnersEmpty(t *testing.T) {
	got := parseCardOwners("")
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}

// --- trimHistory tests ---

func userText(s string) anthropic.MessageParam {
	return anthropic.NewUserMessage(anthropic.NewTextBlock(s))
}

func assistantText(s string) anthropic.MessageParam {
	return anthropic.NewAssistantMessage(anthropic.NewTextBlock(s))
}

func assistantToolUse(id, name string) anthropic.MessageParam {
	return anthropic.NewAssistantMessage(
		anthropic.NewToolUseBlock(id, map[string]any{}, name),
	)
}

func userToolResult(id, result string) anthropic.MessageParam {
	return anthropic.NewUserMessage(anthropic.NewToolResultBlock(id, result, false))
}

func TestTrimHistoryUnderLimit(t *testing.T) {
	history := []anthropic.MessageParam{
		userText("hi"),
		assistantText("hello"),
	}
	trimmed := trimHistory(history, 20)
	if len(trimmed) != 2 {
		t.Errorf("under-limit history should pass through, got %d", len(trimmed))
	}
}

func TestTrimHistoryDropsOldestTurns(t *testing.T) {
	// 6 messages total, cap=4. Naive slice would be history[2:] = [user, asst, user, asst] — valid.
	history := []anthropic.MessageParam{
		userText("msg1"), assistantText("resp1"),
		userText("msg2"), assistantText("resp2"),
		userText("msg3"), assistantText("resp3"),
	}
	trimmed := trimHistory(history, 4)
	if len(trimmed) != 4 {
		t.Errorf("expected 4 messages, got %d", len(trimmed))
	}
	if trimmed[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first msg should be user, got %v", trimmed[0].Role)
	}
}

func TestTrimHistoryDropsLeadingAssistant(t *testing.T) {
	// Naive tail[2:] would be [assistant, user, assistant] — starts with assistant, invalid.
	// trimHistory must drop the leading assistant and return [user, assistant].
	history := []anthropic.MessageParam{
		userText("msg1"), assistantText("resp1"),
		userText("msg2"), assistantText("resp2"),
	}
	trimmed := trimHistory(history, 3)
	if len(trimmed) == 0 {
		t.Fatal("empty result")
	}
	if trimmed[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first msg must be user, got %v", trimmed[0].Role)
	}
	if len(trimmed) != 2 {
		t.Errorf("expected 2 messages after dropping leading assistant, got %d", len(trimmed))
	}
}

func TestTrimHistoryDropsStrandedToolResult(t *testing.T) {
	// Conversation:
	//   0: user "ask"
	//   1: assistant tool_use (id=t1)
	//   2: user tool_result (id=t1)   <-- depends on msg 1
	//   3: assistant text "answer"
	//   4: user "followup"
	//   5: assistant text "final"
	//
	// cap=4 would naive-slice to [tool_result, assistant text, user followup, assistant final].
	// That strands the tool_result (its tool_use is gone). trimHistory must skip
	// to the next clean user text, i.e. msg 4.
	history := []anthropic.MessageParam{
		userText("ask"),
		assistantToolUse("t1", "list_tasks"),
		userToolResult("t1", "[]"),
		assistantText("answer"),
		userText("followup"),
		assistantText("final"),
	}
	trimmed := trimHistory(history, 4)
	if len(trimmed) == 0 {
		t.Fatal("empty result")
	}
	// First message must be a user text message with no tool_result.
	first := trimmed[0]
	if first.Role != anthropic.MessageParamRoleUser {
		t.Errorf("first must be user, got %v", first.Role)
	}
	if messageHasToolResult(first) {
		t.Errorf("first user msg must not contain tool_result — it would be stranded")
	}
	// Expect the final two messages (followup + final).
	if len(trimmed) != 2 {
		t.Errorf("expected 2 messages (followup + final), got %d", len(trimmed))
	}
}

func TestTrimHistoryKeepsCompleteToolRound(t *testing.T) {
	// Same conversation but cap=5 — the tail is [tool_use, tool_result, answer, followup, final].
	// That starts with assistant tool_use (invalid: must start with user). Trim
	// should drop forward until it finds a clean user. Since tool_use and
	// tool_result would also be stranded, it keeps [followup, final].
	history := []anthropic.MessageParam{
		userText("ask"),
		assistantToolUse("t1", "list_tasks"),
		userToolResult("t1", "[]"),
		assistantText("answer"),
		userText("followup"),
		assistantText("final"),
	}
	trimmed := trimHistory(history, 5)
	if trimmed[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first must be user, got %v", trimmed[0].Role)
	}
	if messageHasToolResult(trimmed[0]) {
		t.Errorf("first user must not contain tool_result")
	}
}

func TestDefaultBillingCycleRange(t *testing.T) {
	t.Setenv("BILLING_DAY", "10")
	t.Setenv("TIMEZONE", "Asia/Jerusalem")

	// Explicit dates pass through unchanged.
	since, until := defaultBillingCycleRange("2026-01-01", "2026-01-31")
	if since != "2026-01-01" || until != "2026-01-31" {
		t.Errorf("explicit dates should pass through, got %s / %s", since, until)
	}

	// Empty → current cycle. We can't hardcode dates (time.Now() is real),
	// but we can assert structure: since is day-10, until is day-09.
	since, until = defaultBillingCycleRange("", "")
	if len(since) != 10 || len(until) != 10 {
		t.Fatalf("expected ISO dates, got %s / %s", since, until)
	}
	if since[8:] != "10" {
		t.Errorf("since should end in day 10, got %s", since)
	}
	if until[8:] != "09" {
		t.Errorf("until should end in day 09, got %s", until)
	}
}

// newTestAgentWithMembers wires up an Agent with a real MemberStore + GroupStore
// so update_task's assignee-against-roster validation can be exercised.
// Returns the agent plus the member store so tests can seed members.
func newTestAgentWithMembers(t *testing.T) (*Agent, *db.MemberStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	txStore, err := db.NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	txStore.Close()

	migDB, _ := sql.Open("sqlite", dbPath)
	t.Setenv("WHATSAPP_GROUP_JID", "")
	if err := db.RunMigrations(migDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	migDB.Close()

	gs, _ := db.NewGroupStore(dbPath)
	ms, _ := db.NewMemberStore(dbPath)
	// Seed a complete group so the agent's loadGroupContext returns it.
	gs.Create(context.Background(), db.Group{ID: testGroupID, OnboardingState: "complete", FinancialEnabled: true})

	t.Cleanup(func() {
		store.Close()
		gs.Close()
		ms.Close()
	})
	return &Agent{store: store, groups: gs, members: ms, history: NewHistory()}, ms
}

func TestExecuteToolUpdateTask_AssigneeValidatedAgainstMembers(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000002", DisplayName: "Bob"})

	addResult, _ := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Bob"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	// Reassign to a known member → success.
	input, _ := json.Marshal(map[string]any{"id": added.ID, "assignee": "Alice"})
	result, err := a.ExecuteTool(testGroupID, "update_task", input)
	if err != nil {
		t.Fatalf("reassign to known member: %v", err)
	}
	var updated db.Task
	json.Unmarshal([]byte(result), &updated)
	if updated.Assignee != "Alice" {
		t.Errorf("Assignee: got %q, want Alice", updated.Assignee)
	}
}

func TestExecuteToolUpdateTask_RefusesUnknownAssignee(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})

	addResult, _ := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Alice"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID, "assignee": "Carol"})
	_, err := a.ExecuteTool(testGroupID, "update_task", input)
	if err == nil {
		t.Fatal("expected refusal — Carol is not a member")
	}
	if !strings.Contains(err.Error(), "unknown assignee") {
		t.Errorf("error should mention unknown assignee: %v", err)
	}
}

func TestExecuteToolUpdateTask_ContentOnly(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	addResult, _ := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Alice"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID, "content": "Fix the kitchen sink"})
	result, err := a.ExecuteTool(testGroupID, "update_task", input)
	if err != nil {
		t.Fatalf("content-only update: %v", err)
	}
	var updated db.Task
	json.Unmarshal([]byte(result), &updated)
	if updated.Content != "Fix the kitchen sink" {
		t.Errorf("Content: got %q", updated.Content)
	}
}

func TestExecuteToolUpdateTask_RefusesEmptyUpdate(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	addResult, _ := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Alice"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID})
	_, err := a.ExecuteTool(testGroupID, "update_task", input)
	if err == nil {
		t.Fatal("expected refusal — no fields provided")
	}
	if !strings.Contains(err.Error(), "no fields provided") {
		t.Errorf("error should explain missing fields: %v", err)
	}
}

func TestExecuteToolUpdateTask(t *testing.T) {
	a := newTestAgent(t)

	addResult, _ := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Bob"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID, "status": "done"})
	result, err := a.ExecuteTool(testGroupID, "update_task", input)
	if err != nil {
		t.Fatalf("ExecuteTool update_task: %v", err)
	}

	var updated db.Task
	json.Unmarshal([]byte(result), &updated)
	if updated.Status != "done" {
		t.Errorf("expected status 'done', got %q", updated.Status)
	}
}

func TestExecuteToolDeleteTask(t *testing.T) {
	a := newTestAgent(t)

	addResult, _ := a.ExecuteTool(testGroupID, "add_task", []byte(`{"content":"Fix sink","assignee":"Bob"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID})
	result, err := a.ExecuteTool(testGroupID, "delete_task", input)
	if err != nil {
		t.Fatalf("ExecuteTool delete_task: %v", err)
	}
	if result != `{"deleted": true}` {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify it's gone
	listResult, _ := a.ExecuteTool(testGroupID, "list_tasks", []byte(`{}`))
	if listResult != "null" && listResult != "[]" {
		var tasks []db.Task
		json.Unmarshal([]byte(listResult), &tasks)
		if len(tasks) != 0 {
			t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
		}
	}
}

func TestExecuteToolUnknown(t *testing.T) {
	a := newTestAgent(t)

	_, err := a.ExecuteTool(testGroupID, "nonexistent", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestExecuteToolBadJSON(t *testing.T) {
	a := newTestAgent(t)

	_, err := a.ExecuteTool(testGroupID, "add_task", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for bad JSON input")
	}
}

// --- Version tests ---

func TestVersionInSystemPrompt(t *testing.T) {
	prompt := buildSystemPrompt(nil, nil)
	if !strings.Contains(prompt, "v"+version.Version) {
		t.Errorf("system prompt missing version %q", version.Version)
	}
	if !strings.Contains(prompt, version.DeployDate) {
		t.Errorf("system prompt missing deploy date %q", version.DeployDate)
	}
}

// --- parsePersonaPhones tests ---

func TestParsePersonaPhones(t *testing.T) {
	personas := `# Family Personas

## Alice
- **Phone:** 972501234567
- **Role:** Engineer

## Bob
- **Phone:** 972509876543
- **Role:** Husband

## Millie
- **Role:** Child
`
	phones := ParsePersonaPhones(personas)

	if phones["Alice"] != "972501234567" {
		t.Errorf("Alice phone: got %q, want 972501234567", phones["Alice"])
	}
	if phones["Bob"] != "972509876543" {
		t.Errorf("Bob phone: got %q, want 972509876543", phones["Bob"])
	}
	if _, ok := phones["Millie"]; ok {
		t.Error("Millie should not have a phone entry")
	}
}

func TestParsePersonaPhonesEmpty(t *testing.T) {
	phones := ParsePersonaPhones("")
	if len(phones) != 0 {
		t.Errorf("expected empty map, got %v", phones)
	}
}

func TestParsePersonaPhonesNoPhoneField(t *testing.T) {
	personas := `## Alice
- **Role:** Parent
`
	phones := ParsePersonaPhones(personas)
	if len(phones) != 0 {
		t.Errorf("expected empty map, got %v", phones)
	}
}

// --- Mention resolution tests ---

func TestResolveMentionsWithPhones(t *testing.T) {
	phones := map[string]string{
		"Alice":  "972501234567",
		"Bob": "972509876543",
	}

	text := "@Alice has 3 tasks. @Bob has 2 tasks."
	resolved, mentions := resolveMentionsWithPhones(text, phones)

	if !strings.Contains(resolved, "@972501234567") {
		t.Errorf("expected Alice's phone in resolved text, got: %s", resolved)
	}
	if !strings.Contains(resolved, "@972509876543") {
		t.Errorf("expected Bob's phone in resolved text, got: %s", resolved)
	}
	if strings.Contains(resolved, "@Alice") {
		t.Error("@Alice should have been replaced")
	}
	if len(mentions) != 2 {
		t.Errorf("expected 2 mentions, got %d", len(mentions))
	}
}

func TestResolveMentionsNoDuplicates(t *testing.T) {
	phones := map[string]string{"Alice": "972501234567"}

	text := "@Alice did this. @Alice did that."
	_, mentions := resolveMentionsWithPhones(text, phones)

	if len(mentions) != 1 {
		t.Errorf("expected 1 mention (no duplicates), got %d", len(mentions))
	}
}

func TestResolveMentionsNoMatch(t *testing.T) {
	phones := map[string]string{"Alice": "972501234567"}

	text := "No mentions here."
	resolved, mentions := resolveMentionsWithPhones(text, phones)

	if resolved != text {
		t.Errorf("text should be unchanged, got: %s", resolved)
	}
	if len(mentions) != 0 {
		t.Errorf("expected 0 mentions, got %d", len(mentions))
	}
}

func TestResolveMentionsEmptyPhones(t *testing.T) {
	text := "@Alice should not be resolved"
	resolved, mentions := resolveMentionsWithPhones(text, map[string]string{})

	if resolved != text {
		t.Errorf("text should be unchanged, got: %s", resolved)
	}
	if len(mentions) != 0 {
		t.Errorf("expected 0 mentions, got %d", len(mentions))
	}
}

// --- System prompt content tests ---

func TestSystemPromptContainsDigestFormat(t *testing.T) {
	prompt := buildSystemPrompt(nil, nil)
	if !strings.Contains(prompt, "Digest format") {
		t.Error("system prompt missing digest format instructions")
	}
	if !strings.Contains(prompt, "@AssigneeName") {
		t.Error("system prompt missing @AssigneeName in digest format")
	}
}

func TestSystemPromptContainsToolRules(t *testing.T) {
	prompt := buildSystemPrompt(nil, nil)
	if !strings.Contains(prompt, "Tool-use rules") {
		t.Error("system prompt missing tool-use rules section")
	}
	if !strings.Contains(prompt, "Response style") {
		t.Error("system prompt missing response style section")
	}
}
