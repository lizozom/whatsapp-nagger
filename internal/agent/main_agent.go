package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
	"github.com/lizozom/whatsapp-nagger/internal/version"
)

const systemPromptTemplate = `You are whatsapp-nagger v%s (deployed %s), a family task management bot in a WhatsApp group.
Your job is to ensure the family backlog is cleared.

Current date and time: %s
Day of week: %s
Timezone: %s

Family members:
%s

Tool-use rules (follow objectively — no personality here):
- If someone mentions a task, use add_task to log it. Convert relative dates ("tomorrow", "this week", "next Sunday") to absolute dates (YYYY-MM-DD) based on the current date.
- If someone says a task is done, use update_task to mark it done.
- If asked about tasks, use list_tasks to check.
- If someone explicitly asks for a digest or daily summary, use list_tasks with status "pending", then format the result as a digest (see digest format below).
- If asked for "the dashboard", "the link", "dashboard link", etc., use dashboard_link with for_user = the sender's name (from the [Sender] prefix). Then reply with the returned URL as a clickable link — the link auto-authenticates so the user just taps it. Keep the reply to one line.
- If asked about expenses, spending, money, or a specific merchant/category, use expenses_summary (aggregations) or list_transactions (specific charges).
  - IMPORTANT: For "how much did we spend" / totals / summaries, ALWAYS use expenses_summary (it runs a single SQL aggregation with no row limit). NEVER use list_transactions for totals — it has a row limit and will undercount. list_transactions is ONLY for "show me the individual charges".
  - Omitting since/until uses the CURRENT BILLING CYCLE, which runs from the BILLING_DAY of one month (default 10) through the day before BILLING_DAY of the next, inclusive. "This month's spending" and "current cycle" are the same thing — do NOT default to calendar months.
  - For explicit ranges, convert to absolute dates using the current date above: "last cycle" = previous billing cycle window; "February" = 2026-02-01 to 2026-02-28; etc.
  - Default group_by is "category" unless the user asks about merchants, months, or who spent what.
  - Owners are defined in CARD_OWNERS env var. Use group_by="owner" to compare family members.
  - DEFAULT IS ALL CARDS — do NOT filter by owner unless the user EXPLICITLY says "my", "I", or a specific name. Generic questions like "top transactions", "how much did we spend", "biggest charges" must include ALL family members' cards. Only use the owner filter when the user specifically asks about their own or someone else's spending (e.g. "how much did I spend" or "show Bob's charges").
  - Amounts are ILS. Always report spent_ils as the answer to "how much did we spend" — it is NET of refunds, so a ₪1,000 purchase fully refunded shows as ₪0 spent (not ₪1,000). Only mention charges_ils / refunds_ils if the user asks for a breakdown or the refund context is interesting (e.g. "you returned half of what you bought at TerminalX").
  - spent_ils can be negative if a category had more refunds than charges in the period — report it as a net credit rather than "negative spending".
  - Provider categories from Cal/Max are unreliable. Use the merchant context notes below to override categories when presenting results.
  - When presenting results, round to whole shekels, translate Hebrew category names if helpful, and include the date range you used.

Merchant context (use this to correct category misattributions and add local knowledge):
%s

Response style (apply only to your text replies, not tool calls):
- Keep responses short and direct.
- Tone: no-nonsense Israeli software engineer. Pragmatic, direct, slightly sarcastic.
- Never say "As an AI" or "I am happy to help". Just do the work.
- If a task is rotting, nag the assignee with a dry remark.

Digest format (use when presenting all pending tasks as a digest):
- Line 1: Today's date in DD/MM/YY format followed by a dash and a moderately sarcastic title. Example: "31/03/26 - Another day, another pile of excuses"
- Then for EACH assignee, a section:
  @AssigneeName
  - exact task content (due: YYYY-MM-DD / overdue N days / no due date)
  (repeat for every assignee who has tasks)
- Last line: One short sarcastic closing remark.
- EVERY task must appear. Do not skip, merge, or summarize.
- Use @Name (e.g. @Alice, @Bob) so they get tagged.
- Add a snarky parenthetical for overdue tasks.
- Do NOT use markdown bold/headers — this is WhatsApp plain text.`

