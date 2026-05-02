package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
)

// OnboardingAgent walks a newly-added group through bilingual setup
// (language → members → timezone → digest hour → confirm) and flips the
// group's onboarding_state to "complete" when done.
type OnboardingAgent struct {
	client    anthropic.Client
	groups    *db.GroupStore
	members   *db.MemberStore
	history   *History
	messenger messenger.IMessenger
}

func NewOnboardingAgent(groups *db.GroupStore, members *db.MemberStore, history *History, m messenger.IMessenger) *OnboardingAgent {
	return &OnboardingAgent{
		client:    anthropic.NewClient(),
		groups:    groups,
		members:   members,
		history:   history,
		messenger: m,
	}
}

// Handle runs one onboarding turn. The system prompt is computed per-message
// from the current groups+members rows so the LLM can resume from the last
// unanswered question across process restarts.
func (o *OnboardingAgent) Handle(ctx context.Context, groupID, sender, text string) error {
	group, err := o.groups.Get(ctx, groupID)
	if err != nil {
		return fmt.Errorf("onboarding: load group: %w", err)
	}
	if group == nil {
		// The dispatcher should have routed only known groups here. If we
		// somehow got an unknown one, refuse rather than silently no-op.
		return fmt.Errorf("onboarding: no groups row for %s", groupID)
	}
	members, err := o.members.List(ctx, groupID)
	if err != nil {
		return fmt.Errorf("onboarding: list members: %w", err)
	}

	key := historyKey{GroupID: groupID, AgentKind: KindOnboarding}
	userContent := fmt.Sprintf("[%s]: %s", sender, text)
	window := o.history.Append(key, anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)))

	tools := buildOnboardingTools()
	systemPrompt := buildOnboardingSystemPrompt(group, members)

	for {
		message, err := o.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 1024,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  window,
			Tools:     tools,
		})
		if err != nil {
			return fmt.Errorf("onboarding: claude api: %w", err)
		}

		window = o.history.Append(key, message.ToParam())

		var textParts []string
		var toolResults []anthropic.ContentBlockParamUnion
		var completed bool

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				textParts = append(textParts, variant.Text)
			case anthropic.ToolUseBlock:
				result, didComplete, toolErr := o.executeTool(ctx, groupID, variant.Name, []byte(variant.JSON.Input.Raw()))
				if toolErr != nil {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(variant.ID, toolErr.Error(), true))
				} else {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(variant.ID, result, false))
				}
				if didComplete {
					completed = true
				}
			}
		}

		if len(toolResults) == 0 {
			response := strings.Join(textParts, "\n")
			if response == "" {
				return nil
			}
			return o.messenger.Write(groupID, response)
		}

		// Onboarding done. The history was discarded by complete_onboarding
		// (D6), so we can't safely call Anthropic again — the tool_result
		// would be orphaned without its matching tool_use in the prior
		// assistant message. Send the assistant's pre-tool text (if any)
		// plus a hardcoded confirmation in the locked language and return.
		if completed {
			msg := strings.TrimSpace(strings.Join(textParts, "\n"))
			closing := completionMessage(group.Language)
			if msg != "" {
				msg += "\n\n" + closing
			} else {
				msg = closing
			}
			return o.messenger.Write(groupID, msg)
		}

		window = o.history.Append(key, anthropic.NewUserMessage(toolResults...))
	}
}

// completionMessage returns the hardcoded "all set" reply in the group's
// locked language. Used after complete_onboarding to avoid a second
// Anthropic round-trip with an orphaned tool_result.
func completionMessage(language string) string {
	switch language {
	case "he":
		return "הכל מוכן! 🎉 מעכשיו אני בקבוצה הראשית. תגידו לי על משימות והוצאות — אזכיר אתכם כל בוקר."
	default:
		return "All set! 🎉 I'm now in main mode. Send me tasks anytime — I'll remind you every morning."
	}
}

// executeTool dispatches an onboarding tool by name. Returns (result, didComplete, error).
// didComplete is true only when complete_onboarding succeeded — the dispatcher
// uses it to know the next inbound message should route to the main agent.
func (o *OnboardingAgent) executeTool(ctx context.Context, groupID, name string, inputJSON []byte) (string, bool, error) {
	switch name {
	case "set_language":
		return o.toolSetLanguage(ctx, groupID, inputJSON)
	case "set_member":
		return o.toolSetMember(ctx, groupID, inputJSON)
	case "set_timezone":
		return o.toolSetTimezone(ctx, groupID, inputJSON)
	case "set_digest_hour":
		return o.toolSetDigestHour(ctx, groupID, inputJSON)
	case "complete_onboarding":
		return o.toolCompleteOnboarding(ctx, groupID)
	default:
		return "", false, fmt.Errorf("unknown onboarding tool: %s", name)
	}
}

