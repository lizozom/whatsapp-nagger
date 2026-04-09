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

func buildSystemPrompt() string {
	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Asia/Jerusalem"
	}
	loc, _ := time.LoadLocation(tz)
	now := time.Now().In(loc)

	members := loadPersonas()
	merchantCtx := os.Getenv("MERCHANT_CONTEXT")
	if merchantCtx == "" {
		merchantCtx = "(none configured)"
	}

	return fmt.Sprintf(systemPromptTemplate,
		version.Version,
		version.DeployDate,
		now.Format("2006-01-02 15:04 MST"),
		now.Weekday().String(),
		tz,
		members,
		merchantCtx,
	)
}

func loadPersonas() string {
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

// parsePersonaPhones extracts name->phone mappings from personas markdown.
// Expects "## Name" headers followed by "- **Phone:** 972..." lines.
func parsePersonaPhones(personas string) map[string]string {
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

type Agent struct {
	client  anthropic.Client
	store   *db.TaskStore
	txStore *db.TxStore // optional: enables expense tools when non-nil
	history []anthropic.MessageParam
	tools   []anthropic.ToolUnionParam
}

// NewAgent constructs an agent with task and (optional) transaction stores.
// If txStore is nil, the expense tools are still registered but return an
// "expenses not configured" error — this lets the system prompt stay constant
// regardless of deployment configuration.
func NewAgent(store *db.TaskStore, txStore *db.TxStore) *Agent {
	client := anthropic.NewClient()

	tools := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "add_task",
			Description: anthropic.String("Add a new task to the family backlog."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"content":  map[string]any{"type": "string", "description": "What needs to be done"},
					"assignee": map[string]any{"type": "string", "description": "Who should do it (Alice, Bob)"},
					"due_date": map[string]any{"type": "string", "description": "Optional due date (YYYY-MM-DD)"},
				},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "list_tasks",
			Description: anthropic.String("List tasks, optionally filtered by assignee or status."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"assignee": map[string]any{"type": "string", "description": "Filter by assignee (optional)"},
					"status":   map[string]any{"type": "string", "description": "Filter by status: pending or done (optional)"},
				},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "update_task",
			Description: anthropic.String("Update a task's status and/or due date."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"id":       map[string]any{"type": "integer", "description": "Task ID"},
					"status":   map[string]any{"type": "string", "description": "New status: pending or done (optional)"},
					"due_date": map[string]any{"type": "string", "description": "New due date YYYY-MM-DD (optional)"},
				},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "delete_task",
			Description: anthropic.String("Delete a task from the backlog."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"id": map[string]any{"type": "integer", "description": "Task ID to delete"},
				},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name: "expenses_summary",
			Description: anthropic.String(
				"Aggregate credit card / bank expenses grouped by a dimension. " +
					"Use for questions like 'how much did we spend this month', " +
					"'top categories in February', 'top merchants this year', " +
					"'how much did Alice spend vs Bob'. " +
					"Amounts are in ILS. spent_ils is the NET outflow (charges minus refunds) — " +
					"report THIS when the user asks 'how much did we spend'. " +
					"charges_ils is gross debits and refunds_ils is gross credits, for transparency only. " +
					"Default date range is the CURRENT BILLING CYCLE if since/until are omitted " +
					"(cycles run from BILLING_DAY of one month through the day before BILLING_DAY of the next).",
			),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"group_by": map[string]any{
						"type":        "string",
						"enum":        []string{"category", "merchant", "month", "provider", "card_last4", "owner"},
						"description": "Dimension to group by. 'owner' aggregates across each family member's cards (defined in personas.md).",
					},
					"since":             map[string]any{"type": "string", "description": "Start date (YYYY-MM-DD), inclusive. Optional."},
					"until":             map[string]any{"type": "string", "description": "End date (YYYY-MM-DD), inclusive. Optional."},
					"provider":          map[string]any{"type": "string", "description": "Filter by provider: 'cal' or 'max'. Optional."},
					"category":          map[string]any{"type": "string", "description": "Exact category filter (e.g. 'מזון וצריכה'). Optional."},
					"merchant_contains": map[string]any{"type": "string", "description": "Case-insensitive substring match on merchant description. Optional."},
					"card_last4":        map[string]any{"type": "string", "description": "Filter to a single card by last 4 digits. Optional."},
					"owner": map[string]any{
						"type":        "string",
						"description": "Filter to a single family member's cards (name as in personas.md, e.g. 'Alice' or 'Bob'). Optional.",
					},
					"limit": map[string]any{"type": "integer", "description": "Max groups to return (default 20, use 0 for unlimited)."},
				},
				Required: []string{"group_by"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name: "list_transactions",
			Description: anthropic.String(
				"List individual credit card / bank transactions matching a filter. " +
					"Use for drilling into specific charges (e.g. 'what did we buy at Shufersal in Feb'). " +
					"Amounts are in ILS, negative for debits. " +
					"If since/until are omitted, defaults to the current billing cycle (same as expenses_summary). " +
					"Always set a limit — this can return hundreds of rows otherwise.",
			),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"since":             map[string]any{"type": "string", "description": "Start date (YYYY-MM-DD). Optional."},
					"until":             map[string]any{"type": "string", "description": "End date (YYYY-MM-DD). Optional."},
					"provider":          map[string]any{"type": "string", "description": "'cal' or 'max'. Optional."},
					"category":          map[string]any{"type": "string", "description": "Exact category filter. Optional."},
					"merchant_contains": map[string]any{"type": "string", "description": "Substring match on description. Optional."},
					"card_last4":        map[string]any{"type": "string", "description": "Card last4. Optional."},
					"owner":             map[string]any{"type": "string", "description": "Family member name — filters to their cards (e.g. 'Alice', 'Bob'). Optional."},
					"debits_only":       map[string]any{"type": "boolean", "description": "If true, only debits (charges). Optional."},
					"credits_only":      map[string]any{"type": "boolean", "description": "If true, only credits (refunds). Use for 'show me refunds'. Optional."},
					"sort_by": map[string]any{
						"type":        "string",
						"enum":        []string{"date", "amount"},
						"description": "Sort order: 'date' (default, newest first) or 'amount' (largest absolute amount first). Use 'amount' with debits_only=true for 'top/biggest/highest charges'.",
					},
					"limit":             map[string]any{"type": "integer", "description": "Max rows to return (default 50)."},
				},
			},
		}},
	}

	return &Agent{
		client:  client,
		store:   store,
		txStore: txStore,
		tools:   tools,
	}
}

