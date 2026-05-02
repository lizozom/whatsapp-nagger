package agent

import (
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// AgentKind discriminates between the main task/expense agent and the
// onboarding agent. Each (group, kind) pair has its own conversation history
// so onboarding turns don't bleed into main-agent context (architecture D5/D6).
type AgentKind string

const (
	KindMain       AgentKind = "main"
	KindOnboarding AgentKind = "onboarding"
)

// historyKey scopes a conversation window. In-memory only; lost on restart
// (acceptable per D5).
type historyKey struct {
	GroupID   string
	AgentKind AgentKind
}

// History is a process-wide store of per-(group, agent-kind) conversation
// windows. Each window is trimmed to maxHistoryMessages on every Append,
// using the same algorithm as the pre-refactor single-conversation cap (NFR6).
//
// All methods are safe for concurrent use — schedulers and the main message
// loop touch the same instance.
type History struct {
	mu       sync.Mutex
	messages map[historyKey][]anthropic.MessageParam
}

func NewHistory() *History {
	return &History{messages: make(map[historyKey][]anthropic.MessageParam)}
}

// Append adds msg to the window for key, applies the trim cap, and returns
// the resulting slice (a snapshot the caller may pass directly to Anthropic).
func (h *History) Append(key historyKey, msg anthropic.MessageParam) []anthropic.MessageParam {
	h.mu.Lock()
	defer h.mu.Unlock()
	window := append(h.messages[key], msg)
	window = trimHistory(window, maxHistoryMessages)
	h.messages[key] = window
	out := make([]anthropic.MessageParam, len(window))
	copy(out, window)
	return out
}

// Get returns a snapshot of the current window for key, or nil if absent.
func (h *History) Get(key historyKey) []anthropic.MessageParam {
	h.mu.Lock()
	defer h.mu.Unlock()
	window := h.messages[key]
	if window == nil {
		return nil
	}
	out := make([]anthropic.MessageParam, len(window))
	copy(out, window)
	return out
}

// Discard removes the window for key. Used by complete_onboarding (D6) so
// the main agent starts fresh with no onboarding leakage.
func (h *History) Discard(key historyKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.messages, key)
}
