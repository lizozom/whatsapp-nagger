package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

func TestToolGetGroupSettings_OmitsOperatorFields(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})
	a.groups.SetName(ctx, testGroupID, "Test Family")
	a.groups.SetTimezone(ctx, testGroupID, "Asia/Jerusalem")
	a.groups.SetDigestHour(ctx, testGroupID, 9)
	a.groups.SetLanguage(ctx, testGroupID, "en")

	result, err := a.ExecuteTool(testGroupID, "get_group_settings", []byte(`{}`))
	if err != nil {
		t.Fatalf("get_group_settings: %v", err)
	}
	for _, leaked := range []string{"financial_enabled", "onboarding_state", "last_active_at"} {
		if strings.Contains(result, leaked) {
			t.Errorf("operator-only field %q must not appear in tool output: %s", leaked, result)
		}
	}
	for _, must := range []string{"Test Family", "Asia/Jerusalem", "Alice", "100000000001"} {
		if !strings.Contains(result, must) {
			t.Errorf("expected field %q in result: %s", must, result)
		}
	}
}

func TestToolUpdateGroupSettings_ValidFields(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)

	input, _ := json.Marshal(map[string]any{
		"name":        "Renamed Group",
		"timezone":    "Europe/Berlin",
		"digest_hour": 7,
	})
	if _, err := a.ExecuteTool(testGroupID, "update_group_settings", input); err != nil {
		t.Fatalf("update_group_settings: %v", err)
	}
	got, _ := a.groups.Get(context.Background(), testGroupID)
	if got.Name != "Renamed Group" || got.Timezone != "Europe/Berlin" || got.DigestHour != 7 {
		t.Errorf("settings not applied: %+v", got)
	}
}

func TestToolUpdateGroupSettings_RejectsInvalidTimezone(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	input, _ := json.Marshal(map[string]any{"timezone": "Asia/Atlantis"})
	if _, err := a.ExecuteTool(testGroupID, "update_group_settings", input); err == nil {
		t.Fatal("expected refusal for invalid IANA timezone")
	}
}

func TestToolUpdateGroupSettings_RejectsInvalidDigestHour(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	input, _ := json.Marshal(map[string]any{"digest_hour": 25})
	if _, err := a.ExecuteTool(testGroupID, "update_group_settings", input); err == nil {
		t.Fatal("expected refusal for hour=25")
	}
}

func TestToolUpdateGroupSettings_RefusesEmpty(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	if _, err := a.ExecuteTool(testGroupID, "update_group_settings", []byte(`{}`)); err == nil {
		t.Fatal("expected refusal for no fields")
	}
}

func TestToolAddMember_BelowCap(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	input, _ := json.Marshal(map[string]any{"name": "Alice", "whatsapp_id": "100000000001"})
	if _, err := a.ExecuteTool(testGroupID, "add_member", input); err != nil {
		t.Fatalf("add_member: %v", err)
	}
	mems, _ := a.members.List(context.Background(), testGroupID)
	if len(mems) != 1 || mems[0].DisplayName != "Alice" {
		t.Errorf("members: got %+v", mems)
	}
}

func TestToolAddMember_RefusedAtCap(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000002", DisplayName: "Bob"})

	input, _ := json.Marshal(map[string]any{"name": "Carol", "whatsapp_id": "100000000003"})
	if _, err := a.ExecuteTool(testGroupID, "add_member", input); err == nil {
		t.Fatal("expected refusal — over member cap")
	}
}

func TestToolUpdateMember_CascadesToTaskAssignee(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})
	a.store.AddTask(testGroupID, "Open task", "Alice", "")
	doneTask, _ := a.store.AddTask(testGroupID, "Done task", "Alice", "")
	a.store.UpdateTask(testGroupID, doneTask.ID, db.TaskUpdate{Status: "done"})

	input, _ := json.Marshal(map[string]any{"whatsapp_id": "100000000001", "display_name": "Alicia"})
	result, err := a.ExecuteTool(testGroupID, "update_member", input)
	if err != nil {
		t.Fatalf("update_member: %v", err)
	}
	if !strings.Contains(result, `"tasks_reassigned": 1`) {
		t.Errorf("expected 1 pending task reassigned, got: %s", result)
	}
	// Pending task → renamed.
	pending, _ := a.store.ListTasks(testGroupID, "", "pending")
	if len(pending) != 1 || pending[0].Assignee != "Alicia" {
		t.Errorf("pending task should be reassigned to Alicia: %+v", pending)
	}
	// Done task → unchanged.
	done, _ := a.store.ListTasks(testGroupID, "", "done")
	if len(done) != 1 || done[0].Assignee != "Alice" {
		t.Errorf("done task should keep Alice: %+v", done)
	}
}

