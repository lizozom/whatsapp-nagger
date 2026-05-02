---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8]
inputDocuments:
  - _bmad-output/planning-artifacts/prd.md
  - _bmad-output/project-context.md
workflowType: 'architecture'
project_name: 'whatsapp-nagger'
user_name: 'Liza'
date: '2026-05-02'
lastStep: 8
status: 'complete'
completedAt: '2026-05-02'
---

# Architecture Decision Document — whatsapp-nagger

_This document builds collaboratively through step-by-step discovery. Sections are appended as we work through each architectural decision together._

## Scope Refinement (captured during init)

- **All financial data is scoped by `group_id`.** Despite tenant-zero being the only group with `financial_enabled=true` in v1, the `transactions` table gains a `group_id` column and every query/tool filters by it server-side. The tenant-zero migration backfills `transactions.group_id` to Liza's group. The `/ingest/transactions` HMAC endpoint stays single-tenant in v1 but writes scoped rows. Rationale: eliminates "transactions live outside the tenancy model" ambiguity; future per-group ingest becomes a routing change, not a schema migration.

## Project Context Analysis

### Requirements Overview

**Functional Requirements (from PRD):**

- **FR1 — Multi-tenancy:** `group_id` on `tasks`, `metadata`, conversation history, and `transactions` (refinement). New `groups` table (`id`, `name`, `language`, `timezone`, `digest_hour`, `onboarding_state`, `financial_enabled`, `created_at`, `last_active_at`) and `members` table (`group_id`, `whatsapp_id`, `display_name`, `created_at`).
- **FR2 — Group-scoped agent tools:** Existing task tools (`add_task`, `list_tasks`, `delete_task`, `update_task`) scoped server-side by `group_id`. `update_task` gains optional `content` and `assignee`. New tools: `get_group_settings`, `update_group_settings`, `add_member`, `update_member`, `remove_member` (1–2 member cap; `remove_member` auto-reassigns open tasks; refuses if group would become empty).
- **FR3 — Conditional financial tools:** `expenses_summary` and `list_transactions` registered into the LLM tool schema only when calling group's `financial_enabled = true`. Data layer always scopes by `group_id` regardless.
- **FR4 — Tenant-zero migration:** Idempotent migration creates `groups`/`members`, backfills tenant-zero row from existing env vars + persona file, populates `group_id` on `tasks`, `metadata`, and `transactions`. Reversible enough to test locally.
- **FR5 — Per-group daily digest & nag:** Each group's digest fires at its configured `digest_hour` in its `timezone`. `metadata.last_digest_date` and `last_nag_date` become per-group keys.
- **FR6 — Chat-driven onboarding:** Language pick (bilingual prompt) → solo/couple → member names + WhatsApp IDs → timezone → digest hour → confirm. Resumes from last unanswered question if abandoned. Delivered via tool calls in a warm tone; the regular persona returns at completion.

**Non-Functional Requirements:**

- **NFR1 — Hard data isolation (non-negotiable):** Every DB query takes `group_id`. No tool returns cross-group data. `group_id` is never on the LLM tool schema — derived server-side from message envelope. Operator (Liza) gets no admin back-door through the agent.
- **NFR2 — Allowlist gating:** `ALLOWED_PHONES` env (comma-separated, no `+`). Bot operates in a group iff at least one allowlisted phone is a member. Checked every message (not cached). Non-allowlisted groups: completely silent.
- **NFR3 — Member cap:** 1 ≤ members ≤ 2 per group, enforced in onboarding and `add_member`.
- **NFR4 — Tone & language:** Bilingual onboarding prompt; `groups.language` locks permanently at first pick. Warm tone during onboarding, sarcastic Israeli-engineer persona afterwards. Both shape the system prompt.
- **NFR5 — Persistence:** SQLite remains the single source of truth (see decision below). No config-file fallback for per-group settings. `financial_enabled` flips happen in DB only, never via tool/chat.
- **NFR6 — Conversation history cap:** 20 messages, per-group. Existing `trimHistory` algorithm (preserves leading user message, prevents orphan tool_results) preserved verbatim, applied per-group, per-agent (main vs onboarding).
- **NFR7 — Migration safety:** Idempotent. Verify with `SELECT COUNT(*) WHERE group_id IS NULL` returning 0 across all scoped tables.
- **NFR8 — Code style:** Flat, minimal abstraction. No `tenancy` package. Add `group_id` parameters and a `groups` store; that's it.
- **NFR9 — Privacy:** Personal identifiers (real names, phones, JIDs, last4s) only in gitignored files. Placeholders in committed code/tests.

**Scale & Complexity:**

- Primary domain: **backend messaging bot** (Go core), peripheral TypeScript scraper + Next.js dashboard.
- Complexity level: **medium overall, high in isolation/security** — small surface area, high correctness bar on scoping.
- Architectural components affected: `internal/db`, `internal/messenger`, `internal/agent` (split into main + onboarding), `cmd/nagger`, plus a new `internal/onboarding` helper if onboarding-specific tool handlers need a home (TBD in step 4).
- Throughput: ~10 groups × ≤2 members × low message rate — vertical scaling on a single Fly VM is sufficient.

### Architectural Decisions Captured in Step 2

#### D1 — Two-agent split: main agent + onboarding agent

Onboarding is a separate agent type, not a mode of the main agent.

**Shape (still flat, not a new package):**

```
internal/agent/
  main_agent.go        // existing Agent renamed
  onboarding_agent.go  // new
  dispatch.go          // routes by groups.onboarding_state
```

- Tool surfaces are physically disjoint. Main: task tools, group/member management, optionally financial tools (if `financial_enabled`). Onboarding: `set_language`, `set_member`, `set_timezone`, `set_digest_hour`, `complete_onboarding`. Neither sees the other's tools.
- System prompts are separate (warm/welcoming bilingual vs. locked-language sarcastic persona).
- Per-group conversation history is per-agent: the warm-tone onboarding exchange does not carry into the main agent's context after completion. Onboarding history is discarded/archived on `complete_onboarding`.
- `dispatch.go` does the routing on every inbound message: `groups.onboarding_state == "complete"` → main; otherwise → onboarding. One-line rule.
- Both agents share the Anthropic client, the `trimHistory` helper, and the per-group history store.

**Rationale:** Eliminates conditional-by-state tool registration (state machine leakage) by replacing it with capability-defined tool surfaces. Mirrors the project's existing isolation philosophy ("not present" beats "present and refuses"). Keeps each agent's contract verifiable in isolation.

#### D2 — Stay on SQLite for v1

The existing `modernc.org/sqlite` setup remains the persistence layer. No migration to Postgres or any other DB.

**Rationale:**

- Volume is tiny (~10 groups, ≤20 humans, low write rate).
- None of SQLite's pain points trigger here: no multi-writer concurrency (single Go binary on one Fly VM), no HA need beyond what we already have, no multi-region.
- Bundling a DB swap on top of the multi-tenancy schema cutover doubles the highest-stakes risk (the tenant-zero backfill) for zero functional benefit.
- Pure-Go driver keeps the single-binary, no-cgo build asset intact.