// maxHistoryMessages bounds the conversation window sent to Claude. Older
// messages are dropped (respecting tool_use/tool_result pairing — see
// trimHistory). This keeps token usage bounded on a long-running bot.
const maxHistoryMessages = 20

func (a *Agent) HandleMessage(sender, text string) (string, error) {
	userContent := fmt.Sprintf("[%s]: %s", sender, text)
	a.history = append(a.history, anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)))
	a.history = trimHistory(a.history, maxHistoryMessages)

	for {
		message, err := a.client.Messages.New(context.Background(), anthropic.MessageNewParams{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 1024,
			System:    []anthropic.TextBlockParam{{Text: buildSystemPrompt()}},
			Messages:  a.history,
			Tools:     a.tools,
		})
		if err != nil {
			return "", fmt.Errorf("claude api: %w", err)
		}

		a.history = append(a.history, message.ToParam())

		var textParts []string
		var toolResults []anthropic.ContentBlockParamUnion

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				textParts = append(textParts, variant.Text)
			case anthropic.ToolUseBlock:
				result, toolErr := a.ExecuteTool(variant.Name, []byte(variant.JSON.Input.Raw()))
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

		a.history = append(a.history, anthropic.NewUserMessage(toolResults...))
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
	return resolveMentionsWithPhones(text, parsePersonaPhones(loadPersonas()))
}

func (a *Agent) ExecuteTool(name string, inputJSON []byte) (string, error) {
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
		task, err := a.store.AddTask(input.Content, input.Assignee, input.DueDate)
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
		tasks, err := a.store.ListTasks(input.Assignee, input.Status)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(tasks)
		return string(b), nil

	case "update_task":
		var input struct {
			ID      int64  `json:"id"`
			Status  string `json:"status"`
			DueDate string `json:"due_date"`
		}
		if err := json.Unmarshal(inputJSON, &input); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		task, err := a.store.UpdateTask(input.ID, input.Status, input.DueDate)
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
		if err := a.store.DeleteTask(input.ID); err != nil {
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
				total, err := a.txStore.TotalSpent(f)
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
			rows, err = a.txStore.SumBy(input.GroupBy, baseFilter)
			if err != nil {
				return "", err
			}
		}

		resp := map[string]any{
			"group_by": input.GroupBy,
			"since":    since,
			"until":    until,
			"rows":     rows,
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
		txs, err := a.txStore.QueryTransactions(db.TxFilter{
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

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
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
