---
stepsCompleted: []
inputDocuments: []
workflowType: 'prd'
---

# Product Requirements Document — whatsapp-nagger

**Author:** Liza
**Date:** 2026-05-02
**Status:** Draft

## Problem

The nagger works great for our family, and friends want it too. The codebase assumes a single household — one set of members, one timezone, one digest schedule baked into env vars. To share it with ~10 friend groups without forking and per-friend deploys, the bot needs to support multiple WhatsApp groups from a single instance, with each group's setup driven entirely from inside the chat.

## Goals

- Run one bot serving up to ~10 distinct WhatsApp groups from a single Fly deploy.
- New group adds the bot → bot self-introduces and walks the group through setup conversationally. No config files, no web UI.
- Each group's tasks, settings, and conversation history are isolated by `group_id`.
- Existing single-tenant data (Liza's family) migrates cleanly to a "tenant zero" group row with no functional regression.

## Non-Goals (deferred)

- Per-group credit-card scraping and expense tracking for *new* groups. Liza's tenant-zero group keeps full financial functionality (existing scraper, expense tools, transactions). New friend groups get tasks + nags + digests only; expense tools are not exposed to them.
- Hosted scraper, secure credential UI, or any web surface. Liza's local scraper continues to work for tenant zero only.
- Authentication / accounts beyond "you're in an allowlisted group, you're authorized."
- Admin dashboard, billing, usage analytics.

## Security & Isolation (non-negotiable)

- **Hard isolation**: every DB query, every tool call, every prompt context is scoped by `group_id`. There is no tool, no admin command, and no agent capability that lists groups, members of other groups, or data from any group other than the caller's. This is enforced at the data-access layer, not just at the agent prompt level.
- **No cross-group leak surface**: tools accept no `group_id` parameter from the LLM — `group_id` is derived from the inbound message envelope server-side and injected into the call. The model cannot ask for "all groups" because no tool returns that shape.
- **Financial tools are tenant-bound**: `expenses_summary` and `list_transactions` are only registered into the tool list when the calling group has financial access enabled. For other groups they don't exist in the schema the LLM sees.
- **Member-phone allowlist**: the operator (Liza) maintains an allowlist of approved WhatsApp phone numbers. The bot operates in a group iff at least one allowlisted phone is a member of that group. If no allowlisted member is present, the bot stays completely silent — no message, no leave, no presence beyond joining.
- **Tenant-zero financial access**: a `groups.financial_enabled` boolean column gates the registration of expense tools (`expenses_summary`, `list_transactions`) and any future card-related capabilities into the LLM's tool schema. Liza's group is the only one with this flag set to true in v1. The flag is set in DB, not via the agent — no tool exposes a way to flip it.

## Users

- **Group owner / inviter** — adds the bot to a WhatsApp group, drives onboarding.
- **Group members** — 1 or 2 people total. Solo (just me) and couple (two adults) are both first-class.
- Kids, roommates, larger families: not supported in v1.

## Constraints

- Member cap: 1 ≤ members ≤ 2 per group.
- Group cap: ~10 (not strictly enforced; allowlist-friendly).
- Tone: the existing no-nonsense Israeli-engineer persona is fixed for v1 — onboarding is delivered *in the agent's voice*, not a separate polite-wizard tone. No per-group persona customization.
- Languages: Hebrew and English. Chosen during onboarding (first question) and locked for the lifetime of the group. All subsequent agent replies use that language.
- WhatsApp limitations explicitly out of scope (assume the multi-device API handles N groups fine).

## Onboarding Flow (chat-driven)

When the bot detects it's been added to a group containing at least one allowlisted phone, and the group has not yet been onboarded:

**Tone for onboarding:** the agent drops its usual sarcasm and opens warm/welcoming — something like *"hey, heard there are some folk here who have things falling behind. I'm here to help."* The familiar persona returns once setup is done.

1. **Language pick** — first message bilingual: *"עברית או English?"*. Locks `groups.language`.
2. Self-introduces in the chosen language, warm tone.
3. Asks whether this is solo or for a couple.
4. Captures member names + WhatsApp identifiers (one or two).
5. Asks for timezone.
6. Asks for preferred digest hour.
7. Confirms captured config back, then closes with a usage hint.

Onboarding completes when all required fields are captured. Subsequent messages go through the normal agent loop. A group whose onboarding is abandoned mid-flow resumes from the last unanswered question on next message.

Onboarding is considered complete when all required fields are captured. Subsequent messages from the group go through the normal agent loop.

A group whose onboarding was abandoned mid-flow resumes from the last unanswered question on next message.

## Functional Requirements

### Multi-tenancy

- `group_id` (WhatsApp group JID) on `tasks`, `metadata`, conversation history, and any future per-group state.
- Every DB query and every tool call scopes by `group_id`.
- A single `groups` table holds: `id` (JID), `name`, `language` (`he` | `en`), `timezone`, `digest_hour`, `onboarding_state`, `financial_enabled` (bool), `created_at`.
- A `members` table holds: `group_id`, `whatsapp_id` (phone), `display_name`, `created_at`.
- An operator-managed allowlist of approved phone numbers lives in env (`ALLOWED_PHONES`, comma-separated). The bot only engages with groups containing at least one allowlisted phone.

### Agent tools (group-scoped)

Existing, modified to scope by `group_id` and gain editable fields:

- `add_task`, `list_tasks`, `delete_task` — unchanged in shape, scoped internally.
- `update_task` — extended to accept optional `content` and `assignee` in addition to `status` and `due_date`. Empty / omitted fields skipped.

New:

- `get_group_settings` — returns name, timezone, digest hour, members.
- `update_group_settings` — patch any of name, timezone, digest hour.
- `add_member`, `update_member`, `remove_member` — with the 1–2 member cap enforced. On `remove_member`, any open tasks assigned to the removed person are auto-reassigned to the remaining member; if the group becomes empty, the operation is refused.

### Expense tools (tenant zero only)

- `expenses_summary` and `list_transactions` are registered into the LLM's tool schema *only* when the calling group's `financial_enabled` is true. They never appear for other groups.

### Tenant-zero migration

- Single migration that creates `groups` and `members` tables, backfills Liza's family group row (JID from current env), backfills members from `CARD_OWNERS` / persona file, backfills all existing `tasks` rows with the new `group_id`, and adds `group_id` to `metadata` rows where applicable.
- Migration is idempotent and reversible enough to test locally.

### Daily digest

- Per-group ticker / scheduling: digest goes to each group at its configured hour in its configured timezone.
- `metadata.last_digest_date` becomes per-group state.

## Success Criteria

- Liza's family group continues to work identically through the migration. Zero perceived behavior change.
- A second test group (Liza's own phone in a 2-person test chat) can be onboarded end-to-end via chat alone in under 5 minutes.
- Each group sees only its own tasks. Cross-group leak is impossible by construction (every query takes `group_id`).
- A friend can be invited as the inviter of a fresh group and reach a working state without Liza touching the server.

## Risks & Open Questions

- **Onboarding state machine in a free-form chat agent.** The agent has to recognize "still onboarding" and constrain its tool set / prompt accordingly. Likely solved by injecting an "onboarding mode" sub-prompt while `groups.onboarding_state != 'complete'`.
- **Per-group digest scheduling** — the current in-process ticker fires once daily and scans state. Needs to become aware of per-group hour/timezone. Probably easier to keep a single ticker that runs every N minutes and checks "is it time for any group?" rather than per-group goroutines.
- **Persona / language** — open question whether non-Hebrew-speaking friends want English. Defer; persona stays fixed for v1, revisit if anyone complains.
- **Migration of existing conversation history** — currently the conversation cap is global. After migration it becomes per-group, and the existing in-memory history (if any) gets attributed to tenant zero.
