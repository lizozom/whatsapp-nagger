package agent

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// BuildTools returns the per-message tool surface for the given group + agent
// kind. Invoked once per inbound message — no caching, no global registry
// (architecture D10). Capability-gated: financial tools appear in the schema
// only when group.FinancialEnabled is true (NFR1 — physically absent for
// non-financial groups, not "present and refuses").
//
// group_id never appears in any tool's input schema (D8) — handlers in the
// agent close over it from the inbound envelope.
func BuildTools(ctx context.Context, group *db.Group, kind AgentKind) []anthropic.ToolUnionParam {
	switch kind {
	case KindOnboarding:
		return buildOnboardingTools()
	case KindMain:
		return buildMainTools(group)
	default:
		return nil
	}
}

func buildMainTools(group *db.Group) []anthropic.ToolUnionParam {
	tools := []anthropic.ToolUnionParam{
		toolAddTask, toolListTasks, toolUpdateTask, toolDeleteTask, toolDashboardLink,
		toolGetGroupSettings, toolUpdateGroupSettings,
		toolAddMember, toolUpdateMember, toolRemoveMember,
	}
	if group != nil && group.FinancialEnabled {
		tools = append(tools, toolExpensesSummary, toolListTransactions)
	}
	return tools
}

// Tool definitions — package-level so BuildTools can reference them without
// allocating on every inbound message.

var toolAddTask = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name:        "add_task",
	Description: anthropic.String("Add a new task to the family backlog."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"content":  map[string]any{"type": "string", "description": "What needs to be done"},
			"assignee": map[string]any{"type": "string", "description": "Who should do it (Alice, Bob)"},
			"due_date": map[string]any{"type": "string", "description": "Optional due date (YYYY-MM-DD)"},
		},
	},
}}

var toolListTasks = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name:        "list_tasks",
	Description: anthropic.String("List tasks, optionally filtered by assignee or status."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"assignee": map[string]any{"type": "string", "description": "Filter by assignee (optional)"},
			"status":   map[string]any{"type": "string", "description": "Filter by status: pending or done (optional)"},
		},
	},
}}

var toolUpdateTask = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "update_task",
	Description: anthropic.String(
		"Update one or more fields of an existing task. At least one of " +
			"`status`, `due_date`, `content`, or `assignee` must be provided. " +
			"Omitted fields are left unchanged. " +
			"Use this for typo fixes (`content`), reassignment (`assignee` — must " +
			"match a current group member's display name), rescheduling (`due_date`), " +
			"or marking done (`status`).",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"id":       map[string]any{"type": "integer", "description": "Task ID"},
			"status":   map[string]any{"type": "string", "description": "New status: 'pending' or 'done'. Optional."},
			"due_date": map[string]any{"type": "string", "description": "New due date YYYY-MM-DD. Optional."},
			"content":  map[string]any{"type": "string", "description": "New task description. Use this to fix typos or clarify the task. Optional."},
			"assignee": map[string]any{"type": "string", "description": "Reassign to a different group member. Must match a current member's display name exactly. Optional."},
		},
		Required: []string{"id"},
	},
}}

var toolDeleteTask = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name:        "delete_task",
	Description: anthropic.String("Delete a task from the backlog."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"id": map[string]any{"type": "integer", "description": "Task ID to delete"},
		},
	},
}}

var toolExpensesSummary = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "expenses_summary",
	Description: anthropic.String(
		"Aggregate credit card / bank expenses grouped by a dimension. " +
			"Use for questions like 'how much did we spend this month', " +
			"'top categories in February', 'top merchants this year', " +
			"'how much did Alice spend vs Bob'. " +
			"Amounts are in ILS. The response always includes total_spent_ils (the grand total " +
			"across ALL rows, unaffected by the limit) — ALWAYS report this as the headline number. " +
			"Each row's spent_ils is the NET outflow for that group. " +
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
}}

var toolListTransactions = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
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
			"limit": map[string]any{"type": "integer", "description": "Max rows to return (default 50)."},
		},
	},
}}

var toolGetGroupSettings = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "get_group_settings",
	Description: anthropic.String(
		"Show the current group's user-facing settings: name, language, timezone, " +
			"digest_hour, and the member list (display names + WhatsApp IDs). " +
			"Use when someone asks 'what are our settings', 'who's in this group', etc. " +
			"Operator-only fields (financial_enabled, onboarding_state, last_active_at) are intentionally omitted.",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{},
	},
}}

var toolUpdateGroupSettings = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "update_group_settings",
	Description: anthropic.String(
		"Update one or more of the group's user-editable settings: name, timezone, digest_hour. " +
			"At least one field must be provided. Language CANNOT be changed here — it's locked at onboarding.",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"name":        map[string]any{"type": "string", "description": "New display name for the group. Optional."},
			"timezone":    map[string]any{"type": "string", "description": "New IANA timezone (e.g. 'Asia/Jerusalem'). Optional."},
			"digest_hour": map[string]any{"type": "integer", "description": "New digest hour 0-23 in the group's timezone. Optional."},
		},
	},
}}

var toolAddMember = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "add_member",
	Description: anthropic.String(
		"Add a new member to the group. Refused if the group already has 2 members (v1 cap). " +
			"Use this for 'add my partner', 'invite Bob', etc. — but NOT during onboarding (use set_member there).",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"name":        map[string]any{"type": "string", "description": "Display name (e.g. 'Alice')."},
			"whatsapp_id": map[string]any{"type": "string", "description": "WhatsApp phone in international format with no '+' (e.g. '972501234567')."},
		},
		Required: []string{"name", "whatsapp_id"},
	},
}}

var toolUpdateMember = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "update_member",
	Description: anthropic.String(
		"Rename an existing member. Cascades to tasks: any pending task assigned to the old name " +
			"will be reassigned to the new name. Done tasks keep their original assignee (historical record).",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"whatsapp_id":  map[string]any{"type": "string", "description": "Target member's WhatsApp ID (international format, no '+')."},
			"display_name": map[string]any{"type": "string", "description": "New display name."},
		},
		Required: []string{"whatsapp_id", "display_name"},
	},
}}

var toolRemoveMember = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "remove_member",
	Description: anthropic.String(
		"Remove a member from the group. Refused if removing would leave the group with zero members. " +
			"Open (pending) tasks assigned to the removed member are auto-reassigned to the remaining member. " +
			"Done tasks keep their original assignee value.",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"whatsapp_id": map[string]any{"type": "string", "description": "Target member's WhatsApp ID (international format, no '+')."},
		},
		Required: []string{"whatsapp_id"},
	},
}}

var toolDashboardLink = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
	Name: "dashboard_link",
	Description: anthropic.String(
		"Generate a one-tap magic link to the web dashboard for the requesting user. " +
			"The link includes a pre-generated OTP code so the user doesn't have to enter it. " +
			"Use this when someone asks for 'the dashboard', 'the link', 'give me the dashboard', etc. " +
			"The phone is resolved from the [Sender] prefix of the current message via personas.md.",
	),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"for_user": map[string]any{
				"type":        "string",
				"description": "Family member name (must match personas.md). Usually the requesting user.",
			},
		},
		Required: []string{"for_user"},
	},
}}