func (o *OnboardingAgent) toolSetLanguage(ctx context.Context, groupID string, inputJSON []byte) (string, bool, error) {
	var input struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", false, fmt.Errorf("parse input: %w", err)
	}
	if input.Language != "he" && input.Language != "en" {
		return "", false, fmt.Errorf("language must be 'he' or 'en', got %q", input.Language)
	}
	group, err := o.groups.Get(ctx, groupID)
	if err != nil {
		return "", false, err
	}
	if group != nil && group.Language != "" {
		return "", false, fmt.Errorf("language is already set to %q and cannot be changed", group.Language)
	}
	if err := o.groups.SetLanguage(ctx, groupID, input.Language); err != nil {
		return "", false, err
	}
	return fmt.Sprintf(`{"language": %q}`, input.Language), false, nil
}

var phoneRe = regexp.MustCompile(`^[0-9]{8,15}$`)

func (o *OnboardingAgent) toolSetMember(ctx context.Context, groupID string, inputJSON []byte) (string, bool, error) {
	var input struct {
		Name       string `json:"name"`
		WhatsAppID string `json:"whatsapp_id"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", false, fmt.Errorf("parse input: %w", err)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.WhatsAppID = strings.TrimSpace(input.WhatsAppID)
	if input.Name == "" {
		return "", false, fmt.Errorf("name is required")
	}
	if !phoneRe.MatchString(input.WhatsAppID) {
		return "", false, fmt.Errorf("whatsapp_id must be digits only in international format (no `+`), got %q", input.WhatsAppID)
	}

	current, err := o.members.List(ctx, groupID)
	if err != nil {
		return "", false, err
	}
	// If this whatsapp_id already exists, it's an update, not a new add — cap not consulted.
	exists := false
	for _, m := range current {
		if m.WhatsAppID == input.WhatsAppID {
			exists = true
			break
		}
	}
	if !exists && len(current) >= db.MemberCap {
		return "", false, fmt.Errorf("group already has %d members; v1 supports up to %d per group", len(current), db.MemberCap)
	}
	if err := o.members.Upsert(ctx, groupID, db.Member{
		GroupID: groupID, WhatsAppID: input.WhatsAppID, DisplayName: input.Name,
	}); err != nil {
		return "", false, err
	}
	return fmt.Sprintf(`{"name": %q, "whatsapp_id": %q, "members_count": %d}`,
		input.Name, input.WhatsAppID, len(current)+boolToInt(!exists)), false, nil
}

func (o *OnboardingAgent) toolSetTimezone(ctx context.Context, groupID string, inputJSON []byte) (string, bool, error) {
	var input struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", false, fmt.Errorf("parse input: %w", err)
	}
	input.Timezone = strings.TrimSpace(input.Timezone)
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return "", false, fmt.Errorf("invalid IANA timezone %q (e.g. Asia/Jerusalem, Europe/Berlin)", input.Timezone)
	}
	if err := o.groups.SetTimezone(ctx, groupID, input.Timezone); err != nil {
		return "", false, err
	}
	return fmt.Sprintf(`{"timezone": %q}`, input.Timezone), false, nil
}

func (o *OnboardingAgent) toolSetDigestHour(ctx context.Context, groupID string, inputJSON []byte) (string, bool, error) {
	var input struct {
		Hour int `json:"hour"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", false, fmt.Errorf("parse input: %w", err)
	}
	if input.Hour < 0 || input.Hour > 23 {
		return "", false, fmt.Errorf("hour must be 0..23, got %d", input.Hour)
	}
	if err := o.groups.SetDigestHour(ctx, groupID, input.Hour); err != nil {
		return "", false, err
	}
	return fmt.Sprintf(`{"hour": %d}`, input.Hour), false, nil
}

func (o *OnboardingAgent) toolCompleteOnboarding(ctx context.Context, groupID string) (string, bool, error) {
	group, err := o.groups.Get(ctx, groupID)
	if err != nil {
		return "", false, err
	}
	members, err := o.members.List(ctx, groupID)
	if err != nil {
		return "", false, err
	}
	var missing []string
	if group.Language == "" {
		missing = append(missing, "language")
	}
	if group.Timezone == "" {
		missing = append(missing, "timezone")
	}
	if !group.DigestHourSet {
		missing = append(missing, "digest_hour")
	}
	if len(members) == 0 {
		missing = append(missing, "at least one member")
	}
	if len(missing) > 0 {
		return "", false, fmt.Errorf("cannot complete onboarding — still missing: %s", strings.Join(missing, ", "))
	}

	if err := o.groups.MarkComplete(ctx, groupID); err != nil {
		return "", false, err
	}
	o.history.Discard(historyKey{GroupID: groupID, AgentKind: KindOnboarding})
	slog.Info("onboarding complete", slog.String("group_id", groupID))
	return `{"status": "complete"}`, true, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
