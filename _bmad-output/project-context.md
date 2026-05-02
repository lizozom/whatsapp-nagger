---
project_name: 'whatsapp-nagger'
user_name: 'Liza'
date: '2026-05-02'
sections_completed: ['technology_stack', 'single_tenant_assumptions', 'multi_tenancy_seams', 'patterns_to_preserve']
---

# Project Context for AI Agents

_This file captures the brownfield reality of `whatsapp-nagger` as it exists before the multi-tenancy work begins. It documents single-tenant assumptions baked into the code, the seams that must change, and the conventions any agent must respect when implementing the upcoming changes._

---

## Technology Stack

- **Bot core:** Go 1.22+, single binary built from `cmd/nagger/main.go`.
- **WhatsApp client:** `go.mau.fi/whatsmeow` (multi-device API). Session DB at `whatsapp_session.db` (SQLite).
- **LLM:** Anthropic Claude Haiku 4.5 (`claude-haiku-4-5-20251001`) via `github.com/anthropics/anthropic-sdk-go`. Tool calling only — no free-form parsing.
- **App DB:** SQLite via `modernc.org/sqlite` (pure Go driver — no cgo). WAL mode. Path from `TASKS_DB_PATH`.
- **HTTP:** stdlib `net/http` with a single shared mux owned by `main.go`. Endpoints: `/healthz`, `/health`, `/pair`, `/ingest/transactions`, `/notify`, plus dashboard auth routes.
- **Scraper sidecar:** `scraper/` — TypeScript, `israeli-bank-scrapers` (Cal + Max). Runs on Liza's Mac via launchd at 22:00 IDT, pushes via HMAC to `/ingest/transactions`.
- **Dashboard:** `dashboard/` — Next.js (App Router) read-only web UI for tasks + finances. Auth via WhatsApp DM OTP → JWT.
- **Deploy:** Fly.io. Single VM, persistent volume for SQLite. `deploy.sh` builds + deploys.
- **Env loading:** `github.com/joho/godotenv` reads `.env` at startup.

## Single-Tenant Assumptions Baked Into the Code

These are the pieces that *will* break or need surgery for multi-tenancy. Anyone implementing the multi-tenant cutover must touch all of these.

### Process-global identity

- **`WHATSAPP_GROUP_JID` env var** hardcodes the one group at process start (`cmd/nagger/main.go:61`). `messenger/whatsapp.go:184` drops every message from any other group with `chat.String() != wa.groupJID.String()`. `Write()` always sends to `wa.groupJID`. The `WhatsApp` struct has a single `groupJID types.JID` field.
- **`personas.md` (gitignored)** is the source of truth for member names ↔ phones. Read globally via `LoadPersonas()` and `ParsePersonaPhones()` in `internal/agent/agent.go`. Used for: system prompt context, mention resolution, nag DM targeting, dashboard auth allowlist, and `dashboard_link` tool.
- **`CARD_OWNERS`** env var maps names → provider/last4 lists. Process-global.
- **`BILLING_DAY`, `TIMEZONE`, `MERCHANT_CONTEXT`** all read from env per-call inside agent code. Process-global.

### Process-global state

- **`Agent.history` is a single `[]anthropic.MessageParam`** (`agent.go:194`). Every group's conversation would mix in the same slice. Becomes per-group on multi-tenant.
- **`metadata` table** has process-global keys: `last_digest_date`, `last_nag_date`. Becomes per-group keys (`last_digest_date:{group_id}` or a row-per-group structure).
- **System prompt** built per-message in `buildSystemPrompt()` but uses process-global timezone, members, merchant context. Becomes per-group input.

### Process-global schedulers

- `startDigestScheduler` (`main.go:210`) — single ticker, single `last_digest_date` check, single `m.Write` to the global group.
- `startNagScheduler` (`main.go:159`) — single ticker, scans `CountOverdueByAssignee` across all tasks (no group scoping), DMs anyone over threshold.
- Both use `Asia/Jerusalem` and a single hour from env.

### Single-tenant tool surface

- All task tools (`add_task`, `list_tasks`, `update_task`, `delete_task`) operate on the unscoped `tasks` table.
- `update_task` only accepts `status` and `due_date` — no `content` or `assignee` editing yet.
- Expense tools (`expenses_summary`, `list_transactions`) are always registered if `txStore != nil`. They have no group concept.
- `dashboard_link` resolves the user via `personas.md` and produces a magic link via the global auth handler.

## Multi-Tenancy Seams (where surgery happens)

Any agent doing this work should preserve the existing flat structure and modify these specific surfaces:

1. **`internal/db/db.go`** — schema migration adds `groups`, `members`, plus `group_id` columns on `tasks` and `metadata`. Every query takes `group_id`. Tenant-zero backfill happens here.
2. **`internal/messenger/whatsapp.go`** — drop the single `groupJID` field. Incoming filter becomes "is the source group on the allowlist (i.e. contains an allowlisted phone)." `Write(group_id, text)` and friends become group-aware. New event handler for `events.JoinedGroup` (or equivalent) to trigger onboarding.
3. **`internal/messenger/messenger.go`** (`IMessenger` interface) — `Read()` returns a message that includes `GroupID`. `Write()` takes a `GroupID`. Terminal mode emulates a single fake group ID for dev.
4. **`internal/agent/agent.go`** — `Agent` becomes `Agent` with per-group history (`map[GroupID][]MessageParam` or DB-backed). `HandleMessage(group_id, sender, text)`. `buildSystemPrompt(group_id)` queries the group's row. Tool registration becomes a function of group capabilities (`expenses_summary` and `list_transactions` only when `groups.financial_enabled`). New tools: `get_group_settings`, `update_group_settings`, `add_member`, `update_member`, `remove_member`. `update_task` extended with `content` and `assignee`.
5. **`cmd/nagger/main.go`** — schedulers become group-iterating. Allowlist read from `ALLOWED_PHONES` env. Onboarding state machine routed through the agent (or a sibling onboarding handler) when a group's `onboarding_state != complete`.
6. **Onboarding flow** — new package, likely `internal/onboarding/`. Captures language, member count, names, phones, timezone, digest hour. Persists to `groups` + `members`.

