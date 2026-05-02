package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

func toolNames(tools []anthropic.ToolUnionParam) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.OfTool != nil {
			out = append(out, t.OfTool.Name)
		}
	}
	return out
}

func hasName(names []string, s string) bool {
	for _, n := range names {
		if n == s {
			return true
		}
	}
	return false
}

func TestBuildTools_MainAlwaysOnIncluded(t *testing.T) {
	group := &db.Group{ID: "120363TT01@g.us", FinancialEnabled: false}
	names := toolNames(BuildTools(context.Background(), group, KindMain))

	for _, n := range []string{"add_task", "list_tasks", "update_task", "delete_task", "dashboard_link"} {
		if !hasName(names, n) {
			t.Errorf("missing always-on tool %q (got %v)", n, names)
		}
	}
}

func TestBuildTools_MainFinancialAbsentWhenDisabled(t *testing.T) {
	group := &db.Group{ID: "120363TT02@g.us", FinancialEnabled: false}
	names := toolNames(BuildTools(context.Background(), group, KindMain))

	for _, n := range []string{"expenses_summary", "list_transactions"} {
		if hasName(names, n) {
			t.Errorf("financial tool %q must be PHYSICALLY ABSENT when financial_enabled=false (got %v)", n, names)
		}
	}
}

func TestBuildTools_MainFinancialPresentWhenEnabled(t *testing.T) {
	group := &db.Group{ID: "120363TT03@g.us", FinancialEnabled: true}
	names := toolNames(BuildTools(context.Background(), group, KindMain))

	for _, n := range []string{"expenses_summary", "list_transactions"} {
		if !hasName(names, n) {
			t.Errorf("financial tool %q should be present when financial_enabled=true (got %v)", n, names)
		}
	}
}

func TestBuildTools_OnboardingIsExactlyTheFiveOnboardingTools(t *testing.T) {
	names := toolNames(BuildTools(context.Background(), &db.Group{}, KindOnboarding))

	wantExact := []string{"set_language", "set_member", "set_timezone", "set_digest_hour", "complete_onboarding"}
	if len(names) != len(wantExact) {
		t.Errorf("onboarding tools: got %d (%v), want %d", len(names), names, len(wantExact))
	}
	for _, n := range wantExact {
		if !hasName(names, n) {
			t.Errorf("missing onboarding tool %q (got %v)", n, names)
		}
	}
	for _, n := range []string{"add_task", "list_tasks", "expenses_summary", "dashboard_link"} {
		if hasName(names, n) {
			t.Errorf("onboarding surface leaked main tool %q (got %v)", n, names)
		}
	}
}

func TestBuildSystemPrompt_LocksToHebrew(t *testing.T) {
	group := &db.Group{ID: "120363TT04@g.us", Language: "he", Timezone: "Asia/Jerusalem"}
	prompt := buildSystemPrompt(group, []db.Member{
		{GroupID: group.ID, WhatsAppID: "100000000001", DisplayName: "Alice"},
	})
	if !strings.Contains(prompt, "Reply ONLY in Hebrew") {
		t.Error("Hebrew lock instruction missing")
	}
}

func TestBuildSystemPrompt_NoLockForEnglish(t *testing.T) {
	group := &db.Group{ID: "120363TT05@g.us", Language: "en", Timezone: "Asia/Jerusalem"}
	prompt := buildSystemPrompt(group, nil)
	if strings.Contains(prompt, "Reply ONLY in Hebrew") {
		t.Error("English group should not have Hebrew lock")
	}
}

func TestBuildSystemPrompt_UsesGroupTimezone(t *testing.T) {
	group := &db.Group{ID: "120363TT06@g.us", Language: "en", Timezone: "Europe/Berlin"}
	prompt := buildSystemPrompt(group, nil)
	if !strings.Contains(prompt, "Europe/Berlin") {
		t.Errorf("prompt should include group's timezone Europe/Berlin")
	}
}

func TestBuildSystemPrompt_UsesMemberDisplayNames(t *testing.T) {
	group := &db.Group{ID: "120363TT07@g.us", Language: "en", Timezone: "Asia/Jerusalem"}
	prompt := buildSystemPrompt(group, []db.Member{
		{GroupID: group.ID, WhatsAppID: "100000000001", DisplayName: "Alice"},
		{GroupID: group.ID, WhatsAppID: "100000000002", DisplayName: "Bob"},
	})
	if !strings.Contains(prompt, "Alice") || !strings.Contains(prompt, "Bob") {
		t.Errorf("prompt should list member display names; got: %s", prompt)
	}
}