func TestToolRemoveMember_AutoReassignsPending(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000002", DisplayName: "Bob"})
	a.store.AddTask(testGroupID, "Alice's task", "Alice", "")
	doneTask, _ := a.store.AddTask(testGroupID, "Alice's done", "Alice", "")
	a.store.UpdateTask(testGroupID, doneTask.ID, db.TaskUpdate{Status: "done"})

	input, _ := json.Marshal(map[string]any{"whatsapp_id": "100000000001"})
	result, err := a.ExecuteTool(testGroupID, "remove_member", input)
	if err != nil {
		t.Fatalf("remove_member: %v", err)
	}
	if !strings.Contains(result, `"tasks_reassigned": 1`) {
		t.Errorf("expected 1 pending task reassigned, got: %s", result)
	}
	mems, _ := ms.List(ctx, testGroupID)
	if len(mems) != 1 || mems[0].DisplayName != "Bob" {
		t.Errorf("after remove: got %+v, want [Bob]", mems)
	}
	pending, _ := a.store.ListTasks(testGroupID, "", "pending")
	if len(pending) != 1 || pending[0].Assignee != "Bob" {
		t.Errorf("pending task should now be Bob's: %+v", pending)
	}
	// Done task keeps Alice.
	done, _ := a.store.ListTasks(testGroupID, "", "done")
	if len(done) != 1 || done[0].Assignee != "Alice" {
		t.Errorf("done task should keep Alice: %+v", done)
	}
}

func TestToolRemoveMember_RefusesIfWouldEmpty(t *testing.T) {
	a, ms := newTestAgentWithMembers(t)
	ctx := context.Background()
	ms.Add(ctx, testGroupID, db.Member{GroupID: testGroupID, WhatsAppID: "100000000001", DisplayName: "Alice"})

	input, _ := json.Marshal(map[string]any{"whatsapp_id": "100000000001"})
	if _, err := a.ExecuteTool(testGroupID, "remove_member", input); err == nil {
		t.Fatal("expected refusal — removing the only member would empty the group")
	}
	mems, _ := ms.List(ctx, testGroupID)
	if len(mems) != 1 {
		t.Errorf("Alice should still be present, got %+v", mems)
	}
}

func TestToolRemoveMember_NotFound(t *testing.T) {
	a, _ := newTestAgentWithMembers(t)
	input, _ := json.Marshal(map[string]any{"whatsapp_id": "999999999999"})
	if _, err := a.ExecuteTool(testGroupID, "remove_member", input); err == nil {
		t.Fatal("expected refusal — member not in this group")
	}
}

func TestToolUpdateGroupSettings_LanguageNotInSchema(t *testing.T) {
	// Even if the LLM somehow tries to pass language, the JSON unmarshal
	// won't bind it (no Language field) and it's silently ignored. The DB
	// should remain on whatever language was set. This protects NFR4.
	a, _ := newTestAgentWithMembers(t)
	ctx := context.Background()
	a.groups.SetLanguage(ctx, testGroupID, "en")

	input, _ := json.Marshal(map[string]any{"language": "he", "name": "ok"})
	if _, err := a.ExecuteTool(testGroupID, "update_group_settings", input); err != nil {
		t.Fatalf("update_group_settings: %v", err)
	}
	got, _ := a.groups.Get(ctx, testGroupID)
	if got.Language != "en" {
		t.Errorf("language should be locked at 'en', got %q", got.Language)
	}
}

func TestBuildTools_IncludesGroupAndMemberTools(t *testing.T) {
	names := toolNames(BuildTools(context.Background(), &db.Group{}, KindMain))
	for _, must := range []string{
		"get_group_settings", "update_group_settings",
		"add_member", "update_member", "remove_member",
	} {
		if !hasName(names, must) {
			t.Errorf("missing tool %q from main surface (got %v)", must, names)
		}
	}
}