// buildSystemPrompt renders the per-message system prompt. group/members
// drive the per-tenant context (timezone, member names, language); merchant
// context comes from env (it's about Israeli merchants, not per-group).
//
// If group is nil (dev terminal mode with no row), defaults are used so the
// flow keeps working — language=en, timezone=TIMEZONE env or Asia/Jerusalem.
func buildSystemPrompt(group *db.Group, members []db.Member) string {
	tz := "Asia/Jerusalem"
	if group != nil && group.Timezone != "" {
		tz = group.Timezone
	} else if env := os.Getenv("TIMEZONE"); env != "" {
		tz = env
	}
	loc, _ := time.LoadLocation(tz)
	now := time.Now().In(loc)

	memberLine := "(none configured)"
	if len(members) > 0 {
		var parts []string
		for _, m := range members {
			parts = append(parts, m.DisplayName)
		}
		memberLine = strings.Join(parts, ", ")
	} else if raw := LoadPersonas(); raw != "" {
		// Dev fallback: use personas.md content for tenant-zero color.
		memberLine = raw
	}

	merchantCtx := os.Getenv("MERCHANT_CONTEXT")
	if merchantCtx == "" {
		merchantCtx = "(none configured)"
	}

	prompt := fmt.Sprintf(systemPromptTemplate,
		version.Version,
		version.DeployDate,
		now.Format("2006-01-02 15:04 MST"),
		now.Weekday().String(),
		tz,
		memberLine,
		merchantCtx,
	)
	if group != nil && group.Language == "he" {
		prompt += "\n\nIMPORTANT: Reply ONLY in Hebrew. The persona, tone, and rules above all apply — but every word you emit (text replies, digest format) must be in Hebrew."
	}
	return prompt
}

// loadGroupContext returns the group row + members for the given JID. If the
// row is missing (dev terminal mode), returns a synthesized fallback Group
// with financial tools enabled so the dev workflow keeps the full tool set
// while still exercising the per-message BuildTools path.
func (a *Agent) loadGroupContext(ctx context.Context, groupID string) (*db.Group, []db.Member) {
	if a.groups == nil {
		return devFallbackGroup(groupID), nil
	}
	group, err := a.groups.Get(ctx, groupID)
	if err != nil || group == nil {
		return devFallbackGroup(groupID), nil
	}
	var members []db.Member
	if a.members != nil {
		members, _ = a.members.List(ctx, groupID)
	}
	return group, members
}

// devFallbackGroup synthesizes a Group for dev terminal mode (no row in DB).
// FinancialEnabled is true so the dev workflow gets the full tool surface;
// language defaults to en for English-only persona text.
func devFallbackGroup(groupID string) *db.Group {
	return &db.Group{
		ID:               groupID,
		Language:         "en",
		FinancialEnabled: true,
	}
}

func LoadPersonas() string {
	// Try personas file first, fall back to env var
	path := os.Getenv("PERSONAS_FILE")
	if path == "" {
		path = "personas.md"
	}
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		return string(data)
	}
	if members := os.Getenv("FAMILY_MEMBERS"); members != "" {
		return members
	}
	return "Not specified — identify family members from conversation context."
}

// parseCardOwners parses CARD_OWNERS into a name -> []CardRef map.
//
// Format: "Owner1:provider/last4,provider/last4;Owner2:provider/last4"
// Example: "Alice:max/1234,max/5678,cal/9999;Bob:max/0000"
//
// Whitespace around separators is tolerated. Provider is lower-cased.
// Entries without a valid "provider/last4" pair are silently skipped.
func parseCardOwners(raw string) map[string][]db.CardRef {
	out := make(map[string][]db.CardRef)
	if raw == "" {
		return out
	}
	for _, ownerBlock := range strings.Split(raw, ";") {
		ownerBlock = strings.TrimSpace(ownerBlock)
		if ownerBlock == "" {
			continue
		}
		colon := strings.Index(ownerBlock, ":")
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(ownerBlock[:colon])
		if name == "" {
			continue
		}
		for _, entry := range strings.Split(ownerBlock[colon+1:], ",") {
			entry = strings.TrimSpace(entry)
			slash := strings.Index(entry, "/")
			if slash < 0 {
				continue
			}
			provider := strings.ToLower(strings.TrimSpace(entry[:slash]))
			last4 := strings.TrimSpace(entry[slash+1:])
			if provider == "" || last4 == "" {
				continue
			}
			if len(last4) > 4 {
				last4 = last4[len(last4)-4:]
			}
			out[name] = append(out[name], db.CardRef{Provider: provider, CardLast4: last4})
		}
	}
	return out
}