**Operational improvements bundled with v1 (cheap, optional but recommended):**

- **Litestream** — continuous SQLite replication to S3/R2 for point-in-time recovery. Config-only, no code change.
- **`sqlite3` baked into the Fly image** — fixes the existing pain point of not being able to query prod DB via `fly ssh`.

**Triggers that would force re-evaluation post-v1:**

- Friends-dashboard ships (multi-tenant Next.js with many users).
- Group count climbs past ~50.
- Multi-region requirement.
- A second writer process added (e.g., scraper-puller alongside the bot).

None of these are in v1 scope.

### Technical Constraints & Dependencies

- **Inherited stack:** Go 1.22+, `whatsmeow`, `modernc.org/sqlite` (pure Go, no cgo), `anthropic-sdk-go`, stdlib `net/http`. No new runtime dependencies anticipated for v1.
- **Tool calling only** for LLM interactions — no free-form text-parsing fallbacks, including for onboarding.
- **Single Fly VM with persistent volume** — no horizontal scaling, no shared state across instances.
- **Scraper sidecar stays single-tenant** — runs on Liza's Mac via launchd, pushes to `/ingest/transactions` HMAC. No per-group ingest in v1.
- **Dashboard stays tenant-zero only** — Next.js auth, OTP/JWT, schema all unchanged for friend groups in v1.
- **`/ingest/transactions` endpoint stays single-tenant in v1** — but rows it writes carry `group_id` (tenant zero), making future per-group ingest a routing change rather than a schema change.
- **Existing in-process patterns preserved:** single HTTP mux owned by `main.go`; SQLite migrations as `CREATE TABLE IF NOT EXISTS` at startup, extended with a versioned migration step; schedulers as goroutines off `main`.

### Cross-Cutting Concerns Identified

1. **Group scoping** — threads through DB schema, every store function, agent histories (per-agent + per-group), messenger seam, tool registries, both schedulers, and system-prompt construction.
2. **Two-agent dispatch** — every inbound message routed to main or onboarding agent based on `groups.onboarding_state`. Main agent never sees onboarding tools and vice-versa.
3. **Capability-gated tool registration** — `expenses_summary` / `list_transactions` registered into main agent's schema only when calling group's `financial_enabled = true`. (This is a *capability* gate, distinct from the *state* gate handled by the agent split.)
4. **Per-group scheduling** — single ticker iterating groups (one ticker per scheduler; not per-group goroutines), checking per-group hour/timezone. Per-group `last_digest_date` and `last_nag_date` keys.
5. **Allowlist gating** — every inbound message filtered before reaching either agent. Non-allowlisted groups receive zero side effects.
6. **Migration as a boundary event** — runs at process start, idempotent, must verify zero-NULL invariants on `group_id` columns post-backfill.
7. **Language and tone in system prompts** — `groups.language` (locked at first onboarding tool call) plus agent-type both shape the system prompt.
8. **Privacy boundary** — personal identifiers stay out of committed code; tests use placeholders; `personas.md` (tenant-zero only) remains gitignored.

## Starter Template Evaluation

**Status: Not applicable — brownfield project.**

The architectural foundation already exists in the working codebase. The technology stack is fixed by reality, not by selection:

- **Bot core:** Go 1.22+ single binary, `cmd/nagger/main.go` entrypoint
- **WhatsApp client:** `go.mau.fi/whatsmeow` (multi-device API)
- **LLM:** `github.com/anthropics/anthropic-sdk-go` (Claude Haiku 4.5)
- **DB:** `modernc.org/sqlite` (pure Go, no cgo)
- **Env loading:** `github.com/joho/godotenv`
- **HTTP:** stdlib `net/http`
- **Scraper sidecar:** TypeScript + `israeli-bank-scrapers`, npm-managed
- **Dashboard:** Next.js (App Router) at `dashboard/`
- **Deploy:** Fly.io, persistent volume for SQLite, `deploy.sh` script

No new components, runtimes, or scaffolds are introduced for the multi-tenancy work. Project structure and conventions are inherited verbatim from the existing repo (flat code, `internal/` packages, tests next to code).

**Implication:** Implementation work for v1 is purely surgical changes to existing modules (`internal/db`, `internal/messenger`, `internal/agent`, `cmd/nagger`) plus internal additions to `internal/agent/` for the two-agent split. No `npx create-*` or equivalent invocation is part of the plan.

## Core Architectural Decisions

### Decision Priority Analysis

**Already Decided (from earlier steps & existing codebase):**

- D1 — Two-agent split (main + onboarding) [step 2]
- D2 — Stay on SQLite for v1 [step 2]
- All-financial-data scoped by `group_id` [init]
- Stack inheritance: Go 1.22+, `whatsmeow`, `modernc.org/sqlite`, `anthropic-sdk-go`, stdlib `net/http`, single Fly VM, Next.js dashboard tenant-zero only [step 3 brownfield]

**Critical Decisions (block implementation):** D3, D4, D7, D8, D12

**Important Decisions (shape architecture):** D5, D6, D10, D14, D15

**Operational / Bundleable:** D11, D13, D16

### Data Architecture

**D3 — Hand-rolled migration framework, no library**

- New table: `schema_version (id INTEGER PRIMARY KEY, applied_at TEXT)`.
- `runMigrations(db)` runs at process start. Each migration is a numbered Go function (`migrate_001_groups`, `migrate_002_backfill_tenant_zero`, ...) that consults `schema_version`, applies if not yet recorded, records on success.
- The existing `CREATE TABLE IF NOT EXISTS` block in `NewTaskStore` continues creating the v1 baseline schema; new schema work flows through migration steps.
- **Rationale:** Project rule "flat code, minimal abstraction"; ~3–4 migrations for v1; library ceremony adds zero value at this scale.
- **Affects:** `internal/db/db.go`, `cmd/nagger/main.go` (call site).

**D4 — `groups.id = WhatsApp JID` directly (no surrogate key)**

- JIDs are stable in WhatsApp; no benefit to a separate numeric primary key.
- Foreign keys (`tasks.group_id`, `members.group_id`, `metadata.group_id`, `transactions.group_id`) all use the JID string.
- Index every `group_id` column for query performance.
- **Affects:** All scoped tables in `internal/db/db.go`.

**D5 — Conversation history stays in-memory, keyed by `(group_id, agent_kind)`**

- Replace single `[]MessageParam` with `map[historyKey][]MessageParam`, where `historyKey = {group_id, agent_kind}` (`agent_kind ∈ {"main", "onboarding"}`).
- Lost on process restart, same as today. Tenant-zero behavior unchanged.
- 20-message trim cap applied per-key via the existing `trimHistory` helper.
- **Why not persist:** Existing behavior is fine; persistence adds DB writes per message; restarts on Fly are rare. Defer.
- **Affects:** `internal/agent/main_agent.go`, `internal/agent/onboarding_agent.go`.

**D6 — On `complete_onboarding`, discard onboarding history entirely (not archived)**

