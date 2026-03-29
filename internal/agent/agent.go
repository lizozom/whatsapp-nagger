package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/lizozom/whatsapp-nagger/internal/db"
)

const systemPromptTemplate = `You are whatsapp-nagger, a family task management bot in a WhatsApp group.
Your job is to ensure the family backlog is cleared.

Current date and time: %s
Day of week: %s
Timezone: %s

Family members:
%s

Rules:
- If someone mentions a task, use add_task to log it. Convert relative dates ("tomorrow", "this week", "next Sunday") to absolute dates (YYYY-MM-DD) based on the current date.
- If someone says a task is done, use update_task to mark it done.
- If asked about tasks, use list_tasks to check.
- Keep responses short and direct.
- Tone: no-nonsense. Pragmatic, direct, slightly sarcastic.
- Never say "As an AI" or "I am happy to help". Just do the work.
- If a task is rotting, nag the assignee with a dry remark.`

func buildSystemPrompt() string {
	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Asia/Jerusalem"
	}
	loc, _ := time.LoadLocation(tz)
	now := time.Now().In(loc)

	members := loadPersonas()

	return fmt.Sprintf(systemPromptTemplate,
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

func (a *Agent) GenerateDigest() (string, error) {
	tasks, err := a.store.ListTasks("", "pending")
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}

	if len(tasks) == 0 {
		return "Daily digest: zero pending tasks. Suspicious. Either you're all incredibly productive or nobody is logging anything. 🤔", nil
	}

	taskList, _ := json.Marshal(tasks)
	prompt := fmt.Sprintf(`Here are all pending tasks:\n%s\n\nWrite a daily digest / "Wall of Shame" for the family WhatsApp group. Address everyone. Be sarcastic about overdue or old tasks. Keep it short — a few lines max.`, string(taskList))

	message, err := a.client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 512,
		System:    []anthropic.TextBlockParam{{Text: buildSystemPrompt()}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	})
	if err != nil {
		return "", fmt.Errorf("claude api: %w", err)
	}

	var parts []string
	for _, block := range message.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			parts = append(parts, tb.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
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
