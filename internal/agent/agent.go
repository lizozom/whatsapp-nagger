package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
- Use @Name (e.g. @Liza, @Denis) so they get tagged.
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

	return fmt.Sprintf(systemPromptTemplate,
		version.Version,
		version.DeployDate,
		now.Format("2006-01-02 15:04 MST"),
		now.Weekday().String(),
		tz,
		members,
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
	history []anthropic.MessageParam
	tools   []anthropic.ToolUnionParam
}

func NewAgent(store *db.TaskStore) *Agent {
	client := anthropic.NewClient()

	tools := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "add_task",
			Description: anthropic.String("Add a new task to the family backlog."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"content":  map[string]any{"type": "string", "description": "What needs to be done"},
					"assignee": map[string]any{"type": "string", "description": "Who should do it (Liza, Denis)"},
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
		}

	return &Agent{
		client: client,
		store:  store,
		tools:  tools,
	}
}

func (a *Agent) HandleMessage(sender, text string) (string, error) {
	userContent := fmt.Sprintf("[%s]: %s", sender, text)
	a.history = append(a.history, anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)))

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

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}
