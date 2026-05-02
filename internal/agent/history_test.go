package agent

import (
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func userMsg(text string) anthropic.MessageParam {
	return anthropic.NewUserMessage(anthropic.NewTextBlock(text))
}

func TestHistoryAppendIsolatedPerKey(t *testing.T) {
	h := NewHistory()
	groupA := historyKey{GroupID: "120363AAAAAA@g.us", AgentKind: KindMain}
	groupB := historyKey{GroupID: "120363BBBBBB@g.us", AgentKind: KindMain}

	h.Append(groupA, userMsg("[Alice]: hello A"))
	h.Append(groupB, userMsg("[Bob]: hello B"))

	a := h.Get(groupA)
	b := h.Get(groupB)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 msg per group, got A=%d B=%d", len(a), len(b))
	}
}

func TestHistoryAppendIsolatedPerAgentKind(t *testing.T) {
	h := NewHistory()
	gid := "120363CCCCCC@g.us"
	main := historyKey{GroupID: gid, AgentKind: KindMain}
	onb := historyKey{GroupID: gid, AgentKind: KindOnboarding}

	h.Append(main, userMsg("[Alice]: main message"))
	h.Append(onb, userMsg("[Alice]: onboarding answer"))

	if len(h.Get(main)) != 1 {
		t.Errorf("main window: got %d, want 1", len(h.Get(main)))
	}
	if len(h.Get(onb)) != 1 {
		t.Errorf("onboarding window: got %d, want 1", len(h.Get(onb)))
	}
}

func TestHistoryDiscardRemovesOnlyTargetKey(t *testing.T) {
	h := NewHistory()
	gid := "120363DDDDDD@g.us"
	main := historyKey{GroupID: gid, AgentKind: KindMain}
	onb := historyKey{GroupID: gid, AgentKind: KindOnboarding}

	h.Append(main, userMsg("main"))
	h.Append(onb, userMsg("onboarding"))

	h.Discard(onb)
	if got := h.Get(onb); got != nil {
		t.Errorf("onboarding should be discarded, got %d messages", len(got))
	}
	if got := h.Get(main); len(got) != 1 {
		t.Errorf("main should be untouched, got %d", len(got))
	}
}

func TestHistoryAppendAppliesTrimPerKey(t *testing.T) {
	h := NewHistory()
	key := historyKey{GroupID: "120363EEEEEE@g.us", AgentKind: KindMain}

	// Append more than the cap; verify the window is bounded.
	for i := 0; i < maxHistoryMessages+5; i++ {
		h.Append(key, userMsg(fmt.Sprintf("[Alice]: msg %d", i)))
	}
	got := h.Get(key)
	if len(got) > maxHistoryMessages {
		t.Errorf("window exceeds cap: got %d, want <= %d", len(got), maxHistoryMessages)
	}
}

func TestHistorySnapshotsAreDecoupled(t *testing.T) {
	h := NewHistory()
	key := historyKey{GroupID: "120363FFFFFF@g.us", AgentKind: KindMain}
	h.Append(key, userMsg("first"))
	snap := h.Get(key)
	h.Append(key, userMsg("second"))
	if len(snap) != 1 {
		t.Errorf("earlier snapshot should not see later append, got %d", len(snap))
	}
}