- Map entry for `(group_id, "onboarding")` is deleted. No DB row.
- Main agent starts with fresh history; first message it processes is the next inbound user message after onboarding completes.
- **Rationale:** Warm-tone onboarding exchange has no archival value; keeping it costs context tokens for nothing.
- **Affects:** `internal/agent/onboarding_agent.go` (`complete_onboarding` tool handler).

### Authentication & Security

**D7 — Allowlist enforcement at the messenger layer**

- `internal/messenger/whatsapp.go` filters every inbound message before it reaches the dispatcher or any DB call.
- `isAllowlistedGroup(jid)` checks intersection of group members (from whatsmeow's session/group cache) with `ALLOWED_PHONES`.
- Allowlist parsed from env at process start, struct-cached. Re-read on SIGHUP if needed (deferred).
- Non-allowlisted groups: no reply, no log of message content, no DB write. Log emits `group_id="<dropped>"` only.
- **Rationale:** Fail-fast at the network boundary; one place to audit; zero accidental leakage.
- **Affects:** `internal/messenger/whatsapp.go`, `cmd/nagger/main.go` (env load).

**D8 — `group_id` derived server-side, injected as closure-captured handler arg**

- Tool definitions sent to the LLM never declare `group_id`. The LLM cannot ask for it.
- Per-message: a closure captures the current `group_id` and `sender_phone`; tool handlers invoke `Store.Method(group_id, ...)` with the captured values.
- Store interfaces make `group_id` the first parameter of every method (`TaskStore.List(group_id, ...)`, `TaskStore.Add(group_id, ...)`, etc.). No method exists without it.
- **Enforcement is structural** — code that lacks a `group_id` literally cannot call the store. Compile-time, not runtime.
- **Affects:** `internal/db/db.go` (every method signature), `internal/agent/*.go`, `internal/agent/tools.go`.

**D9 — Existing security surfaces unchanged for v1**

- `/ingest/transactions` HMAC: tenant-zero only.
- Dashboard JWT/OTP: tenant-zero only.
- `/notify` HMAC (scraper alerts): tenant-zero only.
- `/pair` (whatsmeow pairing): global, unchanged.
- None of these gain `group_id` parameters in v1.
- **Affects:** None — explicit non-changes.

### API & Communication

**D10 — Tool registry built per-message (capability-and-state-aware)**

- `BuildTools(ctx, group, agentKind) []anthropic.ToolParam` constructs the tool slice for the current call.
- **Onboarding agent tools (always):** `set_language`, `set_member`, `set_timezone`, `set_digest_hour`, `complete_onboarding`. The system prompt tells the agent what's already captured; the tool set is constant.
- **Main agent tools (always):** `add_task`, `list_tasks`, `update_task`, `delete_task`, `get_group_settings`, `update_group_settings`, `add_member`, `update_member`, `remove_member`, `dashboard_link`.
- **Main agent tools (capability-gated):** `expenses_summary`, `list_transactions` iff `group.financial_enabled = true`.
- Build cost is negligible at our message rate; no caching.
- **Affects:** `internal/agent/tools.go` (or wherever tool definitions live).

**D11 — HTTP layer unchanged**

- Stdlib `net/http`, single mux in `cmd/nagger/main.go`. No router library.
- No new HTTP routes for v1 multi-tenancy work — group lifecycle and dispatch happen inside the WhatsApp event handler, not via HTTP.
- **Affects:** None.

**D12 — Group lifecycle: auto-create on first allowlisted message**

- On inbound message from a JID with no `groups` row, and at least one allowlisted phone in the WhatsApp group's member list:
  1. Create `groups` row with `onboarding_state = "language_pick"`, `language = NULL`, `name = <whatsmeow group name>`, `financial_enabled = false`.
  2. Populate `members` rows for the **allowlisted** phones present in the WhatsApp group. Non-allowlisted phones are not inserted as members in v1.
  3. Hand to dispatcher → onboarding agent → first reply is the bilingual language-pick prompt.
- No separate "joined group" event handler — the first message is the trigger. Handles the case where the service starts after the bot was already added to a group.
- **Affects:** `internal/messenger/whatsapp.go` (or a new `internal/agent/dispatch.go`), `internal/db/db.go` (groups + members stores).

### Frontend Architecture

**D13 — Dashboard untouched in v1**

- No Next.js work for the multi-tenancy rebuild. Auth, OTP, JWT, schema unchanged.
- Even cosmetic copy is out of scope. Explicit non-extension boundary from PRD.
- **Affects:** None.

### Infrastructure & Deployment

**D14 — Schedulers: one goroutine per scheduler kind, group-iterating, 5-minute scan tick**

- `startDigestScheduler` becomes a single goroutine on `time.NewTicker(5 * time.Minute)`. Each tick:
  - `SELECT id, timezone, digest_hour FROM groups WHERE onboarding_state = 'complete'`
  - For each group: compute "is it the digest hour in this group's TZ today, and have we already sent today?" If yes, send and update `metadata` (key `last_digest_date:<group_id>` or row-per-group).
- `startNagScheduler` follows the same shape, scanning overdue tasks per group, DM'ing assignees over `NAG_THRESHOLD`.
- 5-minute granularity is fine for digest/nag; "9:00" fires within 9:00–9:05.
- **Rationale over per-group goroutines:** Iterator pattern matches existing simplicity, scales gracefully past 10 groups.
- **Affects:** `cmd/nagger/main.go` (both schedulers).

**D15 — Logging: structured `slog` with `group_id` on every tenant-scoped log line**

- Adopt stdlib `log/slog` (Go 1.21+, already available). Replace `log.Printf` calls in tenant-scoped paths with `slog` and a `group_id` attribute.
- No external logging dep. Default text formatter for human-readable `fly logs` tailing.
- Non-allowlisted message events log `group_id="<dropped>"` and no message content.
- **Affects:** Most files in `internal/`. Mechanical change; can land alongside the changes that introduce `group_id` in each module.

**D16 — Litestream + `sqlite3` in Fly image (operational additions)**

- `Dockerfile` includes `apt-get install -y sqlite3 litestream`.
- `litestream.yml` replicates the `tasks.db` to S3/R2 (bucket choice deferred to Liza).
- `fly.toml` adds Litestream as a sidecar process via `[processes]`.
- Optional bundling — can also land as a separate cleanup task post-cutover.
- **Affects:** `Dockerfile`, `fly.toml`, new `litestream.yml`.

### Implementation Sequence

1. **D3** — Migration framework. Lands first; nothing else can land safely without it.
2. **D4 + tenant-zero migration** — `groups`, `members` schema; backfill from `WHATSAPP_GROUP_JID`, `personas.md`, `CARD_OWNERS`. `group_id` populated on `tasks`, `metadata`, `transactions`. Verify `SELECT COUNT(*) WHERE group_id IS NULL = 0`.
3. **D8** — Store interfaces gain `group_id` first-parameter. Compile-time enforcement across the codebase.
4. **D7 + D12** — Allowlist filter at messenger layer; auto-create on first allowlisted message. Non-allowlisted groups silent through the cutover.
5. **D5 + D6 + D10 + D1 dispatcher** — Per-key history, agent split, per-message tool build, dispatcher routing by `onboarding_state`.
6. **Onboarding agent** — System prompt, tools (`set_language`, `set_member`, `set_timezone`, `set_digest_hour`, `complete_onboarding`), state transition on completion.
7. **D14** — Schedulers refactored to iterate groups.
8. **D15** — `slog` migration with `group_id` attribute. Lands incrementally with the touches above.
9. **D16** — Litestream + sqlite3 in Fly image. Independent infra; lands ideally before the tenant-zero migration runs in prod.

### Cross-Component Dependencies

- **D3 → everything schema-related.** Migration framework is a hard prerequisite.
- **D4 → D8.** Schema must exist before store signatures can require `group_id`.
- **D8 → D10 → D1 dispatcher.** Stores are scoped, then tool handlers wrap them with closures, then dispatcher selects which agent gets which tools.
- **D7 + D12 land together.** Allowlist gate and auto-create are two halves of the same boundary; splitting them invites a window where non-allowlisted groups get partial side effects.
- **D6 depends on D1 dispatcher** — discarding history happens at the agent boundary on `complete_onboarding`.
- **D15 (logging)** is concurrent with all of the above; not a blocker, but the discipline must be enforced in PR review.
- **D16 (Litestream)** is independent of the multi-tenancy work but a prerequisite for the tenant-zero migration's "reversible enough to test" property.

### Group Lifecycle

**D17 — Bot removed from group: preserve data, no-op on remove event**

- On `events.LeftGroup` (or whatsmeow equivalent): no DB change. The `groups` row, `members`, `tasks`, and history all stay intact.
- On next inbound message after re-add: existing `groups` row is found, dispatcher routes by `onboarding_state` as normal (`"complete"` → main agent immediately; `"in_progress"` → resume onboarding from current implicit state).
- No "removed" state column. No data deletion path in v1.
- **Rationale:** Friend groups may kick + re-add bots while reorganizing. Preserving data avoids forcing them through onboarding repeatedly. Data deletion is a separate operator action (manual SQL) if ever needed.
- **Affects:** `internal/messenger/whatsapp.go` (leave-event handler is a documented no-op).

**D18 — `groups.last_active_at` for observability**

- New TEXT column on `groups` table (ISO-8601 timestamp). Auto-create populates it on the first inbound message.
- Updated by `dispatch.Handle` on every successful inbound message processing (after the agent completes its turn). One small UPDATE per message.
- Tenant-zero migration (`migrate_002_backfill_tenant_zero`) backfills `last_active_at = created_at` for the existing row.
- **Rationale:** Operational visibility — distinguishing dormant from active groups without grepping logs. Cheap to maintain, supports future ops decisions.
- **Affects:** `internal/db/migrations.go` (column add in `migrate_001_groups`, backfill in `migrate_002_backfill_tenant_zero`), `internal/db/groups.go` (`UpdateLastActive(group_id)` method), `internal/agent/dispatch.go` (call after handle).
- **Not exposed via tools.** Operator queries via SSH + sqlite (consistent with NFR1's "no admin back-door through the agent" rule).

## Implementation Patterns & Consistency Rules

### Pattern Categories Defined

This is a brownfield Go codebase; most patterns are already established and inherited. The patterns below split into:

- **Locked from existing codebase** — recorded so future AI agents don't drift.
- **New for multi-tenancy** — introduced by D1–D16; agents must follow these exactly.

### Naming Patterns

**Database (locked):**

- Tables: lowercase plural — `tasks`, `transactions`, `metadata`, `ingest_runs`. New tables: `groups`, `members`, `schema_version`.
- Columns: `snake_case` — `due_date`, `card_last4`, `posted_at`.
- Foreign keys: `<entity>_id` — `group_id`, `whatsapp_id`. Always indexed.
- Primary keys: `id` for surrogate keys; for `groups.id` the value IS the WhatsApp JID string (D4), no surrogate.

**Go code (locked):**

- Files: `snake_case.go` (`main_agent.go`, `onboarding_agent.go`, `dispatch.go`, `db.go`).
- Packages: lowercase short names — `db`, `agent`, `messenger`, `ingest`, `version`.
- Functions/methods: Go standard (PascalCase exported, camelCase unexported).
- Test files: co-located `*_test.go`.

**Tools exposed to LLM (new):**

- Tool names: `snake_case` verb-object — `add_task`, `set_language`, `complete_onboarding`.
- Tool input schemas never include `group_id` (D8). Compile-time enforcement: store interfaces require `group_id` first, closures inject it.
- Onboarding tools follow pattern `set_<field>` (upsert). Main-agent member tools follow `add_member` / `update_member` / `remove_member` (explicit verbs). Onboarding's `set_member` and main's `add_member`/`update_member` are intentionally distinct — different lifecycles, different tool surfaces.

**Migration steps (new):**

- Function naming: `migrate_NNN_description` with 3-digit zero-padded number — `migrate_001_groups`, `migrate_002_backfill_tenant_zero`, etc.
- Each migration consults `schema_version`, applies if absent, records on success — pattern enforced by the runner, not by the migration body.

**Logging attribute keys (new):**

- `slog.String("group_id", ...)` on every tenant-scoped log line. Use literal `"group_id"` — never `"groupID"` or `"gid"`.
- `slog.String("sender_phone", ...)` for sender attribution where relevant.
- Non-allowlisted-group log lines emit `group_id="<dropped>"` and no message content.

### Structure Patterns

**Project layout (locked):**

- `cmd/nagger/main.go` — single binary entrypoint, owns the HTTP mux and scheduler goroutines.
- `internal/<domain>/` — flat domain packages: `db`, `agent`, `messenger`, `ingest`, `version`.
- No `tenancy/` package. No service/repository ceremony. `group_id` parameters and a `groups` store; that's it.
- Tests next to code; real SQLite in tests, no mocks at the DB layer.

**Agent layout (new — from D1):**

```
internal/agent/
  main_agent.go        // existing Agent, renamed
  onboarding_agent.go  // new
  dispatch.go          // routes by groups.onboarding_state
  tools.go             // BuildTools(group, agentKind) registry
  history.go           // map[historyKey][]MessageParam + trimHistory
```

No `internal/onboarding/` separate package — the onboarding agent lives in `internal/agent/` alongside the main agent.

### Format Patterns

**Onboarding state representation (new):**

- `groups.onboarding_state` is a TEXT column with values: `"in_progress"` or `"complete"`.
- Granular sub-state ("which question is next") is *implicit* — derived from which fields are populated (`language IS NULL` → ask language; `members` count = 0 → ask members; etc.). The system prompt computes this each turn. No separate state-machine column.
- Reasoning: avoids state/data drift. The DB row is the single source of truth.

**Per-group metadata (new):**

- `metadata` table extends with `group_id TEXT NOT NULL` column.
- Composite key: `(group_id, key)`. No string concatenation in keys.
- Existing keys carry to per-group: `last_digest_date`, `last_nag_date`.
- Tenant-zero migration backfills existing rows with the tenant-zero `group_id`.

**Allowlist phone format (locked):**

- `ALLOWED_PHONES` env: comma-separated, international format, no `+`, no spaces.
- Example: `972541234567,972549876543`.
- Same format used in `members.whatsapp_id` so they compare directly with `==`.

**Errors:**

- Go errors wrapped with `fmt.Errorf("...: %w", err)` per existing convention.
- Tool handlers return `(toolResultJSON string, err error)`; the JSON contains user-facing copy in the group's locked language. Errors that escape to the LLM as tool failures are fine — the LLM phrases them in its persona.

### Communication Patterns

**Tool registration (new — from D10):**

- `BuildTools(ctx, group, agentKind) []anthropic.ToolParam` is called per inbound message.
- Returns the agent-appropriate slice. No global tool registry; no cached registry. Build cost is negligible.
- `expenses_summary` and `list_transactions` are appended to the main-agent slice iff `group.financial_enabled = true`. They are never present otherwise — not "present and refuses."

**System prompt construction (new):**

- Both agents have a `buildSystemPrompt(group, members) string` function that produces the prompt for the current call.
- Inputs: `groups` row (language, timezone, digest_hour, onboarding_state, name) + `members` rows.
- Onboarding prompt is bilingual until `groups.language` is set, then locked to chosen language.
- Main agent prompt is locked to `groups.language` permanently.

**Dispatcher (new — from D1):**

- `dispatch.go` exposes `Handle(ctx, group_id, sender_phone, text) error`.
- One rule: `groups.onboarding_state == "complete"` → main agent; otherwise → onboarding agent.
- Lookup happens on every inbound message; no caching. SQLite reads are <1ms.

**WhatsApp event filtering (new — from D7):**

- Inbound message handler in `internal/messenger/whatsapp.go`:
  1. Resolve group JID + sender from envelope.
  2. `isAllowlistedGroup(jid)` — fail-fast drop if not allowlisted.
  3. Auto-create `groups` row if absent (D12).
  4. Hand to dispatcher.
- Non-allowlisted: log-only with `group_id="<dropped>"` (no content); return.

### Process Patterns

**Migrations:**

- Run at process start, before HTTP mux is bound.
- Each migration function is idempotent (no-op if already applied per `schema_version`).
- Tenant-zero backfill (`migrate_002`) verifies post-condition: `SELECT COUNT(*) FROM <table> WHERE group_id IS NULL` returns 0 across `tasks`, `metadata`, `transactions`. If non-zero, migration aborts and process exits — fail-loudly.

**Conversation history:**

- Keyed by `(group_id, agent_kind)` (D5).
- 20-message cap applied per-key via existing `trimHistory`.
- On `complete_onboarding`: `delete(history, historyKey{group_id, "onboarding"})` (D6).

**Schedulers (D14):**

- One goroutine per scheduler kind (digest, nag).
- 5-minute ticker. Each tick: `SELECT id, timezone, digest_hour FROM groups WHERE onboarding_state = 'complete'`, iterate, check "is it time," send if so, record state in `metadata`.
- No per-group goroutines.

**Auto-create on first allowlisted message (D12):**

- Triggered by absence of `groups` row.
- Creates row with `onboarding_state="in_progress"`, `language=NULL`, `name=<whatsmeow group name>`, `financial_enabled=false`.
- Inserts allowlisted phones present in the WhatsApp group as `members` rows. Non-allowlisted phones are not inserted.

**Personal identifiers in tests:**

- Use `Alice` / `Bob` as display names; `100000000001` / `100000000002` as fake phones; `120363999999@g.us` as fake group JIDs.
- Real names, phones, and JIDs only in `.env` and `personas.md` (gitignored).

### Enforcement Guidelines

**All AI agents implementing this codebase MUST:**

1. **Pass `group_id` as the first parameter to every store method.** No exception, no convenience wrapper.
2. **Never expose `group_id` in any tool input schema sent to the LLM.** It must be derived server-side from the message envelope and injected via closure.
3. **Build the tool list per inbound message** via `BuildTools(group, agentKind)`. Never construct the tool list at process start or cache it.
4. **Conditionally register `expenses_summary` / `list_transactions`** based on `group.financial_enabled`. They must not appear in the schema for groups without financial access.
5. **Apply the dispatcher rule first** (`onboarding_state == "complete"` → main, else onboarding). No bypass paths.
6. **Drop non-allowlisted-group messages at the messenger layer.** No DB writes, no agent invocation, no message-content logs.
7. **Keep onboarding history out of the main agent's context window** — discard the onboarding `historyKey` entry on `complete_onboarding`.
8. **Use `slog` with a `group_id` attribute** on every tenant-scoped log line. No `log.Printf` in tenant-scoped paths.
9. **Verify zero-NULL post-conditions** in every migration that backfills `group_id` columns.
10. **Use `Alice`/`Bob` placeholders in tests.** No real names, phones, or JIDs in committed code.

### Anti-Patterns (forbidden)

- A `tenancy` package, a `Tenant` struct, or any abstraction whose only job is "wrap `group_id`."
- A "global" or "admin" tool that returns data across multiple groups (e.g., `list_all_groups`, `count_users`). Operator inspection happens via SSH + sqlite, not tools.
- Caching the allowlist per-join-event. Re-check on every message — friends drop in and out.
- Persisting onboarding history. It's discarded on completion.
- Calling the LLM with a tool list that includes `expenses_summary` for a group without `financial_enabled = true`.
- Using `metadata` keys with embedded group IDs (`last_digest_date:<jid>`). Use the `group_id` column.
- Per-group goroutines for schedulers.
- Auto-bumping `internal/version/version.go`. The user controls version manually.

## Project Structure & Boundaries

### Current Project Tree (existing, locked)

```
whatsapp-nagger/
├── CLAUDE.md
├── README.md
├── Dockerfile
├── .dockerignore
├── .env / .env.example
├── .gitignore
├── personas.md / personas.md.example       # gitignored, tenant-zero only
├── deploy.sh
├── entrypoint.sh
├── fly.toml
├── go.mod / go.sum
├── tasks.db / tasks.db-wal / tasks.db-shm  # SQLite (gitignored)
├── whatsapp_session.db                     # whatsmeow session (gitignored)
├── cmd/
│   └── nagger/
│       └── main.go                          # entrypoint: env load, mux, schedulers
├── internal/
│   ├── agent/
│   │   ├── agent.go                         # → renamed main_agent.go (D1)
│   │   └── agent_test.go
│   ├── api/                                 # dashboard auth (tenant-zero only)
│   │   ├── auth.go
│   │   ├── otp.go
│   │   └── router.go
│   ├── categories/
│   │   └── categories.go                    # transaction categorization
│   ├── db/
│   │   ├── db.go                            # tasks, metadata stores
│   │   ├── db_test.go
│   │   ├── transactions.go
│   │   └── transactions_test.go
│   ├── ingest/
│   │   ├── handler.go                       # /ingest/transactions HMAC (tenant-zero only, D9)
│   │   ├── hmac.go
│   │   ├── ingest_test.go
│   │   └── notify.go                        # /notify HMAC (tenant-zero only, D9)
│   ├── messenger/
│   │   ├── messenger.go                     # IMessenger interface
│   │   ├── messenger_test.go
│   │   ├── terminal.go                      # dev-mode messenger
│   │   └── whatsapp.go                      # prod messenger
│   └── version/
│       └── version.go                       # manual version, never auto-bumped
├── dashboard/                               # Next.js, tenant-zero only (D13)
│   ├── package.json
│   ├── tsconfig.json
│   ├── components.json
│   └── ...                                  # untouched in v1
├── scraper/                                 # TS sidecar, tenant-zero only
│   ├── package.json
│   ├── tsconfig.json
│   ├── run.sh
│   └── ...                                  # untouched in v1
└── _bmad-output/                            # planning artifacts
    └── planning-artifacts/
        ├── prd.md
        └── architecture.md
```

### Project Tree After Multi-Tenancy Cutover (additions/renames)

```
whatsapp-nagger/
├── Dockerfile                               # CHANGED: install sqlite3 + litestream (D16)
├── fly.toml                                 # CHANGED: litestream sidecar process (D16)
├── litestream.yml                           # NEW (D16)
├── cmd/
│   └── nagger/
│       └── main.go                          # CHANGED: invoke runMigrations; group-iterating schedulers (D14)
├── internal/
│   ├── agent/
│   │   ├── main_agent.go                    # RENAMED from agent.go (D1)
│   │   ├── main_agent_test.go               # RENAMED from agent_test.go
│   │   ├── onboarding_agent.go              # NEW (D1)
│   │   ├── onboarding_agent_test.go         # NEW
│   │   ├── dispatch.go                      # NEW: route by onboarding_state (D1)
│   │   ├── dispatch_test.go                 # NEW
│   │   ├── tools.go                         # NEW: BuildTools(group, agentKind) (D10)
│   │   └── history.go                       # NEW: per-(group_id, agent_kind) history map (D5/D6)
│   ├── db/
│   │   ├── db.go                            # CHANGED: every method takes group_id (D8)
│   │   ├── groups.go                        # NEW: groups + members stores (D4)
│   │   ├── groups_test.go                   # NEW
│   │   ├── migrations.go                    # NEW: runMigrations + numbered migrations (D3)
│   │   └── migrations_test.go               # NEW: idempotency + zero-NULL postcondition tests
│   └── messenger/
│       ├── whatsapp.go                      # CHANGED: drop groupJID field; allowlist + auto-create (D7/D12)
│       ├── messenger.go                     # CHANGED: Read returns GroupID; Write takes GroupID
│       └── terminal.go                      # CHANGED: emulate single fixed dev-group ID
└── docs/
    └── multi-tenancy-runbook.md             # NEW (optional): operator notes for migration day
```

**Files explicitly NOT touched in v1:**

- `internal/api/{auth,otp,router}.go` — dashboard auth stays tenant-zero only (D13).
- `internal/categories/categories.go` — unchanged.
- `internal/ingest/{handler,hmac,notify}.go` — `/ingest/transactions` and `/notify` endpoints stay tenant-zero only (D9). Tenant-zero rows still get `group_id` populated by the writer.
- `internal/version/version.go` — never auto-bumped.
- `dashboard/`, `scraper/` — entire directories untouched.

### Architectural Boundaries

**Network boundary (highest privilege gate):**

- WhatsApp inbound: `internal/messenger/whatsapp.go` — allowlist filter (D7) + auto-create (D12). Below this layer, no code path has any concept of "non-allowlisted." Every store call below this point assumes the `group_id` is for an allowlisted group.
- HTTP inbound: `cmd/nagger/main.go` mux — routes are unchanged in v1; `/ingest/transactions` and `/notify` HMAC-gated to tenant-zero scraper.

**Persistence boundary:**

- All data flows through `internal/db/` stores. Methods require `group_id` as first argument (D8). No raw SQL outside this package.
- `internal/db/groups.go` is the only file that may create/delete `groups` rows. Auto-create lives there as `groups.AutoCreate(ctx, jid, name, allowlistedPhones)`.
- `internal/db/migrations.go` is the only file that may execute schema changes; called once at process start.

**Agent boundary:**

- `internal/agent/dispatch.go` is the entry point from messenger to LLM logic. Only `dispatch.Handle(ctx, group_id, sender_phone, text)` is called externally.
- Main agent and onboarding agent never invoke each other; transitions happen via the dispatcher reading `groups.onboarding_state`.
- Tool handlers are constructed by `tools.BuildTools(group, agentKind)` per inbound message; closures capture `group_id`.

**Capability boundary:**

- Financial tools (`expenses_summary`, `list_transactions`) appear in the LLM tool schema only when `group.financial_enabled = true`. Enforced in `internal/agent/tools.go` (D10).
- `groups.financial_enabled` is set in DB only — no tool, no chat command, no env var override.

**Test isolation boundary:**

- Each test creates a fresh temp SQLite DB. No shared fixture file. Multi-tenancy tests use multiple `groups` rows within a single DB to verify scoping.

### Requirements → Structure Mapping

| Requirement | Lives in | Notes |
|---|---|---|
| **FR1 — Multi-tenancy schema** | `internal/db/migrations.go`, `internal/db/groups.go`, `internal/db/db.go`, `internal/db/transactions.go` | New tables in `migrations.go`; new stores in `groups.go`; existing stores gain `group_id` parameter. |
| **FR2 — Group-scoped task tools** | `internal/agent/tools.go`, `internal/agent/main_agent.go`, `internal/db/db.go` | Tool handlers in `tools.go`; main agent dispatches them; `update_task` extended for content+assignee. |
| **FR2 — Member management tools** | `internal/agent/tools.go`, `internal/db/groups.go` | `add_member`/`update_member`/`remove_member` handlers; member-cap and auto-reassign logic in store. |
| **FR3 — Conditional financial tools** | `internal/agent/tools.go`, `internal/db/transactions.go` | `BuildTools` checks `group.financial_enabled`; data layer always scoped. |
| **FR4 — Tenant-zero migration** | `internal/db/migrations.go` | `migrate_001_groups`, `migrate_002_backfill_tenant_zero`. Reads existing env vars + `personas.md` + `CARD_OWNERS`. |
| **FR5 — Per-group digest & nag** | `cmd/nagger/main.go`, `internal/db/db.go` (metadata store) | Schedulers iterate groups; `metadata` table extended with `group_id` + composite key. |
| **FR6 — Chat-driven onboarding** | `internal/agent/onboarding_agent.go`, `internal/agent/tools.go`, `internal/db/groups.go` | Onboarding agent + tools `set_language`/`set_member`/`set_timezone`/`set_digest_hour`/`complete_onboarding`. |

| Cross-Cutting Concern | Lives in | Notes |
|---|---|---|
| **Group scoping (NFR1)** | All `internal/` files | Compile-time enforced via store signatures (D8). |
| **Allowlist gating (NFR2)** | `internal/messenger/whatsapp.go` | Single enforcement point (D7). |
| **Member cap (NFR3)** | `internal/db/groups.go` | Enforced in `groups.AddMember` and `groups.AutoCreate`. |
| **Language & tone (NFR4)** | `internal/agent/main_agent.go`, `internal/agent/onboarding_agent.go` | `buildSystemPrompt(group, members)` per agent. |
| **Migration safety (NFR7)** | `internal/db/migrations.go` | Zero-NULL post-condition checks; abort-on-fail. |
| **Logging (D15)** | All `internal/` files | `slog.String("group_id", ...)` on every tenant-scoped log line. |

### Integration Points

**Internal communication:**

- `messenger.whatsapp.MessageHandler` → `agent.dispatch.Handle(group_id, sender, text)` → either `MainAgent.Handle` or `OnboardingAgent.Handle` (chosen by dispatcher).
- Both agents → `agent.tools.BuildTools` → tool handler closures → `db.*Store` methods.
- Schedulers (`cmd/nagger/main.go`) → `db.GroupStore.ListActive()` → for each group → `messenger.Write(group_id, text)` + `db.MetadataStore.Set(group_id, key, val)`.
- Onboarding completion: `OnboardingAgent.complete_onboarding` tool → `db.GroupStore.MarkComplete(group_id)` → `agent.history.Discard(group_id, "onboarding")`. Next message routes to main agent via dispatcher.

**External integrations (unchanged from v1 baseline):**

- WhatsApp Multi-Device API via `whatsmeow` (`whatsapp_session.db`).
- Anthropic Messages API via `anthropic-sdk-go`.
- Israeli bank scrapers (Cal, Max) via the TypeScript sidecar — pushes to `/ingest/transactions` HMAC.
- S3/R2 for Litestream replication (D16).

**Data flow (typical inbound message):**

```
WhatsApp → whatsmeow event → messenger.whatsapp handler
  ├─ allowlist check (drop if fail)
  ├─ groups.AutoCreate if absent
  └─ dispatch.Handle(group_id, sender, text)
       ├─ load groups row
       ├─ if onboarding_state != "complete":
       │    onboarding_agent.Handle → tools subset → groups/members store updates
       │    on complete_onboarding: state flip + history discard
       ├─ else:
       │    main_agent.Handle → tools subset (capability-gated) → tasks/groups/members/transactions store reads/writes
       │    → messenger.Write(group_id, response)
       └─ groups.UpdateLastActive(group_id)   ← D18: one UPDATE per handled message
```

### File Organization Patterns

**Configuration:**

- `.env` (gitignored), `.env.example` (committed). All structured config (`ALLOWED_PHONES`, `BILLING_DAY`, `CARD_OWNERS`, `TIMEZONE`, `MERCHANT_CONTEXT`, `DIGEST_HOUR`, `NAG_HOUR`, `NAG_THRESHOLD`) lives here. Per-group settings live in DB.
- `personas.md` (gitignored, tenant-zero only) — used by tenant-zero migration to backfill `members` rows; not consulted post-cutover.
- `fly.toml` — Fly app config. Adds Litestream sidecar process (D16).
- `litestream.yml` — replication targets and credentials (gitignored if it contains creds; otherwise referenced via env).

**Source layout (locked, see Implementation Patterns above):**

- Flat `internal/<domain>/` packages.
- Tests co-located.
- No service/repository ceremony.

**Test organization:**

- Unit tests next to code (`*_test.go`).
- Each test: temp SQLite DB, `Alice`/`Bob` placeholder names, `100000000001`-style fake phones, `120363999999@g.us`-style fake JIDs.
- Multi-tenancy tests assert scoping by creating two groups in one DB and verifying queries on group A return zero rows for group B.

### Development Workflow Integration

**Local dev:**

- `go run ./cmd/nagger` → terminal mode, fake group ID. `MESSENGER=whatsapp` for WhatsApp mode.
- `go test ./...` exercises full DB layer with real SQLite.
- Scraper sidecar in `scraper/` runs independently via `npm start`.
- Dashboard in `dashboard/` runs independently via `npm run dev`.

**Build process:**

- `Dockerfile` builds the Go binary, installs `sqlite3` + `litestream`, copies entrypoint.
- `entrypoint.sh` runs Litestream restore (if a previous backup exists) and then exec's the binary.
- `deploy.sh` invokes `fly deploy`.

**Deployment structure:**

- Single Fly app, single VM, one persistent volume mounted at the SQLite path.
- `litestream.yml` continuously replicates `tasks.db` to S3/R2.
- `whatsapp_session.db` lives on the same volume; not replicated by Litestream in v1 (regenerated by re-pairing if lost — acceptable risk).

## Architecture Validation Results

### Coherence Validation ✅

**Decision Compatibility:**

- D1 (two-agent split) ↔ D5/D6 (per-key history) ↔ D10 (per-message tool registry): mutually reinforcing. Onboarding agent's tool surface and history are disjoint from main agent's by construction, not by runtime check.
- D2 (stay on SQLite) ↔ D3 (migration framework) ↔ D16 (Litestream + sqlite3 in image): internally consistent. SQLite-as-only-DB is supported by hand-rolled versioned migrations and continuous replication for point-in-time recovery.
- D7 (allowlist at messenger layer) ↔ D12 (auto-create on first allowlisted message) ↔ D17 (preserve on remove): compose into a single network/lifecycle boundary. Below this boundary no code path has any concept of "non-allowlisted" or "removed."
- D8 (group_id as first parameter) ↔ D10 (per-message tool build with closures) ↔ NFR1 (hard data isolation): structural enforcement. Tools never see group_id; closures inject it; stores require it.
- D14 (single-ticker group-iterating schedulers) is consistent with NFR8 (flat code) — no per-group goroutine sprawl.
- D17/D18 fit the existing isolation philosophy: D17 preserves data without exposing operator inspection through tools; D18 is observability-only, never surfaced to the LLM.
- All-financial-data-scoped (init refinement) ↔ D4 (groups.id = JID) ↔ D9 (single-tenant ingest endpoints): the `transactions` table gains group_id; tenant-zero rows backfilled; future per-group ingest is a routing change.

**Pattern Consistency:**

- Naming patterns align with all decisions. Migration function naming (`migrate_NNN_*`) supports D3. Tool naming (`snake_case` verb-object) supports D10. Logging attribute names (`group_id` literal) support D15.
- Format patterns reinforce decisions. Onboarding state representation (`in_progress` / `complete`, granular sub-state implicit) supports D1's dispatcher rule. Per-group metadata composite key (no string concatenation) supports D8's clean store signatures.
- Process patterns enforce post-conditions. Migration zero-NULL check supports NFR7. History discard on `complete_onboarding` supports D6.

**Structure Alignment:**

- Project tree reflects every architectural component. `internal/agent/` split matches D1. `internal/db/migrations.go` matches D3. `internal/db/groups.go` matches D4 + D12 + D17 + D18. Capability gating lives in `internal/agent/tools.go` per D10.
- Boundaries are explicit (network, persistence, agent, capability, test isolation) and align with the enforcement rules in patterns.
- Files explicitly NOT touched (dashboard, scraper, /ingest endpoints, version) match D9, D11, D13.

### Requirements Coverage Validation ✅

**Functional Requirements:**

- **FR1 — Multi-tenancy schema:** Covered by D3 (migration framework), D4 (groups.id = JID), D8 (group_id parameter), D18 (last_active_at), and the structure mapping.
- **FR2 — Group-scoped agent tools:** Covered by D8 (store signatures), D10 (tool registry), and the structure mapping for `tools.go` + `main_agent.go`. `update_task` extension to content+assignee is in scope.
- **FR3 — Conditional financial tools:** Covered by D10 (capability gate in BuildTools) plus the all-financial-data-scoped refinement at the data layer.
- **FR4 — Tenant-zero migration:** Covered by D3 (numbered migrations), the zero-NULL post-condition pattern, D18 backfill of `last_active_at = created_at`, and `migrations.go` in the structure.
- **FR5 — Per-group daily digest & nag:** Covered by D14 (group-iterating schedulers) and the per-group metadata format pattern.
- **FR6 — Chat-driven onboarding:** Covered by D1 (separate agent), D6 (history discard on completion), the onboarding-state format pattern, and `onboarding_agent.go` in the structure.

**Non-Functional Requirements:**

- **NFR1 — Hard data isolation:** Covered by D8 (compile-time enforcement via store signatures) — the strongest possible mechanism. Reinforced by D18's "not exposed via tools" note.
- **NFR2 — Allowlist gating:** Covered by D7 (messenger-layer enforcement) plus the "checked every message, not cached" rule in patterns.
- **NFR3 — Member cap:** Covered by member-cap enforcement in `groups.AddMember` and `groups.AutoCreate` (structure mapping).
- **NFR4 — Tone & language:** Covered by per-agent `buildSystemPrompt(group, members)`; locked-language enforced by the `groups.language IS NULL` → onboarding rule.
- **NFR5 — Persistence single source:** Covered by D2 (SQLite stays) and the no-config-fallback pattern.
- **NFR6 — Conversation history cap:** Covered by D5 (per-key map) + existing `trimHistory` applied per-key.
- **NFR7 — Migration safety:** Covered by D3 (idempotent runner via `schema_version`) + zero-NULL postcondition pattern.
- **NFR8 — Flat code style:** Covered by structure (no `tenancy/` package) and the anti-patterns list.
- **NFR9 — Privacy of identifiers:** Covered by `Alice`/`Bob` placeholder pattern and gitignored `.env`/`personas.md`.

### Implementation Readiness Validation ✅

**Decision Completeness:** All 18 decisions (D1–D18) have rationale, file impact, and dependency notes. No decision is left as "TBD."

**Structure Completeness:** Every requirement maps to a specific file. Every cross-cutting concern has a single enforcement location named. No undefined integration points.

**Pattern Completeness:** Naming, structure, format, communication, and process patterns are documented. Enforcement guidelines are mandatory and enumerated. Anti-patterns are explicit.

### Gap Analysis Results

**Critical Gaps:** None.

**Resolved during validation:**

- ~~Bot-removed-from-group behavior undefined~~ → **D17** (preserve data, no-op on remove).
- ~~`groups.last_active_at` column missing~~ → **D18** (added to schema + dispatcher updates).

**Accepted by design (non-issues):**

- **Partial-onboarding history loss on process restart.** Not a regression vs single-tenant. The `groups` row + populated fields persist, so onboarding resumes from the implicit state-machine perspective; only conversational narrative is lost. No fix in v1.

**Minor Gaps (defer or document):**

- Litestream snapshot cadence + bucket choice (D16) — operational sub-decision, not architectural.
- Operational flow for flipping `groups.financial_enabled` (manual SQL) — one-paragraph runbook note.

### Validation Issues Addressed

All gaps above are non-blocking and either resolved (D17, D18) or accepted by design. None require revision to D1–D16, the patterns, or the structure beyond the D17/D18 additions.

### Architecture Completeness Checklist

**Requirements Analysis**

- [x] Project context thoroughly analyzed
- [x] Scale and complexity assessed
- [x] Technical constraints identified
- [x] Cross-cutting concerns mapped

**Architectural Decisions**

- [x] Critical decisions documented with versions (D1–D18; brownfield, no version churn)
- [x] Technology stack fully specified (inherited; no new runtime deps)
- [x] Integration patterns defined
- [x] Performance considerations addressed (per-message tool build cost, scheduler tick interval, D18 single-UPDATE-per-message cost)

**Implementation Patterns**

- [x] Naming conventions established
- [x] Structure patterns defined
- [x] Communication patterns specified
- [x] Process patterns documented

**Project Structure**

- [x] Complete directory structure defined
- [x] Component boundaries established
- [x] Integration points mapped
- [x] Requirements to structure mapping complete

### Architecture Readiness Assessment

**Overall Status:** READY FOR IMPLEMENTATION

**Confidence Level:** High

**Key Strengths:**

- **Compile-time enforcement of the hardest constraint.** `group_id` as required first parameter on every store method makes cross-group leakage a build error, not a runtime failure.
- **Two-agent split eliminates state-machine leakage.** Capability-defined tool surfaces beat conditional registration; less surface area for an agent to misbehave.
- **"Not present" semantics for capability gating.** Financial tools don't appear in the schema for non-financial groups — stronger than "present and refuses."
- **Brownfield-honest scope.** No fake starter, no DB platform swap, no premature dashboard extension. Scope matches PRD precisely.
- **Single-ticker schedulers.** Flat structure, no goroutine sprawl, scales gracefully past 10 groups.
- **All-financial-data scoped uniformly.** No exception clause; the entire data model is tenant-aware even where v1 only uses tenant zero.
- **Operational visibility added without exposing it via the LLM (D18).** Operator queries `last_active_at` via SSH + sqlite, not through any tool.
- **Lifecycle resilience (D17).** Bot removal is a no-op; re-add resumes seamlessly. Friend groups can reorganize without losing state.

**Areas for Future Enhancement:**

- Per-group dashboard surface (when/if friend groups want it).
- Per-group ingest endpoints (when/if friends want their own scrapers).
- Postgres migration (only if group count or HA needs trigger).
- Operator-facing TUI/admin endpoint for `financial_enabled` flips, group inspection (currently SSH + sqlite).

### Implementation Handoff

**AI Agent Guidelines:**

- Follow D1–D18 exactly. Do not introduce a `tenancy` package or any abstraction listed in the anti-patterns.
- Use the implementation sequence (step 4) as the merge order. D3 (migration framework) lands first; everything else follows the documented dependencies. D17 is a documentation-only decision (no-op handler); D18 lands with `migrate_001_groups`.
- Refer to the Requirements → Structure table for every story. If a story doesn't map cleanly to a file, escalate before implementing.
- Compile-time enforcement is non-negotiable: if a code path can call a store method without `group_id`, that's a bug.

**First Implementation Priority:**

`migrate_001_groups` — create `groups`, `members`, and `schema_version` tables (with `last_active_at` on `groups`), plus add `group_id` columns to `tasks`, `metadata`, `transactions`. No backfill yet; `migrate_002` handles tenant-zero backfill (including `last_active_at = created_at` for the existing row) in a separate, atomic step.