## Critical Implementation Rules

These are the rules an agent **must** follow when implementing features in this codebase. They prevent both regressions and security mistakes.

### Hard data isolation (non-negotiable)

- **`group_id` is never a parameter on the LLM-facing tool schema.** It is derived server-side from the inbound message envelope and injected into the tool execution. The model cannot ask for "all groups" because no tool returns that shape.
- **Every DB query takes `group_id`.** No helper, no convenience function, no test fixture is allowed to query `tasks`, `members`, conversation history, or `metadata` without it. Add a layer at the store level that refuses queries without a `group_id` rather than relying on call-sites to remember.
- **No tool lists groups, members of other groups, or any cross-group aggregation.** Liza included — she does not get an admin back-door through the agent. Operator-level inspection happens via SSH + sqlite, not tools.
- **`expenses_summary` and `list_transactions` are conditionally registered** based on `groups.financial_enabled` for the calling group. They must not appear in the tool schema for other groups (not "appear and refuse"; not present at all).
- **`groups.financial_enabled` is set in DB only.** No tool, no chat command, no prompt can flip it.

### Allowlist gating

- The operator allowlist is `ALLOWED_PHONES` in env (comma-separated international-format phone numbers, no `+`).
- Bot operates in a group iff at least one allowlisted phone is a member of that group.
- Non-allowlisted groups: completely silent. No reply, no leave, no presence beyond joining.
- Allowlist membership is checked *every message*, not cached at join time — friends drop in and out of groups, allowlist can change.

### Onboarding tone & language

- Language is bilingual ("עברית או English?") on first contact. Locks `groups.language` permanently after pick.
- Onboarding tone is **warm/welcoming**, not the usual sarcastic persona. Sample phrasing: *"hey, heard there are some folk here who have things falling behind. I'm here to help."* The familiar persona returns once `onboarding_state = complete`.
- All replies, including the digest format, must respect `groups.language`.

### Member cap

- 1 ≤ `members.count` ≤ 2 per group. Enforce in `add_member` and in onboarding. Rejecting "we have three kids" is fine — v1 is solo & couples only.
- On `remove_member`: open tasks assigned to the removed person are auto-reassigned to the remaining member. If a group would become empty, refuse.

### Conversation history

- Cap remains at 20 messages, but **per-group**, not global.
- The trim algorithm in `trimHistory` (preserves a valid leading user message, no orphan tool_results) is non-trivial — keep it intact, just apply it per-group.

### Patterns to preserve

- **Flat code, minimal abstraction.** Don't introduce a `tenancy` package. Add `group_id` parameters and a `groups` store; that's it.
- **`IMessenger` is the seam between dev (terminal) and prod (WhatsApp).** Terminal mode must keep working — emulate a single hardcoded "dev" group ID.
- **All Claude interactions use tool calling.** Don't add free-form text-parsing fallbacks for onboarding; use a tool that the agent calls when it has all the captured fields.
- **SQLite is the single source of truth.** Don't add a config-file fallback for per-group settings.
- **Personal identifiers** (real names, phone numbers, JIDs, last4s) only appear in gitignored files (`.env`, `personas.md`) — never in committed code or tests. Use `Alice` / `Bob` placeholders.
- **Conversation history is capped at 20 messages.** Bound token usage is a deliberate constraint, not an oversight.
- **Never auto-bump versions.** The user controls `internal/version/version.go` manually.

### Migration safety

- The tenant-zero migration must be **idempotent and reversible enough to test locally.**
  - Backfill from `WHATSAPP_GROUP_JID`, `personas.md`, `CARD_OWNERS`, `TIMEZONE`, `BILLING_DAY` env vars into a single `groups` row (`financial_enabled = true`) plus matching `members` rows.
  - Backfill `tasks.group_id` and `metadata.group_id` to that row's id.
  - Verify with a `SELECT COUNT(*) WHERE group_id IS NULL` — must return 0 across all tables.
- The migration runs at process start (existing pattern: schema is `CREATE TABLE IF NOT EXISTS` in `NewTaskStore`). Add a versioned migration step rather than mutating that block in-place.

### Testing

- Tests live next to the code (`*_test.go`). `internal/db/db_test.go`, `internal/agent/agent_test.go`, `internal/ingest/ingest_test.go` exist — match their style.
- Tests use temp SQLite DBs (no shared fixture file).
- Don't mock at the DB layer — use real SQLite. Mocks live at `IMessenger` (terminal mode) and at the Anthropic client level if needed.

### What's deferred and must not regress

- **Tenant-zero financial functionality (Liza's group) keeps working identically.** This means: scraper push to `/ingest/transactions`, `expenses_summary`, `list_transactions`, `dashboard_link`, daily digest, nag DMs — all of these continue to behave the same for tenant zero through and after the migration.
- **The Next.js dashboard stays tenant-zero only.** Don't extend its auth, OTP, or schema for friend groups in v1. Dashboard JWT keeps using the existing personas-based allowlist.
- **`/ingest/transactions` HMAC endpoint stays single-tenant.** No `group_id` on that endpoint — it's Liza's scraper only. If a friend ever wants expenses, that's a future surface, not v1.
