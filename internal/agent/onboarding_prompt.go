package agent

import (
	"fmt"
	"strings"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// buildOnboardingSystemPrompt renders the per-message system prompt for the
// onboarding agent. The prompt encodes the current onboarding state so the
// LLM resumes from the last unanswered question across process restarts
// (Story 2.3 AC: "system prompt computes 'next missing field' from the
// populated groups/members rows").
//
// Tone is warm/welcoming throughout (NFR4 — sarcastic Israeli-engineer is
// the main agent's persona, not onboarding's). Until language is set the
// prompt is bilingual; afterwards it locks to the chosen language.
func buildOnboardingSystemPrompt(group *db.Group, members []db.Member) string {
	var b strings.Builder

	b.WriteString("You are the onboarding agent for whatsapp-nagger, a couples/family task management bot.\n")
	b.WriteString("Your job is to set up this new group in a few short turns. Be warm and welcoming.\n\n")

	b.WriteString("# Current state\n")
	b.WriteString(fmt.Sprintf("- Language: %s\n", orNone(group.Language)))
	if len(members) == 0 {
		b.WriteString("- Members: (none yet)\n")
	} else {
		var parts []string
		for _, m := range members {
			if m.DisplayName == "" {
				parts = append(parts, fmt.Sprintf("%s → (unnamed)", m.WhatsAppID))
			} else {
				parts = append(parts, fmt.Sprintf("%s (%s)", m.DisplayName, m.WhatsAppID))
			}
		}
		b.WriteString(fmt.Sprintf("- Members (%d/%d): %s\n", len(members), db.MemberCap, strings.Join(parts, ", ")))
	}
	b.WriteString(fmt.Sprintf("- Timezone: %s\n", orNone(group.Timezone)))
	if group.DigestHourSet {
		b.WriteString(fmt.Sprintf("- Digest hour: %d\n", group.DigestHour))
	} else {
		b.WriteString("- Digest hour: (none yet)\n")
	}
	b.WriteString("\n")

	b.WriteString("# Onboarding flow — capture these in order, ONE at a time\n")
	b.WriteString("1. **Language** (`set_language`): pick `he` (Hebrew) or `en` (English). Permanent — cannot be changed later.\n")
	b.WriteString("2. **Members** (`set_member`): up to 2 people, captured by display name + WhatsApp phone.\n")
	if hasUnnamedMembers(members) {
		b.WriteString("   - Some members were already detected from the WhatsApp group (shown as `phone → (unnamed)` above). Just ask what to call each one — for example: \"You're listed as 972501234567 — what should I call you?\". Then call `set_member` with that phone and the chosen name. Do NOT ask for the phone again; you already have it.\n")
	} else {
		b.WriteString("   - In dev/terminal mode (no members pre-seeded), ask for both display name and WhatsApp phone (international format, no `+`).\n")
	}
	b.WriteString("   - If a member says \"just me\" / \"I'm solo\", you can stop after one member.\n")
	b.WriteString("3. **Timezone** (`set_timezone`): IANA tz, e.g. `Asia/Jerusalem`, `Europe/Berlin`.\n")
	b.WriteString("4. **Digest hour** (`set_digest_hour`): 0–23, in the group's timezone. Suggest 8 or 9 if unsure.\n")
	b.WriteString("5. **Confirm** (`complete_onboarding`): when all set, summarize the captured config and call this tool.\n\n")

	b.WriteString("# Tone & language\n")
	switch group.Language {
	case "he":
		b.WriteString("Reply ONLY in Hebrew from now on. Warm, friendly, brief.\n")
	case "en":
		b.WriteString("Reply ONLY in English from now on. Warm, friendly, brief.\n")
	default:
		b.WriteString("Until the language is picked, write each reply BILINGUALLY — Hebrew first, then English (or vice versa). Example greeting: `שלום! אני עוזר ניהול המשימות. עברית או English? / Hi! I'm the task management bot. Hebrew or English?`\n")
	}
	b.WriteString("Avoid sarcasm during onboarding — that's the main agent's job, not yours.\n\n")

	b.WriteString("# Tool rules\n")
	b.WriteString("- NEVER mention or pass `group_id` — it's set automatically.\n")
	b.WriteString("- Ask for ONE missing field at a time. After each tool call, briefly confirm and ask the next question.\n")
	b.WriteString("- If a tool refuses (e.g. member cap exceeded), explain warmly in the locked language.\n")
	b.WriteString("- The `whatsapp_id` field is digits only, international format, no `+`. Example: `972501234567`. Strip any spaces, dashes, or `+` before calling the tool.\n")
	b.WriteString("- When the user gives you their phone in any other format, normalize it before passing to `set_member`.\n\n")

	if next := nextMissingField(group, members); next != "" {
		b.WriteString(fmt.Sprintf("# Next step\n%s\n", next))
	} else {
		b.WriteString("# Next step\nAll required fields are set. Summarize the config and call `complete_onboarding`.\n")
	}

	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(none yet)"
	}
	return s
}

// nextMissingField returns the next onboarding step instruction, or "" when
// nothing is missing.
func nextMissingField(group *db.Group, members []db.Member) string {
	if group.Language == "" {
		return "Ask the user to pick Hebrew (`he`) or English (`en`)."
	}
	if hasUnnamedMembers(members) {
		// Pre-seeded by AutoCreate from the WhatsApp group; only names are missing.
		var phones []string
		for _, m := range members {
			if m.DisplayName == "" {
				phones = append(phones, m.WhatsAppID)
			}
		}
		return fmt.Sprintf("Ask what to call the unnamed member(s): %s. Use `set_member` to attach a display name to each existing phone.", strings.Join(phones, ", "))
	}
	if len(members) == 0 {
		return "Ask for the first member's display name and WhatsApp phone."
	}
	if group.Timezone == "" {
		return "Ask for the IANA timezone (e.g. `Asia/Jerusalem`)."
	}
	if !group.DigestHourSet {
		return "Ask what hour (0-23) of the day they want the daily digest."
	}
	return ""
}

// hasUnnamedMembers reports whether any member row exists with an empty
// display_name. AutoCreate seeds rows this way when the WhatsApp messenger
// inserts allowlisted phones at first message.
func hasUnnamedMembers(members []db.Member) bool {
	for _, m := range members {
		if m.DisplayName == "" {
			return true
		}
	}
	return false
}
