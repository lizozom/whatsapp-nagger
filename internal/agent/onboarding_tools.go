package agent

import "github.com/anthropics/anthropic-sdk-go"

// buildOnboardingTools returns the per-message tool surface for the
// onboarding agent. group_id is intentionally absent from every input
// schema — it's injected server-side from the inbound envelope (D8).
//
// Story 2.3 keeps these as a static slice; Story 2.4's BuildTools(group, kind)
// will replace this with a per-message capability-gated registry.
func buildOnboardingTools() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "set_language",
			Description: anthropic.String("Lock the group's reply language. Can only be called once — language is permanent after the first call."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"language": map[string]any{
						"type":        "string",
						"enum":        []string{"he", "en"},
						"description": "Language code: 'he' for Hebrew, 'en' for English.",
					},
				},
				Required: []string{"language"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "set_member",
			Description: anthropic.String("Add or update a member of the group. Up to 2 members per group (v1 limit). Idempotent on whatsapp_id — calling with an existing whatsapp_id updates the display name."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Display name (e.g. 'Alice', 'Bob').",
					},
					"whatsapp_id": map[string]any{
						"type":        "string",
						"description": "WhatsApp phone in international format with no '+' (e.g. '972501234567').",
					},
				},
				Required: []string{"name", "whatsapp_id"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "set_timezone",
			Description: anthropic.String("Set the group's IANA timezone (e.g. 'Asia/Jerusalem', 'Europe/Berlin'). Used for daily digest scheduling."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"timezone": map[string]any{
						"type":        "string",
						"description": "IANA timezone identifier.",
					},
				},
				Required: []string{"timezone"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "set_digest_hour",
			Description: anthropic.String("Set the hour of day (0-23) when the daily digest fires, in the group's timezone."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"hour": map[string]any{
						"type":        "integer",
						"description": "Hour 0..23 in the group's timezone.",
					},
				},
				Required: []string{"hour"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "complete_onboarding",
			Description: anthropic.String("Finalize onboarding once language, members (1-2), timezone, and digest hour are all set. Refuses with the missing-field list if any required field is unset."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{},
			},
		}},
	}
}
