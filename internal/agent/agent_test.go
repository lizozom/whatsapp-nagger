package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/version"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := db.NewTaskStore(dbPath)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	return &Agent{store: store}
}

func TestExecuteToolAddTask(t *testing.T) {
	a := newTestAgent(t)

	result, err := a.ExecuteTool("add_task", []byte(`{"content":"Fix sink","assignee":"Denis","due_date":"2026-03-25"}`))
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
	if task.Assignee != "Denis" {
		t.Errorf("expected assignee 'Denis', got %q", task.Assignee)
	}
	if task.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", task.Status)
	}
}

func TestExecuteToolListTasks(t *testing.T) {
	a := newTestAgent(t)

	a.ExecuteTool("add_task", []byte(`{"content":"Task 1","assignee":"Denis"}`))
	a.ExecuteTool("add_task", []byte(`{"content":"Task 2","assignee":"Liza"}`))

	result, err := a.ExecuteTool("list_tasks", []byte(`{"assignee":"Denis"}`))
	if err != nil {
		t.Fatalf("ExecuteTool list_tasks: %v", err)
	}

	var tasks []db.Task
	if err := json.Unmarshal([]byte(result), &tasks); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task for Denis, got %d", len(tasks))
	}
}

func TestExecuteToolListTasksEmpty(t *testing.T) {
	a := newTestAgent(t)

	result, err := a.ExecuteTool("list_tasks", []byte(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool list_tasks: %v", err)
	}
	if result != "null" && result != "[]" {
		// empty slice marshals to "null" in Go
		t.Logf("empty list result: %s", result)
	}
}

func TestExecuteToolUpdateTask(t *testing.T) {
	a := newTestAgent(t)

	addResult, _ := a.ExecuteTool("add_task", []byte(`{"content":"Fix sink","assignee":"Denis"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID, "status": "done"})
	result, err := a.ExecuteTool("update_task", input)
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

	addResult, _ := a.ExecuteTool("add_task", []byte(`{"content":"Fix sink","assignee":"Denis"}`))
	var added db.Task
	json.Unmarshal([]byte(addResult), &added)

	input, _ := json.Marshal(map[string]any{"id": added.ID})
	result, err := a.ExecuteTool("delete_task", input)
	if err != nil {
		t.Fatalf("ExecuteTool delete_task: %v", err)
	}
	if result != `{"deleted": true}` {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify it's gone
	listResult, _ := a.ExecuteTool("list_tasks", []byte(`{}`))
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

	_, err := a.ExecuteTool("nonexistent", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestExecuteToolBadJSON(t *testing.T) {
	a := newTestAgent(t)

	_, err := a.ExecuteTool("add_task", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for bad JSON input")
	}
}

// --- Version tests ---

func TestVersionInSystemPrompt(t *testing.T) {
	prompt := buildSystemPrompt()
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

## Liza
- **Phone:** 972546260906
- **Role:** Engineer

## Denis
- **Phone:** 972547084477
- **Role:** Husband

## Millie
- **Role:** Child
`
	phones := parsePersonaPhones(personas)

	if phones["Liza"] != "972546260906" {
		t.Errorf("Liza phone: got %q, want 972546260906", phones["Liza"])
	}
	if phones["Denis"] != "972547084477" {
		t.Errorf("Denis phone: got %q, want 972547084477", phones["Denis"])
	}
	if _, ok := phones["Millie"]; ok {
		t.Error("Millie should not have a phone entry")
	}
}

func TestParsePersonaPhonesEmpty(t *testing.T) {
	phones := parsePersonaPhones("")
	if len(phones) != 0 {
		t.Errorf("expected empty map, got %v", phones)
	}
}

func TestParsePersonaPhonesNoPhoneField(t *testing.T) {
	personas := `## Alice
- **Role:** Parent
`
	phones := parsePersonaPhones(personas)
	if len(phones) != 0 {
		t.Errorf("expected empty map, got %v", phones)
	}
}

// --- Mention resolution tests ---

func TestResolveMentionsWithPhones(t *testing.T) {
	phones := map[string]string{
		"Liza":  "972546260906",
		"Denis": "972547084477",
	}

	text := "@Liza has 3 tasks. @Denis has 2 tasks."
	resolved, mentions := resolveMentionsWithPhones(text, phones)

	if !strings.Contains(resolved, "@972546260906") {
		t.Errorf("expected Liza's phone in resolved text, got: %s", resolved)
	}
	if !strings.Contains(resolved, "@972547084477") {
		t.Errorf("expected Denis's phone in resolved text, got: %s", resolved)
	}
	if strings.Contains(resolved, "@Liza") {
		t.Error("@Liza should have been replaced")
	}
	if len(mentions) != 2 {
		t.Errorf("expected 2 mentions, got %d", len(mentions))
	}
}

func TestResolveMentionsNoDuplicates(t *testing.T) {
	phones := map[string]string{"Liza": "972546260906"}

	text := "@Liza did this. @Liza did that."
	_, mentions := resolveMentionsWithPhones(text, phones)

	if len(mentions) != 1 {
		t.Errorf("expected 1 mention (no duplicates), got %d", len(mentions))
	}
}

func TestResolveMentionsNoMatch(t *testing.T) {
	phones := map[string]string{"Liza": "972546260906"}

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
	text := "@Liza should not be resolved"
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
	prompt := buildSystemPrompt()
	if !strings.Contains(prompt, "Digest format") {
		t.Error("system prompt missing digest format instructions")
	}
	if !strings.Contains(prompt, "@AssigneeName") {
		t.Error("system prompt missing @AssigneeName in digest format")
	}
}

func TestSystemPromptContainsToolRules(t *testing.T) {
	prompt := buildSystemPrompt()
	if !strings.Contains(prompt, "Tool-use rules") {
		t.Error("system prompt missing tool-use rules section")
	}
	if !strings.Contains(prompt, "Response style") {
		t.Error("system prompt missing response style section")
	}
}