// loadCardOwners reads and parses the CARD_OWNERS environment variable.
func loadCardOwners() map[string][]db.CardRef {
	return parseCardOwners(os.Getenv("CARD_OWNERS"))
}

// ParsePersonaPhones extracts name->phone mappings from personas markdown.
// Expects "## Name" headers followed by "- **Phone:** 972..." lines.
func ParsePersonaPhones(personas string) map[string]string {
	phones := make(map[string]string)
	nameRe := regexp.MustCompile(`(?m)^## (.+)`)
	phoneRe := regexp.MustCompile(`(?i)\*\*Phone:\*\*\s*(\d+)`)

	names := nameRe.FindAllStringSubmatchIndex(personas, -1)
	for i, match := range names {
		name := personas[match[2]:match[3]]
		end := len(personas)
		if i+1 < len(names) {
			end = names[i+1][0]
		}
		section := personas[match[0]:end]
		if pm := phoneRe.FindStringSubmatch(section); len(pm) > 1 {
			phones[name] = pm[1]
		}
	}
	return phones
}

// DashboardLinker generates a pre-authenticated dashboard login URL for a phone.
// Implemented by the api.AuthHandler (which holds the OTP store).
type DashboardLinker interface {
	GenerateMagicLink(phone string) (string, error)
}

type Agent struct {
	client  anthropic.Client
	store   *db.TaskStore
	txStore *db.TxStore     // optional: enables expense tools when non-nil
	groups  *db.GroupStore  // per-message group lookup for prompt + tool gating
	members *db.MemberStore // per-message member list for prompt context
	linker  DashboardLinker // optional: enables dashboard_link tool when non-nil
	history *History        // per-(group, agent_kind) conversation windows (D5)
}

// NewAgent constructs the main task/expense agent. Conversation history,
// system prompt, and tool surface are all rebuilt per inbound message from
// the supplied group_id (D8, D10).
func NewAgent(store *db.TaskStore, txStore *db.TxStore, groups *db.GroupStore, members *db.MemberStore, history *History) *Agent {
	return &Agent{
		client:  anthropic.NewClient(),
		store:   store,
		txStore: txStore,
		groups:  groups,
		members: members,
		history: history,
	}
}

// SetDashboardLinker enables the dashboard_link tool.
// Call after NewAgent but before HandleMessage starts running.
func (a *Agent) SetDashboardLinker(linker DashboardLinker) {
	a.linker = linker
}

// maxHistoryMessages bounds the conversation window sent to Claude. Older
// messages are dropped (respecting tool_use/tool_result pairing — see
// trimHistory). Applied per (group, agent_kind) key in History.
const maxHistoryMessages = 20

// HandleMessage runs one user-turn through Claude for the given group and
// returns the assistant's text reply. The conversation window is keyed by
// (group_id, "main") in the shared History — no per-instance group state.
//
// Used directly by the digest scheduler (which wants the text to format and
// send itself). The dispatcher wraps this in Handle() for the I/O-driven
// per-message path.
func (a *Agent) HandleMessage(ctx context.Context, groupID, sender, text string) (string, error) {
	group, members := a.loadGroupContext(ctx, groupID)
	tools := BuildTools(ctx, group, KindMain)
	systemPrompt := buildSystemPrompt(group, members)

	key := historyKey{GroupID: groupID, AgentKind: KindMain}
	userContent := fmt.Sprintf("[%s]: %s", sender, text)
	window := a.history.Append(key, anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)))

	for {
		message, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 1024,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  window,
			Tools:     tools,
		})
		if err != nil {
			return "", fmt.Errorf("claude api: %w", err)
		}

		window = a.history.Append(key, message.ToParam())

		var textParts []string
		var toolResults []anthropic.ContentBlockParamUnion

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				textParts = append(textParts, variant.Text)
			case anthropic.ToolUseBlock:
				result, toolErr := a.ExecuteTool(groupID, variant.Name, []byte(variant.JSON.Input.Raw()))
				if toolErr != nil {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(variant.ID, toolErr.Error(), true))
				} else {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(variant.ID, result, false))
				}
			}
		}

		if len(toolResults) == 0 {
			return strings.Join(textParts, "\n"), nil
		}

		window = a.history.Append(key, anthropic.NewUserMessage(toolResults...))
	}
}

// resolveMentionsWithPhones scans text for @Name patterns, resolves them using
// the provided name->phone map, and returns the modified text and mention list.
func resolveMentionsWithPhones(text string, phones map[string]string) (string, []messenger.Mention) {
	seen := make(map[string]bool)
	var mentions []messenger.Mention
	for name, phone := range phones {
		if strings.Contains(text, "@"+name) && !seen[name] {
			text = strings.ReplaceAll(text, "@"+name, "@"+phone)
			mentions = append(mentions, messenger.Mention{Phone: phone, Name: name})
			seen[name] = true
		}
	}
	return text, mentions
}

// ResolveMentions scans text for @Name patterns, matches them against persona
// phone numbers, and returns the modified text (with @phone) and mention list.
func ResolveMentions(text string) (string, []messenger.Mention) {
	return resolveMentionsWithPhones(text, ParsePersonaPhones(LoadPersonas()))
}

func (a *Agent) ExecuteTool(groupID, name string, inputJSON []byte) (string, error) {
	switch name {
	case "add_task":
		var input struct {
			Content  string `json:"content"`
			Assignee string `json:"assignee"`
			DueDate  string `json:"due_date"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		task, err := a.store.AddTask(groupID, input.Content, input.Assignee, input.DueDate)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(task)
		return string(b), nil

	case "list_tasks":
		var input struct {
			Assignee string `json:"assignee"`
			Status   string `json:"status"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		tasks, err := a.store.ListTasks(groupID, input.Assignee, input.Status)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(tasks)
		return string(b), nil

	case "update_task":
		var input struct {
			ID       int64  `json:"id"`
			Status   string `json:"status"`
			DueDate  string `json:"due_date"`
			Content  string `json:"content"`
			Assignee string `json:"assignee"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		fields := db.TaskUpdate{
			Status:   input.Status,
			DueDate:  input.DueDate,
			Content:  input.Content,
			Assignee: input.Assignee,
		}
		if fields.IsEmpty() {
			return "", fmt.Errorf("no fields provided — pass at least one of: status, due_date, content, assignee")
		}
		// Validate assignee against the group's member roster (display names).
		if fields.Assignee != "" && a.members != nil {
			ms, mErr := a.members.List(context.Background(), groupID)
			if mErr == nil && len(ms) > 0 {
				ok := false
				var known []string
				for _, m := range ms {
					if m.DisplayName != "" {
						known = append(known, m.DisplayName)
					}
					if m.DisplayName == fields.Assignee {
						ok = true
					}
				}
				if !ok {
					return "", fmt.Errorf("unknown assignee %q — current members: %s", fields.Assignee, strings.Join(known, ", "))
				}
			}
		}
		task, err := a.store.UpdateTask(groupID, input.ID, fields)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(task)
		return string(b), nil

	case "delete_task":
		var input struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		if err := a.store.DeleteTask(groupID, input.ID); err != nil {
			return "", err
		}
		return `{"deleted": true}`, nil

	case "expenses_summary":
		if a.txStore == nil {
			return "", fmt.Errorf("expenses are not configured on this deployment")
		}
		var input struct {
			GroupBy          string `json:"group_by"`
			Since            string `json:"since"`
			Until            string `json:"until"`
			Provider         string `json:"provider"`
			Category         string `json:"category"`
			MerchantContains string `json:"merchant_contains"`
			CardLast4        string `json:"card_last4"`
			Owner            string `json:"owner"`
			Limit            *int   `json:"limit"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		since, until := defaultBillingCycleRange(input.Since, input.Until)
		limit := 20
		if input.Limit != nil {
			limit = *input.Limit
		}

		// Resolve optional owner filter to a list of cards via personas.md.
		ownerCards := loadCardOwners()
		var filterCards []db.CardRef
		if input.Owner != "" {
			cards, ok := ownerCards[input.Owner]
			if !ok || len(cards) == 0 {
				return "", fmt.Errorf("unknown owner %q — personas.md has: %s", input.Owner, strings.Join(sortedKeys(ownerCards), ", "))
			}
			filterCards = cards
		}

		baseFilter := db.TxFilter{
			Since:            since,
			Until:            until,
			Provider:         input.Provider,
			CardLast4:        input.CardLast4,
			Category:         input.Category,
			MerchantContains: input.MerchantContains,
			DebitsOnly:       false,
			Cards:            filterCards,
			Limit:            limit,
		}

		var rows []db.SumRow
		if input.GroupBy == "owner" {
			// Aggregate per owner by calling TotalSpent with each owner's cards.
			for _, name := range sortedKeys(ownerCards) {
				cards := ownerCards[name]
				f := baseFilter
				// Owner groupby overrides any single Cards filter; but if the user
				// ALSO passed owner=X, intersect: only that one owner appears.
				if input.Owner != "" && input.Owner != name {
					continue
				}
				f.Cards = cards
				f.Limit = 0
				total, err := a.txStore.TotalSpent(groupID, f)
				if err != nil {
					return "", err
				}
				total.Key = name
				rows = append(rows, total)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].SpentILS > rows[j].SpentILS })
			if limit > 0 && len(rows) > limit {
				rows = rows[:limit]
			}
		} else {
			var err error
			rows, err = a.txStore.SumBy(groupID, input.GroupBy, baseFilter)
			if err != nil {
				return "", err
			}
		}

		// Always include the overall total so the LLM doesn't need to sum rows
		// (which may be truncated by the limit).
		total, _ := a.txStore.TotalSpent(groupID, db.TxFilter{
			Since:    since,
			Until:    until,
			Provider: input.Provider,
			Cards:    filterCards,
		})

		resp := map[string]any{
			"group_by":           input.GroupBy,
			"since":              since,
			"until":              until,
			"rows":               rows,
			"total_spent_ils":    total.SpentILS,
			"total_charges_ils":  total.ChargesILS,
			"total_refunds_ils":  total.RefundsILS,
			"total_tx_count":     total.TxCount,
		}
		b, _ := json.Marshal(resp)
		return string(b), nil

	case "list_transactions":
		if a.txStore == nil {
			return "", fmt.Errorf("expenses are not configured on this deployment")
		}
		var input struct {
			Since            string `json:"since"`
			Until            string `json:"until"`
			Provider         string `json:"provider"`
			Category         string `json:"category"`
			MerchantContains string `json:"merchant_contains"`
			CardLast4        string `json:"card_last4"`
			Owner            string `json:"owner"`
			DebitsOnly       bool   `json:"debits_only"`
			CreditsOnly      bool   `json:"credits_only"`
			SortBy           string `json:"sort_by"`
			Limit            *int   `json:"limit"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		// Default to current billing cycle if no dates provided (same as expenses_summary).
		since, until := defaultBillingCycleRange(input.Since, input.Until)
		input.Since = since
		input.Until = until

		limit := 50
		if input.Limit != nil {
			limit = *input.Limit
		}
		var filterCards []db.CardRef
		if input.Owner != "" {
			ownerCards := loadCardOwners()
			cards, ok := ownerCards[input.Owner]
			if !ok || len(cards) == 0 {
				return "", fmt.Errorf("unknown owner %q — personas.md has: %s", input.Owner, strings.Join(sortedKeys(ownerCards), ", "))
			}
			filterCards = cards
		}
		txs, err := a.txStore.QueryTransactions(groupID, db.TxFilter{
			Since:            since,
			Until:            until,
			Provider:         input.Provider,
			CardLast4:        input.CardLast4,
			Category:         input.Category,
			MerchantContains: input.MerchantContains,
			DebitsOnly:       input.DebitsOnly,
			CreditsOnly:      input.CreditsOnly,
			SortBy:           input.SortBy,
			Cards:            filterCards,
			Limit:            limit,
		})
		if err != nil {
			return "", err
		}
		// Strip raw_json to keep the tool-result payload small.
		for i := range txs {
			txs[i].RawJSON = ""
		}
		b, _ := json.Marshal(txs)
		return string(b), nil

	case "dashboard_link":
		if a.linker == nil {
			return "", fmt.Errorf("dashboard link not configured on this deployment")
		}
		var input struct {
			ForUser string `json:"for_user"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		if input.ForUser == "" {
			return "", fmt.Errorf("for_user is required")
		}

		// Resolve name → phone via personas.
		phones := ParsePersonaPhones(LoadPersonas())
		phone, ok := phones[input.ForUser]
		if !ok {
			return "", fmt.Errorf("unknown user %q — personas.md has: %s",
				input.ForUser, strings.Join(sortedKeys(phones), ", "))
		}

		url, err := a.linker.GenerateMagicLink(phone)
		if err != nil {
			return "", fmt.Errorf("generate link: %w", err)
		}
		b, _ := json.Marshal(map[string]any{
			"url":        url,
			"expires_in": 300,
			"user":       input.ForUser,
		})
		return string(b), nil

	case "get_group_settings":
		return a.toolGetGroupSettings(groupID)
	case "update_group_settings":
		return a.toolUpdateGroupSettings(groupID, inputJSON)
	case "add_member":
		return a.toolAddMember(groupID, inputJSON)
	case "update_member":
		return a.toolUpdateMember(groupID, inputJSON)
	case "remove_member":
		return a.toolRemoveMember(groupID, inputJSON)

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// toolGetGroupSettings returns the user-visible group config + member list.
// Operator-only fields (financial_enabled, onboarding_state, last_active_at)
// are intentionally omitted (NFR1 — no operator back-door through the agent).
func (a *Agent) toolGetGroupSettings(groupID string) (string, error) {
	ctx := context.Background()
	if a.groups == nil {
		return "", fmt.Errorf("group settings not available in this deployment")
	}
	group, err := a.groups.Get(ctx, groupID)
	if err != nil {
		return "", err
	}
	if group == nil {
		return "", fmt.Errorf("no group settings for %s", groupID)
	}
	var members []db.Member
	if a.members != nil {
		members, _ = a.members.List(ctx, groupID)
	}
	type memberOut struct {
		Name       string `json:"display_name"`
		WhatsAppID string `json:"whatsapp_id"`
	}
	out := struct {
		Name       string      `json:"name"`
		Language   string      `json:"language"`
		Timezone   string      `json:"timezone"`
		DigestHour any         `json:"digest_hour"`
		Members    []memberOut `json:"members"`
	}{
		Name:     group.Name,
		Language: group.Language,
		Timezone: group.Timezone,
	}
	if group.DigestHourSet {
		out.DigestHour = group.DigestHour
	}
	for _, m := range members {
		out.Members = append(out.Members, memberOut{Name: m.DisplayName, WhatsAppID: m.WhatsAppID})
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (a *Agent) toolUpdateGroupSettings(groupID string, inputJSON []byte) (string, error) {
	var input struct {
		Name       *string `json:"name"`
		Timezone   *string `json:"timezone"`
		DigestHour *int    `json:"digest_hour"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if input.Name == nil && input.Timezone == nil && input.DigestHour == nil {
		return "", fmt.Errorf("no fields provided — pass at least one of: name, timezone, digest_hour")
	}
	ctx := context.Background()
	if input.Timezone != nil {
		if _, err := time.LoadLocation(*input.Timezone); err != nil {
			return "", fmt.Errorf("invalid IANA timezone %q", *input.Timezone)
		}
		if err := a.groups.SetTimezone(ctx, groupID, *input.Timezone); err != nil {
			return "", err
		}
	}
	if input.DigestHour != nil {
		if *input.DigestHour < 0 || *input.DigestHour > 23 {
			return "", fmt.Errorf("digest_hour must be 0..23, got %d", *input.DigestHour)
		}
		if err := a.groups.SetDigestHour(ctx, groupID, *input.DigestHour); err != nil {
			return "", err
		}
	}
	if input.Name != nil {
		if err := a.groups.SetName(ctx, groupID, *input.Name); err != nil {
			return "", err
		}
	}
	return a.toolGetGroupSettings(groupID)
}

func (a *Agent) toolAddMember(groupID string, inputJSON []byte) (string, error) {
	var input struct {
		Name       string `json:"name"`
		WhatsAppID string `json:"whatsapp_id"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.WhatsAppID = strings.TrimSpace(input.WhatsAppID)
	if input.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if !phoneRe.MatchString(input.WhatsAppID) {
		return "", fmt.Errorf("whatsapp_id must be digits only in international format (no `+`), got %q", input.WhatsAppID)
	}
	ctx := context.Background()
	current, err := a.members.List(ctx, groupID)
	if err != nil {
		return "", err
	}
	if len(current) >= db.MemberCap {
		return "", fmt.Errorf("group already has %d members; v1 supports up to %d per group", len(current), db.MemberCap)
	}
	if err := a.members.Add(ctx, groupID, db.Member{
		GroupID: groupID, WhatsAppID: input.WhatsAppID, DisplayName: input.Name,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"name": %q, "whatsapp_id": %q, "members_count": %d}`,
		input.Name, input.WhatsAppID, len(current)+1), nil
}

func (a *Agent) toolUpdateMember(groupID string, inputJSON []byte) (string, error) {
	var input struct {
		WhatsAppID  string `json:"whatsapp_id"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	input.WhatsAppID = strings.TrimSpace(input.WhatsAppID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.WhatsAppID == "" || input.DisplayName == "" {
		return "", fmt.Errorf("both whatsapp_id and display_name are required")
	}
	ctx := context.Background()
	current, err := a.members.List(ctx, groupID)
	if err != nil {
		return "", err
	}
	var oldName string
	for _, m := range current {
		if m.WhatsAppID == input.WhatsAppID {
			oldName = m.DisplayName
			break
		}
	}
	if oldName == "" && !memberExists(current, input.WhatsAppID) {
		return "", fmt.Errorf("member %s not found in this group", input.WhatsAppID)
	}
	if err := a.members.UpdateName(ctx, groupID, input.WhatsAppID, input.DisplayName); err != nil {
		return "", err
	}
	// Cascade: rename pending tasks assigned to the old display_name. Done
	// tasks keep history. Skip if oldName is empty (was previously unnamed).
	var reassigned int64
	if oldName != "" && oldName != input.DisplayName {
		reassigned, _ = a.store.ReassignPending(groupID, oldName, input.DisplayName)
	}
	return fmt.Sprintf(`{"whatsapp_id": %q, "display_name": %q, "tasks_reassigned": %d}`,
		input.WhatsAppID, input.DisplayName, reassigned), nil
}

func (a *Agent) toolRemoveMember(groupID string, inputJSON []byte) (string, error) {
	var input struct {
		WhatsAppID string `json:"whatsapp_id"`
	}
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	input.WhatsAppID = strings.TrimSpace(input.WhatsAppID)
	if input.WhatsAppID == "" {
		return "", fmt.Errorf("whatsapp_id is required")
	}
	ctx := context.Background()
	current, err := a.members.List(ctx, groupID)
	if err != nil {
		return "", err
	}
	var (
		targetName    string
		remainingName string
		found         bool
	)
	for _, m := range current {
		if m.WhatsAppID == input.WhatsAppID {
			found = true
			targetName = m.DisplayName
		} else {
			// Keep the first non-target name as the reassign destination.
			if remainingName == "" {
				remainingName = m.DisplayName
			}
		}
	}
	if !found {
		return "", fmt.Errorf("member %s not found in this group", input.WhatsAppID)
	}
	if len(current) <= 1 {
		return "", fmt.Errorf("cannot remove the only member — at least one must remain")
	}
	// Reassign open tasks first, then remove. Worst case (reassign succeeds,
	// remove fails) leaves an inconsistent but recoverable state.
	var reassigned int64
	if targetName != "" && remainingName != "" {
		reassigned, _ = a.store.ReassignPending(groupID, targetName, remainingName)
	}
	if err := a.members.Remove(ctx, groupID, input.WhatsAppID); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"removed": %q, "tasks_reassigned_to": %q, "tasks_reassigned": %d}`,
		input.WhatsAppID, remainingName, reassigned), nil
}

func memberExists(members []db.Member, whatsappID string) bool {
	for _, m := range members {
		if m.WhatsAppID == whatsappID {
			return true
		}
	}
	return false
}

// defaultBillingCycleRange returns the caller's since/until if both are
// provided. Otherwise it computes the current billing cycle using BILLING_DAY
// (default 10). A cycle runs from BILLING_DAY of one month through the day
// before BILLING_DAY of the next month, inclusive on both ends.
//
// Example with BILLING_DAY=10, today=2026-04-05: cycle is 2026-03-10 → 2026-04-09.
// Example with BILLING_DAY=10, today=2026-04-15: cycle is 2026-04-10 → 2026-05-09.
func defaultBillingCycleRange(since, until string) (string, string) {
	if since != "" && until != "" {
		return since, until
	}
	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Asia/Jerusalem"
	}
	loc, _ := time.LoadLocation(tz)
	now := time.Now().In(loc)

	billingDay := 10
	if v := os.Getenv("BILLING_DAY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 28 {
			billingDay = n
		}
	}

	// Find the cycle START: most recent (today-or-past) occurrence of billingDay.
	var cycleStart time.Time
	if now.Day() >= billingDay {
		cycleStart = time.Date(now.Year(), now.Month(), billingDay, 0, 0, 0, 0, loc)
	} else {
		cycleStart = time.Date(now.Year(), now.Month()-1, billingDay, 0, 0, 0, 0, loc)
	}
	// Cycle END is day before next billingDay (inclusive).
	cycleEnd := cycleStart.AddDate(0, 1, -1)

	if since == "" {
		since = cycleStart.Format("2006-01-02")
	}
	if until == "" {
		until = cycleEnd.Format("2006-01-02")
	}
	return since, until
}

// trimHistory returns the most recent messages up to maxN, adjusted so the
// first retained message is a valid conversation start:
//
//  1. Claude's API requires messages to begin with a user message — any leading
//     assistant message after trimming would cause a 400.
//  2. A tool_result block inside a user message must have its matching tool_use
//     block present (in the preceding assistant message). If we cut in the
//     middle of a tool call round, we'd strand a tool_result.
//
// The algorithm: take the tail [len-maxN:], then walk forward dropping any
// leading message that is either an assistant message or a user message
// containing tool_result blocks, until we find a plain user text message.
func trimHistory(history []anthropic.MessageParam, maxN int) []anthropic.MessageParam {
	if len(history) <= maxN {
		return history
	}
	start := len(history) - maxN
	for start < len(history) {
		m := history[start]
		if m.Role == anthropic.MessageParamRoleUser && !messageHasToolResult(m) {
			break
		}
		start++
	}
	return history[start:]
}

// messageHasToolResult reports whether any content block in m is a tool_result.
func messageHasToolResult(m anthropic.MessageParam) bool {
	for _, block := range m.Content {
		if block.OfToolResult != nil {
			return true
		}
	}
	return false
}

// sortedKeys returns the keys of a map in stable sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
