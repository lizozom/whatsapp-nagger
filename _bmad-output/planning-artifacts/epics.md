---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - _bmad-output/planning-artifacts/prd.md
  - _bmad-output/planning-artifacts/architecture.md
  - _bmad-output/project-context.md
---

# whatsapp-nagger — Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for `whatsapp-nagger`, decomposing the requirements from the PRD and the architecture decisions (D1–D18) into implementable stories. The work is the multi-tenancy rebuild — extending the bot to serve up to ~10 friend groups from a single Fly deploy while preserving Liza's tenant-zero family group identically.

## Requirements Inventory

### Functional Requirements

**FR1 — Multi-tenancy schema:** `group_id` (WhatsApp JID) on `tasks`, `metadata`, conversation history, and `transactions`. New `groups` table (`id`, `name`, `language`, `timezone`, `digest_hour`, `onboarding_state`, `financial_enabled`, `created_at`, `last_active_at`) and `members` table (`group_id`, `whatsapp_id`, `display_name`, `created_at`).

**FR2 — Group-scoped agent tools:** Existing task tools (`add_task`, `list_tasks`, `delete_task`, `update_task`) scoped server-side by `group_id`. `update_task` extended to accept optional `content` and `assignee` (in addition to existing `status` and `due_date`). New tools: `get_group_settings`, `update_group_settings`, `add_member`, `update_member`, `remove_member`. Member cap (1–2) enforced in `add_member` and onboarding. On `remove_member`, open tasks assigned to the removed person auto-reassigned to the remaining member; refused if the group would become empty.

**FR3 — Conditional financial tools:** `expenses_summary` and `list_transactions` registered into the LLM tool schema only when the calling group's `financial_enabled = true`. Data layer always scopes by `group_id` regardless. The `groups.financial_enabled` flag is set in DB only — no tool/chat/env path can flip it.

**FR4 — Tenant-zero migration:** Idempotent migration that creates `groups`, `members`, and `schema_version` tables; backfills the tenant-zero `groups` row from existing env vars (`WHATSAPP_GROUP_JID`, `TIMEZONE`, `DIGEST_HOUR`, `BILLING_DAY`); backfills tenant-zero `members` rows from `personas.md` and `CARD_OWNERS`; populates `group_id` on existing `tasks`, `metadata`, and `transactions` rows. Reversible enough to test locally. Verifies `SELECT COUNT(*) WHERE group_id IS NULL = 0` across all scoped tables before completing.

**FR5 — Per-group daily digest and nag DMs:** Each group's daily digest fires at its configured `digest_hour` in its `timezone`. Nag DMs fire to assignees whose pending overdue tasks exceed `NAG_THRESHOLD`. `metadata.last_digest_date` and `last_nag_date` become per-group keys (composite `(group_id, key)`).

**FR6 — Chat-driven onboarding:** When the bot is added to a group containing at least one allowlisted phone, and the group has not yet been onboarded, the warm-toned onboarding agent walks the group through: bilingual language pick (`עברית או English?`) → solo or couple → member name(s) and WhatsApp ID(s) → timezone → digest hour → confirm captured config. Onboarding completes when all required fields are captured; subsequent messages route to the main agent in the locked language. Abandoned onboarding resumes from the last unanswered question on next message.

### NonFunctional Requirements

**NFR1 — Hard data isolation:** Every DB query takes `group_id`. No tool returns cross-group data. `group_id` is never in the LLM tool schema — derived server-side from the message envelope and injected via closure. Operator (Liza) gets no admin back-door through the agent.

**NFR2 — Allowlist gating:** `ALLOWED_PHONES` env (comma-separated, international format, no `+`). Bot operates in a group iff at least one allowlisted phone is a member. Checked every message (not cached at join). Non-allowlisted groups: completely silent — no reply, no leave, no presence beyond joining.

**NFR3 — Member cap:** 1 ≤ `members` ≤ 2 per group. Enforced in `add_member` and onboarding. Rejecting "we have three kids" is acceptable v1 behavior.

**NFR4 — Tone & language:** Bilingual onboarding prompt; `groups.language` (`he` or `en`) locked permanently at first pick. Warm/welcoming tone during onboarding, sarcastic Israeli-engineer persona afterwards. Both shape the per-message system prompt. All replies, including digest format, respect `groups.language`.

**NFR5 — Persistence:** SQLite single source of truth. No config-file fallback for per-group settings. `groups.financial_enabled` flips happen in DB only, never via tool/chat.

**NFR6 — Conversation history cap:** 20 messages, per-group, per-agent (main vs onboarding). Existing `trimHistory` algorithm (preserves leading user message, no orphan tool_results) preserved verbatim, applied per-key.

**NFR7 — Migration safety:** Idempotent. Verifies `SELECT COUNT(*) WHERE group_id IS NULL = 0` across `tasks`, `metadata`, `transactions` before completing. Process exits on failure.

**NFR8 — Code style:** Flat, minimal abstraction. No `tenancy` package. Add `group_id` parameters and a `groups` store; that's it.

**NFR9 — Privacy of identifiers:** Personal identifiers (real names, phones, JIDs, last4s) only in gitignored files (`.env`, `personas.md`). Tests use `Alice`/`Bob` placeholders, fake phones (`100000000001`-style), fake JIDs (`120363999999@g.us`-style).

### Additional Requirements

(Derived from Architecture decisions D1–D18 — these shape implementation; some directly become stories.)

- **D1 — Two-agent split:** Onboarding is a separate agent type (`internal/agent/onboarding_agent.go`), not a mode of the main agent. Tool surfaces, system prompts, and per-key conversation history are physically disjoint. A small `dispatch.go` routes by `groups.onboarding_state` on every inbound message.
- **D2 — Stay on SQLite for v1:** No migration to Postgres. Pure-Go `modernc.org/sqlite` driver retained.
- **D3 — Hand-rolled migration framework:** `schema_version` table + numbered Go migration functions (`migrate_NNN_description`). No `golang-migrate` or `goose` dependency. Migrations run at process start, before HTTP mux is bound.
- **D4 — `groups.id = WhatsApp JID`:** No surrogate primary key. Foreign keys (`tasks.group_id`, `members.group_id`, `metadata.group_id`, `transactions.group_id`) all use the JID string. Every `group_id` column indexed.
- **D5 — Conversation history in-memory:** Keyed by `(group_id, agent_kind)`. Replaces single `[]MessageParam` with `map[historyKey][]MessageParam`. 20-message cap applied per-key. Lost on process restart (acceptable).
- **D6 — Discard onboarding history on completion:** On `complete_onboarding` tool call, delete the onboarding history map entry for that group. Main agent starts fresh.
- **D7 — Allowlist enforcement at messenger layer:** `internal/messenger/whatsapp.go` is the single enforcement point. `isAllowlistedGroup(jid)` runs before any DB write or agent invocation. Non-allowlisted log lines emit `group_id="<dropped>"` with no message content.
- **D8 — `group_id` derivation rule:** Tool input schemas never include `group_id`. Per-message: closure-captures `group_id` from the message envelope. Store interfaces require `group_id` as first parameter — compile-time enforcement.
- **D9 — Existing security surfaces unchanged:** `/ingest/transactions` HMAC, dashboard JWT/OTP, `/notify` HMAC, `/pair` — all stay tenant-zero only or unchanged. No `group_id` parameters added in v1.
- **D10 — Tool registry built per-message:** `BuildTools(ctx, group, agentKind) []anthropic.ToolParam` invoked per inbound message. Returns agent-appropriate slice. Capability-gated: `expenses_summary` / `list_transactions` only included when `group.financial_enabled = true`.
- **D11 — HTTP layer unchanged:** Stdlib `net/http`, single mux in `cmd/nagger/main.go`. No new routes for v1.
- **D12 — Group auto-create on first allowlisted message:** Inbound message from a JID with no `groups` row + at least one allowlisted phone in the WhatsApp group → create row with `onboarding_state="in_progress"`, `language=NULL`, `name=<whatsmeow group name>`, `financial_enabled=false`. Insert allowlisted phones present in the group as `members` rows.
- **D13 — Dashboard untouched in v1:** Next.js dashboard, OTP/JWT auth, schema all unchanged.
- **D14 — Group-iterating schedulers:** One goroutine per scheduler kind (digest, nag), 5-minute scan tick, iterates `groups WHERE onboarding_state = 'complete'`, sends if hour reached and not yet sent today.
- **D15 — Structured logging with `slog`:** `slog.String("group_id", ...)` on every tenant-scoped log line. Replace `log.Printf` calls in tenant-scoped paths.
- **D16 — Litestream + `sqlite3` in Fly image:** Dockerfile installs both; `litestream.yml` replicates `tasks.db` to S3/R2; `fly.toml` adds Litestream sidecar. Independent infra change.
- **D17 — Bot-removed lifecycle:** Remove event is a no-op. Data preserved. Re-add resumes via existing `groups` row, no re-onboarding.
- **D18 — `groups.last_active_at`:** ISO-8601 TEXT column. Updated by `dispatch.Handle` after each successful inbound message processing. Tenant-zero migration backfills `last_active_at = created_at`.
- **All financial data scoped (init refinement):** `transactions` table gains `group_id` column; tenant-zero rows backfilled; tools always filter by `group_id` server-side.

**Starter Template:** N/A — brownfield project. No `npx create-*` or equivalent invocation. Implementation is surgical changes to existing modules (`internal/db`, `internal/messenger`, `internal/agent`, `cmd/nagger`) plus internal additions to `internal/agent/` for the two-agent split.

**Files explicitly NOT touched in v1:** `internal/api/{auth,otp,router}.go`, `internal/categories/categories.go`, `internal/ingest/{handler,hmac,notify}.go`, `internal/version/version.go`, entire `dashboard/` and `scraper/` directories.

### UX Design Requirements

None. UX work is intentionally out of scope for v1 (D13 defers dashboard changes; no new UI surface). The conversational UX in WhatsApp is specified by FR6 and NFR4 (tone & language) and does not require a separate UX-DR enumeration.

### FR Coverage Map

| FR | Epic | Notes |
|---|---|---|
| **FR1** — Multi-tenancy schema | Epic 1 | Migration framework + groups/members tables + `group_id` columns including `last_active_at` (D4, D18 schema) |
| **FR2** — Group-scoped agent tools | Epic 1 + Epic 2 | Epic 1 establishes `group_id`-first store interfaces (compile-time enforcement of NFR1). Epic 2 builds the tool surface: task tools, `get_group_settings`/`update_group_settings`, `add_member`/`update_member`/`remove_member`, plus `update_task` extension to `content`+`assignee`. |
| **FR3** — Conditional financial tools | Epic 2 | Capability-gated `BuildTools` registration based on `group.financial_enabled` (D10) |
| **FR4** — Tenant-zero migration | Epic 1 | Idempotent backfill of `groups`, `members`, and `group_id` columns on `tasks`/`metadata`/`transactions` from existing env vars + `personas.md` + `CARD_OWNERS`. Zero-NULL postcondition. |
| **FR5** — Per-group digest & nag | Epic 2 | Group-iterating schedulers (single ticker, 5-min scan) with per-group `last_digest_date` / `last_nag_date` keyed by composite (group_id, key) (D14) |
| **FR6** — Chat-driven onboarding | Epic 2 | Onboarding agent (D1) with `set_language`/`set_member`/`set_timezone`/`set_digest_hour`/`complete_onboarding` tools; bilingual prompt then locked language; warm tone; resumable from last unanswered question. |

**NFR coverage:**

- **NFR1** (hard isolation) — Epic 1 (compile-time enforcement via store signatures, D8)
- **NFR2** (allowlist gating) — Epic 2 (messenger-layer enforcement, D7)
- **NFR3** (member cap 1–2) — Epic 2 (enforced in `add_member` and onboarding)
- **NFR4** (tone & language) — Epic 2 (per-agent `buildSystemPrompt`)
- **NFR5** (SQLite single source) — Epic 1 architectural commitment (D2); no work, just a guardrail
- **NFR6** (history cap, per-key) — Epic 2 (per-(group_id, agent_kind) history map, D5)
- **NFR7** (migration safety) — Epic 1 (zero-NULL postcondition in tenant-zero backfill story)
- **NFR8** (flat code style) — Constraint applied throughout both epics
- **NFR9** (privacy of identifiers) — Constraint applied in all test stories (Alice/Bob, fake phones, fake JIDs)

**Architecture guardrails (no work, referenced for non-regression):** D2 (stay on SQLite), D9 (existing security surfaces unchanged), D11 (HTTP layer unchanged), D13 (dashboard untouched).

## Epic List

### Epic 1: Operational Safety & Tenant-Zero Preservation

**User outcome:** Liza-as-operator has continuous DB backup and SSH-queryable prod state. Liza-as-user keeps every existing capability through the multi-tenancy schema cutover with zero perceived behavior change. The codebase emerges with `group_id` enforced as a compile-time invariant on every store method, ready for friend groups in Epic 2.

**FRs covered:** FR1, FR4, FR2 (partial — store layer; tools come in Epic 2)

**Architecture decisions implemented:** D2 (commitment), D3 (migration framework), D4 (groups.id = JID), D8 (store signatures), D16 (Litestream + sqlite3), D18 (schema column + tenant-zero backfill).

**Implementation notes:**

- D16 (Litestream + sqlite3 in Fly image) lands first as a prerequisite for safely running the tenant-zero migration in prod with rollback capability.
- D3 (migration framework) is the second prerequisite — nothing else compiles cleanly without it.
- Stories within Epic 1 share the same core files (`internal/db/*`, `cmd/nagger/main.go`) — file-overlap by design, ordered as a dependency chain.
- The single highest-stakes story is the tenant-zero backfill migration with the zero-NULL postcondition; all other stories enable it or follow from it.
- D15 (`slog` discipline) folded as acceptance criteria for any new logging within these stories.

### Epic 2: Multi-Tenant Bot Operation

**User outcome:** Liza or a friend can add the bot to a new WhatsApp group containing an allowlisted member, complete chat-driven onboarding in their chosen language (Hebrew or English), and operate the same task / digest / nag features Liza's group has — fully isolated from every other group's data and conversation. Liza's family group continues working identically through the cutover.

**FRs covered:** FR2 (full tool surface), FR3, FR5, FR6

**Architecture decisions implemented:** D1 (two-agent split), D5 (per-key history), D6 (history discard on completion), D7 (allowlist gating), D9 (guardrail honored), D10 (per-message tool registry), D11 (guardrail honored), D12 (auto-create), D13 (guardrail honored), D14 (group-iterating schedulers), D17 (bot-removed lifecycle), D18 (dispatcher hook for `last_active_at`). D15 (`slog`) folded as acceptance criteria across all stories.

**Implementation notes:**

- All stories share `internal/agent/*` and `internal/messenger/*` — file-overlap by design, consolidated as the architecture's "agent layer reshape." Each story extends the same component end-to-end.
- The two-agent dispatcher is the central pivot — earlier stories prepare its inputs (allowlist + auto-create), later stories build on it (tools + schedulers + onboarding).
- Integration test for this epic = the PRD success criterion: "a second test group can be onboarded end-to-end via chat alone in under 5 minutes."
- Bot-removed lifecycle (D17) lands as a small explicit story to ensure it's not forgotten — barely a story, but it gets acceptance criteria ("leave event triggers no DB writes; preserves data; re-add resumes via existing row").

## Epic 1: Operational Safety & Tenant-Zero Preservation

**Goal:** Liza-as-operator gets continuous DB backup and SSH-queryable prod state. Liza-as-user keeps every existing capability through the multi-tenancy schema cutover with zero perceived behavior change. The codebase emerges with `group_id` enforced as a compile-time invariant on every store method, ready for friend-group work in Epic 2.

### Story 1.1: Continuous SQLite backup via Litestream and queryable prod DB

As Liza-the-operator,
I want continuous SQLite replication to S3/R2 plus the `sqlite3` CLI in the Fly image,
So that I can recover from data loss and query prod DB state via `fly ssh` before/after risky migrations.

**Acceptance Criteria:**

**Given** the Fly image is rebuilt with the updated `Dockerfile`
**When** the bot deploys
**Then** the running container has both `sqlite3` and `litestream` binaries available on `$PATH`
**And** `fly ssh -C "sqlite3 /data/tasks.db 'SELECT COUNT(*) FROM tasks'"` returns a numeric count

**Given** Litestream is configured via `litestream.yml` with an S3/R2 replica
**When** a write occurs to `tasks.db`
**Then** the change is replicated to the configured bucket within Litestream's normal interval
**And** `litestream restore -o /tmp/restored.db <replica_url>` produces a DB equivalent to the prod state at the snapshot point

**Given** the bot starts via `entrypoint.sh`
**When** the persistent volume contains no `tasks.db` but a Litestream replica exists in the bucket
**Then** `litestream restore` runs before the bot starts
**And** the restored DB is used for the bot session

**Given** D15 logging discipline
**When** new log lines are added in this story
**Then** they use `slog` with structured fields

### Story 1.2: Versioned schema migration framework

As a dev agent maintaining the bot,
I want a hand-rolled migration runner backed by a `schema_version` table,
So that future schema changes can be expressed as numbered, idempotent Go functions and applied automatically at process start.

**Acceptance Criteria:**

**Given** a fresh `tasks.db` without any tables
**When** `runMigrations(db)` is invoked
**Then** a `schema_version (id INTEGER PRIMARY KEY, applied_at TEXT)` table is created
**And** no application schema is touched (no migrations are registered yet in this story)

**Given** `runMigrations(db)` is invoked at process start
**When** a migration with id `N` has not yet been recorded in `schema_version`
**Then** that migration's Go function is executed
**And** an `INSERT INTO schema_version (id, applied_at) VALUES (N, ?)` records its application

**Given** `runMigrations(db)` is invoked twice in succession
**When** all migrations have already been applied
**Then** the second invocation is a no-op (no SQL writes apart from the read of `schema_version`)
**And** the function returns nil

**Given** `cmd/nagger/main.go` startup
**When** the process begins initialization
**Then** `runMigrations(db)` is called *before* the HTTP mux is bound and *before* schedulers start
**And** if migrations fail, the process exits with a non-zero code and a `slog.Error` line including the migration id

### Story 1.3: Multi-tenancy schema (`migrate_001_groups`) and base GroupStore

As a dev agent enabling multi-tenancy work,
I want the `groups` and `members` tables created and `group_id` columns added (nullable) to existing scoped tables, plus a basic `GroupStore` for use by future stories,
So that schema is in place but no data is yet relocated, allowing the backfill story to run as a separate atomic step.

**Acceptance Criteria:**

**Given** `runMigrations(db)` has run with `migrate_001_groups` registered
**When** the migration completes
**Then** the `groups` table exists with columns `id TEXT PRIMARY KEY, name TEXT, language TEXT, timezone TEXT, digest_hour INTEGER, onboarding_state TEXT, financial_enabled INTEGER, created_at TEXT, last_active_at TEXT`
**And** the `members` table exists with columns `group_id TEXT NOT NULL, whatsapp_id TEXT NOT NULL, display_name TEXT, created_at TEXT, PRIMARY KEY (group_id, whatsapp_id)`
**And** `tasks`, `metadata`, `transactions` each have a nullable `group_id TEXT` column added
**And** every `group_id` column has an index

**Given** the migration has applied
**When** existing tenant-zero rows are inspected
**Then** `tasks.group_id`, `metadata.group_id`, `transactions.group_id` are all `NULL` (backfill happens in Story 1.4)
**And** existing tenant-zero behavior is unchanged (queries that don't filter by `group_id` still work as before)

**Given** `internal/db/groups.go` is added
**When** the package compiles
**Then** `GroupStore` exposes at least `Get(ctx, group_id) (*Group, error)`, `Create(ctx, group Group) error`, `UpdateLastActive(ctx, group_id) error`, `MarkComplete(ctx, group_id) error`
**And** `MemberStore` exposes at least `List(ctx, group_id) ([]Member, error)`, `Add(ctx, group_id, m Member) error`
**And** unit tests in `internal/db/groups_test.go` cover create + get + list using a temp SQLite DB with `Alice`/`Bob` placeholder names, fake phones (`100000000001`-style), and fake JIDs (`120363999999@g.us`-style) per NFR9

### Story 1.4: Tenant-zero backfill migration (`migrate_002_backfill_tenant_zero`)

As Liza-the-user,
I want my existing family group's tasks, metadata, and transactions populated with the tenant-zero `group_id`,
So that the codebase can enforce `group_id`-scoped queries without my data effectively becoming inaccessible.

**Acceptance Criteria:**

**Given** the bot is configured with `WHATSAPP_GROUP_JID`, `TIMEZONE`, `DIGEST_HOUR`, `BILLING_DAY`, `CARD_OWNERS` env vars and `personas.md`
**When** `migrate_002_backfill_tenant_zero` runs
**Then** a `groups` row is created with `id = <WHATSAPP_GROUP_JID>`, `name = "tenant-zero"` (or env-derived), `language = "he"` (or env-derived), `timezone = <TIMEZONE>`, `digest_hour = <DIGEST_HOUR>`, `onboarding_state = "complete"`, `financial_enabled = 1`, `created_at = <now>`, `last_active_at = created_at`
**And** `members` rows are created from `personas.md` and `CARD_OWNERS` (one row per name → phone mapping; deduplicated)

**Given** the backfill has run
**When** SQL `SELECT COUNT(*) FROM tasks WHERE group_id IS NULL` is executed
**Then** the count is 0
**And** the same is true for `metadata` and `transactions`

**Given** the backfill has run
**When** SQL `SELECT COUNT(*) FROM tasks WHERE group_id != <WHATSAPP_GROUP_JID>` is executed
**Then** the count is 0
**And** the same is true for `metadata` and `transactions`

**Given** the migration is mid-execution and any verification step fails
**When** `migrate_002_backfill_tenant_zero` returns
**Then** the error returned causes `runMigrations` to fail
**And** the process exits with a non-zero code and a `slog.Error` line naming the failing post-condition

**Given** the migration is run twice (idempotency)
**When** `runMigrations(db)` is invoked a second time
**Then** the second invocation is a no-op (no duplicate `groups` rows, no duplicate `members` rows, no re-backfill SQL)
**And** the function returns nil

**Given** local testability (NFR7)
**When** a dev runs `go test ./internal/db/...`
**Then** a test exercises `migrate_001_groups` + `migrate_002_backfill_tenant_zero` against a temp SQLite DB pre-populated with mock tenant-zero `tasks`/`metadata`/`transactions` rows + a mock `personas.md` + env vars set via `t.Setenv`
**And** all post-conditions above are asserted in tests

### Story 1.5: Group-scoped store signatures across the codebase

As a dev agent maintaining the bot's data layer,
I want every public method on `TaskStore`, `TransactionStore`, and `MetadataStore` to require `group_id` as its first parameter,
So that cross-group data leakage becomes a compile-time error rather than a runtime check.

**Acceptance Criteria:**

**Given** the data layer is being refactored
**When** `internal/db/db.go` and `internal/db/transactions.go` are updated
**Then** every method on `TaskStore` (`Add`, `List`, `Update`, `Delete`, `CountOverdueByAssignee`, etc.) takes `group_id string` as the first non-receiver, non-context parameter
**And** every method on `TransactionStore` and `MetadataStore` does the same
**And** internal SQL `WHERE` clauses include `AND group_id = ?` on every query that touches a scoped table
**And** SQL writes (`INSERT`, `UPDATE`) populate `group_id` from the parameter

**Given** the refactor lands
**When** the existing tenant-zero callers are updated
**Then** callers in `internal/agent/*` pass the tenant-zero JID (read from `WHATSAPP_GROUP_JID` env, cached at startup) as the first argument
**And** callers in `cmd/nagger/main.go` (schedulers) pass the same tenant-zero JID
**And** callers in `internal/ingest/handler.go` (the `/ingest/transactions` endpoint) pass the tenant-zero JID when inserting `transactions` rows
**And** callers in `internal/api/{auth,otp,router}.go` (dashboard) pass the tenant-zero JID where they read `tasks` or `transactions` (per D13 the dashboard stays tenant-zero-only; this is a compile-time fix-up, not a behavior change)

**Given** the refactored stores and updated callers
**When** `go build ./...` is run
**Then** the build succeeds without warnings
**And** `go test ./...` passes — all existing tests are updated to pass `group_id`, no test was deleted or skipped

**Given** the refactored stores are deployed
**When** Liza interacts with the bot in her family group as before
**Then** every existing capability works identically: add/list/update/delete tasks, expenses summary, transaction list, daily digest, nag DMs, dashboard magic-link, scraper alert via `/notify`, scraper push via `/ingest/transactions`
**And** zero behavior change is perceived

**Given** an attempt to write a code path that calls a store method without `group_id`
**Then** the code does not compile (no method exists with the old signature)

**Given** D15 logging discipline
**When** any log line in the touched files emits during this story's work
**Then** it uses `slog` with `slog.String("group_id", ...)` if it pertains to a tenant-scoped operation

### Story 1.6: `IMessenger` group-aware refactor

As a dev agent preparing for multi-group operation,
I want the `IMessenger` interface to expose `group_id` on inbound messages and as a parameter on outbound writes,
So that downstream callers can route messages by group without the messenger leaking single-group assumptions.

**Acceptance Criteria:**

**Given** `internal/messenger/messenger.go` defines the seam between dev and prod
**When** `IMessenger` is updated
**Then** `Read()` returns a struct that includes `GroupID string` (in addition to text and sender)
**And** `Write(group_id string, text string) error` takes `group_id` explicitly
**And** any other I/O method on the interface that targets a specific group includes `group_id` similarly

**Given** the WhatsApp implementation
**When** `internal/messenger/whatsapp.go` is updated
**Then** the previous single `groupJID types.JID` field on the `WhatsApp` struct is removed (allowlist + auto-create take its place in Epic 2)
**And** for now, `Write(group_id, text)` resolves the JID and sends to that specific group
**And** existing tenant-zero outbound paths (digest, nag DM, scraper alert via `/notify`) continue to send to the correct group by passing the tenant-zero JID

**Given** the terminal implementation
**When** `internal/messenger/terminal.go` is updated
**Then** terminal mode emulates a single fixed dev `GroupID` (e.g., `"dev-group"`)
**And** `go run ./cmd/nagger` still launches into a usable interactive prompt with no observable change for the developer

**Given** all callers are updated
**When** `go build ./...` and `go test ./...` are run
**Then** both succeed
**And** Liza's family group continues to receive messages as before (digest, nag DM, scraper alerts)

**Given** D15 logging discipline
**When** the messenger emits any new log lines in this refactor
**Then** they use `slog` with `slog.String("group_id", ...)` attributes

## Epic 2: Multi-Tenant Bot Operation

**Goal:** A friend can add the bot to a new WhatsApp group with at least one allowlisted member, complete chat-driven onboarding in their chosen language, and operate the same task / digest / nag features Liza's group has — fully isolated from every other group's data and conversation. Liza's family group continues working identically.

### Story 2.1: Messenger-layer allowlist gating and group auto-create

As Liza-the-operator,
I want non-allowlisted groups to be completely silent and allowlisted groups to materialize as `groups` rows on first message,
So that the bot only operates where I've authorized it and friend-group state appears automatically when needed.

**Acceptance Criteria:**

**Given** `ALLOWED_PHONES` env is set with comma-separated international-format phones (no `+`, no spaces)
**When** the bot starts
**Then** `internal/messenger/whatsapp.go` parses the allowlist into a struct cached in memory
**And** every inbound message handler invocation re-reads the allowlist from the cached struct (not re-parsing env each time, but not caching membership-per-group either)

**Given** an inbound message from a WhatsApp group whose member list contains zero allowlisted phones
**When** the message arrives
**Then** the message is dropped immediately
**And** no DB write occurs
**And** no agent invocation occurs
**And** a single `slog.Info` line is emitted with `slog.String("group_id", "<dropped>")` and no message content
**And** no reply is sent to the group, no leave occurs, no presence change

**Given** an inbound message from a WhatsApp group with at least one allowlisted phone, where no `groups` row exists yet for that JID
**When** the message arrives
**Then** `groups.AutoCreate(ctx, jid, name, allowlistedPhones)` is invoked
**And** a `groups` row is inserted with `id = <jid>`, `name = <whatsmeow group name>`, `language = NULL`, `onboarding_state = "in_progress"`, `financial_enabled = 0`, `created_at = <now>`, `last_active_at = <now>`
**And** `members` rows are inserted only for the allowlisted phones present in the WhatsApp group (non-allowlisted phones are not inserted as members)
**And** if more than 2 allowlisted phones are in the group, only the first 2 are inserted (NFR3 cap; subsequent stories may surface this as a warning)

**Given** an inbound message from an existing `groups` row matching `WHATSAPP_GROUP_JID` (tenant zero)
**When** the message arrives
**Then** the existing tenant-zero behavior is preserved exactly — message is handed to the existing main agent path (which still uses tenant-zero JID via env-cached value from Story 1.5)

**Given** an inbound message from an existing `groups` row that is *not* tenant zero (a friend group created in a prior message of this same story)
**When** the message arrives
**Then** the message is silently consumed — no agent invocation, no reply
**And** a `slog.Info` line is emitted: `"awaiting dispatcher"` with `slog.String("group_id", <jid>)`
**And** this behavior is replaced in Story 2.2 by the dispatcher

**Given** unit tests for the allowlist + auto-create logic
**When** `go test ./internal/messenger/...` runs
**Then** tests cover: non-allowlisted drop, allowlisted-with-no-groups-row triggering auto-create, allowlisted-tenant-zero falling through to main agent, and allowlisted-non-tenant-zero silent-consume-with-log
**And** all tests use `Alice`/`Bob` placeholders, fake phones, fake JIDs (NFR9)

### Story 2.2: Two-agent dispatcher with per-key history and onboarding stub

As a dev agent enabling per-group routing,
I want a `dispatch.Handle` entry point that routes every inbound message to either the main agent or an onboarding-agent stub based on `groups.onboarding_state`, with conversation history keyed by `(group_id, agent_kind)`,
So that tenant-zero continues to work via the renamed main agent and friend groups have a routing destination ready for Story 2.3 to fill in.

**Acceptance Criteria:**

**Given** the existing `internal/agent/agent.go`
**When** the file is renamed to `internal/agent/main_agent.go` and the `Agent` type renamed to `MainAgent` (or kept as `Agent` within a more focused scope)
**Then** the file's content is the existing main-agent logic, no functional change
**And** `internal/agent/main_agent_test.go` is the renamed `agent_test.go`, all existing tests still pass

**Given** a new file `internal/agent/history.go`
**When** the package compiles
**Then** it exposes `type historyKey struct { GroupID string; AgentKind string }`
**And** a `History` struct with `Append(key historyKey, msg MessageParam)`, `Get(key historyKey) []MessageParam`, `Discard(key historyKey)`, internally `map[historyKey][]MessageParam`
**And** the existing `trimHistory` algorithm is moved here and applied per-key with the existing 20-message cap (NFR6)
**And** unit tests verify per-key trimming + that `Discard` removes only the targeted key

**Given** a new file `internal/agent/dispatch.go`
**When** the package compiles
**Then** it exposes `Handle(ctx context.Context, group_id string, sender_phone string, text string) error`
**And** `Handle` first loads the `groups` row via `GroupStore.Get(ctx, group_id)`
**And** if `groups.onboarding_state == "complete"`, `Handle` routes to `MainAgent.Handle(ctx, group_id, sender_phone, text)`
**And** otherwise, `Handle` routes to `OnboardingAgent.Handle(ctx, group_id, sender_phone, text)` (stub for this story; full impl in 2.3)
**And** after the agent's `Handle` returns successfully, `Handle` calls `groups.UpdateLastActive(ctx, group_id)` (D18 hook)

**Given** a new file `internal/agent/onboarding_agent.go`
**When** the package compiles in this story
**Then** `OnboardingAgent.Handle` is a stub: it logs `slog.Info("onboarding stub", slog.String("group_id", group_id))` and returns nil with no reply sent
**And** the stub is replaced with the full implementation in Story 2.3

**Given** `internal/messenger/whatsapp.go` is updated
**When** an allowlisted message arrives (allowlist + auto-create from Story 2.1)
**Then** the messenger calls `dispatch.Handle(ctx, group_id, sender_phone, text)` instead of the previous direct agent call
**And** the "awaiting dispatcher" placeholder log from Story 2.1 is removed (it's no longer reachable)

**Given** Liza's family group (tenant zero, `onboarding_state = "complete"`)
**When** any message arrives
**Then** dispatcher routes to `MainAgent.Handle`
**And** `groups.last_active_at` is updated after the agent returns
**And** her perceived behavior is unchanged

**Given** a friend group (auto-created, `onboarding_state = "in_progress"`)
**When** any message arrives
**Then** dispatcher routes to `OnboardingAgent.Handle` (stub)
**And** the stub silently consumes (no reply)
**And** `groups.last_active_at` is still updated

**Given** D15 logging discipline
**When** dispatcher and history code emit log lines
**Then** every line includes `slog.String("group_id", group_id)`

### Story 2.3: Onboarding agent and onboarding tool surface

As a friend who's been added to a new WhatsApp group with the bot,
I want the bot to walk us through bilingual onboarding (language → solo/couple → members → timezone → digest hour → confirm) and complete setup so the bot can serve our group like Liza's,
So that we can use it without anyone touching a config file or server.

**Acceptance Criteria:**

**Given** the onboarding agent replaces the Story 2.2 stub
**When** `OnboardingAgent.Handle(ctx, group_id, sender_phone, text)` is invoked
**Then** it loads the current `groups` row + members
**And** constructs a system prompt via `buildSystemPrompt(group, members)` that is bilingual until `groups.language` is set, then locked to the chosen language; warm/welcoming tone throughout (NFR4)
**And** invokes Anthropic with the onboarding tool surface only (no main-agent tools accessible)

**Given** the onboarding tool surface
**When** `BuildTools(ctx, group, "onboarding")` is invoked
**Then** it returns exactly `set_language`, `set_member`, `set_timezone`, `set_digest_hour`, `complete_onboarding` and no other tools
**And** none of these tools accept `group_id` in their input schema (D8)
**And** their handlers are closures that capture `group_id` from the dispatcher call

**Given** the `set_language` tool
**When** the LLM invokes it with `language ∈ {"he", "en"}`
**Then** if `groups.language IS NULL`, the value is written and the tool returns success
**And** if `groups.language` is already set, the tool refuses (returns an error result; LLM phrases the refusal in the locked language)

**Given** the `set_member` tool
**When** the LLM invokes it with `name` and `whatsapp_id` (international format, no `+`)
**Then** the tool upserts the member row, but only if it would not exceed the 1–2 cap (NFR3)
**And** if the cap would be exceeded, the tool refuses (LLM relays the refusal warmly, e.g., "v1 supports up to two members per group")

**Given** the `set_timezone` and `set_digest_hour` tools
**When** invoked with valid IANA timezone or hour `0..23`
**Then** the corresponding column on `groups` is written
**And** invalid values are rejected at the tool handler

**Given** the `complete_onboarding` tool
**When** invoked
**Then** the tool verifies all required fields are populated: `language`, `timezone`, `digest_hour`, `members count >= 1`
**And** if any is missing, the tool refuses and lists which field is missing
**And** if all are present, `groups.onboarding_state` is set to `"complete"`
**And** the onboarding history entry `(group_id, "onboarding")` is discarded via `History.Discard` (D6)
**And** the tool result indicates onboarding is done; the agent's final message is a brief "all set" in the locked language

**Given** an onboarding session is interrupted (process restart, or user steps away)
**When** the next inbound message from that group arrives
**Then** dispatcher still routes to `OnboardingAgent` (state is still `"in_progress"`)
**And** the system prompt computes "next missing field" from the populated `groups`/`members` rows
**And** the LLM picks up from the last unanswered question

**Given** a group whose `onboarding_state` was just flipped to `"complete"` by `complete_onboarding`
**When** the next inbound message arrives
**Then** dispatcher routes to `MainAgent` (with the locked language honored in the main-agent system prompt)

**Given** test coverage
**When** `go test ./internal/agent/...` runs
**Then** unit tests cover each onboarding tool's success + refusal paths using temp SQLite + Alice/Bob placeholders
**And** an integration-style test covers the full happy path: empty group → set_language("en") → set_member(Alice) → set_member(Bob) → set_timezone → set_digest_hour → complete_onboarding → assert state, members, history all correct

### Story 2.4: Main agent per-message tool registry with capability gating

As a member of a friend group with onboarding complete,
I want the main agent to expose only the tools my group has access to,
So that financial tools don't appear when my group has no scraper access, and the bot's persona, language, and tool surface match my group's locked configuration.

**Acceptance Criteria:**

**Given** the main agent's `Handle` is invoked by the dispatcher
**When** it builds its turn
**Then** `BuildTools(ctx, group, "main")` is invoked per inbound message (no caching, no global registry)
**And** the returned slice always includes `add_task`, `list_tasks`, `update_task`, `delete_task`, `dashboard_link` (and any other always-on main tools)
**And** the returned slice includes `expenses_summary` and `list_transactions` if and only if `group.financial_enabled = true`

**Given** a group with `financial_enabled = false`
**When** the LLM is invoked
**Then** `expenses_summary` and `list_transactions` are NOT in the tool schema sent to the LLM (not "present and refuses" — physically absent)
**And** the LLM cannot reference them

**Given** the main agent's system prompt
**When** `buildSystemPrompt(group, members)` runs
**Then** the prompt is constructed in `groups.language` (Hebrew or English)
**And** the persona is the existing sarcastic Israeli-engineer tone (NFR4)
**And** the prompt includes member display names, timezone, digest hour for context

**Given** tenant-zero (Liza's family group)
**When** Liza sends a message
**Then** the main agent receives the same tool surface as before (financial tools included) and the same persona
**And** her perceived behavior is unchanged

**Given** a friend group with onboarding complete and `financial_enabled = false`
**When** a member sends a message asking about expenses
**Then** the LLM responds without invoking financial tools (it cannot, since they're absent from the schema)
**And** the response phrases the limitation in the group's locked language

**Given** test coverage
**When** `go test ./internal/agent/...` runs
**Then** a unit test asserts the financial tools are absent in the slice returned by `BuildTools(ctx, groupWithFinancialFalse, "main")`
**And** a unit test asserts they are present for `groupWithFinancialTrue`

### Story 2.5: `update_task` content and assignee extension

As a member of any group,
I want to edit a task's content or reassign it via chat,
So that fixing typos or moving work between members doesn't require deleting and re-creating the task.

**Acceptance Criteria:**

**Given** the existing `update_task` tool only accepts `status` and `due_date`
**When** the tool's input schema is extended
**Then** it also accepts optional `content` (string) and `assignee` (string)
**And** all four fields are optional; the tool requires at least one to be present
**And** any field omitted or set to null is left unchanged on the row

**Given** an `update_task` invocation with `content` set
**When** the handler runs
**Then** `TaskStore.Update(group_id, id, fields)` is called with the new content
**And** the corresponding `tasks.content` value is updated in DB

**Given** an `update_task` invocation with `assignee` set
**When** the handler runs
**Then** the assignee value is validated against the group's `members.display_name` set
**And** if it doesn't match any current member, the tool returns a refusal (LLM phrases it in the locked language)
**And** if it matches, `tasks.assignee` is updated

**Given** the LLM tool schema
**When** the agent receives the schema for the main agent
**Then** the `update_task` tool documentation describes all four optional fields with examples

**Given** test coverage
**When** `go test ./internal/db/...` and `go test ./internal/agent/...` run
**Then** tests cover: content-only update, assignee-only update with valid member, assignee-only update with invalid member (refusal), combined fields update, no-op (no fields → tool refuses)

### Story 2.6: Group settings and member management tools

As a group member with onboarding complete,
I want tools to read and adjust my group's settings (timezone, digest hour, name) and manage members (add/update/remove with auto-reassign on remove),
So that we can update our setup without re-onboarding or asking Liza to run SQL.

**Acceptance Criteria:**

**Given** the main agent's tool surface
**When** `BuildTools(ctx, group, "main")` returns
**Then** it includes `get_group_settings`, `update_group_settings`, `add_member`, `update_member`, `remove_member`

**Given** the `get_group_settings` tool
**When** invoked
**Then** it returns the current group's name, timezone, digest hour, language, and member list (display names + WhatsApp IDs)
**And** it does NOT return `financial_enabled` (NFR1 — operator-only flag)
**And** it does NOT return `onboarding_state` or `last_active_at` (also operator-only observability)

**Given** the `update_group_settings` tool
**When** invoked with any of `name`, `timezone`, `digest_hour`
**Then** the corresponding column is updated
**And** `language` cannot be changed via this tool (input schema does not include it; locked at onboarding per NFR4)

**Given** the `add_member` tool
**When** invoked with `name` and `whatsapp_id`
**Then** if `members count < 2`, the new member is added (NFR3)
**And** if `members count >= 2`, the tool refuses (LLM relays refusal in locked language)

**Given** the `update_member` tool
**When** invoked with `whatsapp_id` (target) and new `display_name`
**Then** the member's display name is updated
**And** any `tasks.assignee` referencing the old display name is also updated to the new value (consistency)

**Given** the `remove_member` tool
**When** invoked with `whatsapp_id` (target)
**Then** if removing would leave the group with zero members, the tool refuses
**And** otherwise, the target's `members` row is deleted
**And** any open `tasks` (status = "pending") assigned to the removed member are auto-reassigned to the remaining member
**And** completed `tasks` (status = "done") keep their original assignee value (historical record preserved)

**Given** test coverage
**When** `go test ./internal/agent/...` and `go test ./internal/db/...` run
**Then** tests cover: settings get + update, member add at cap (refused), member add below cap (success), update_member with task reassignment, remove_member with auto-reassign (success), remove_member that would empty the group (refused)

### Story 2.7: Group-iterating digest and nag schedulers

As a member of any onboarded group,
I want my daily digest to fire at the hour I configured in my group's timezone, and to receive a nag DM if my overdue tasks pile up,
So that the bot's reminders are timed to my group's preferences, not Liza's.

**Acceptance Criteria:**

**Given** `cmd/nagger/main.go` is updated
**When** `startDigestScheduler` is invoked at process start
**Then** it spawns a single goroutine running `time.NewTicker(5 * time.Minute)`
**And** on each tick: `SELECT id, timezone, digest_hour, language FROM groups WHERE onboarding_state = 'complete'` is executed
**And** for each group, "is the current time in this group's TZ within the digest hour, and have we not yet sent today?" is evaluated using `metadata` rows keyed by `(group_id, "last_digest_date")`
**And** if yes, the digest is sent via `messenger.Write(group_id, text)` and `metadata` is updated atomically

**Given** the digest text
**When** rendered for a group
**Then** the format respects `groups.language` (Hebrew or English)
**And** content is the per-assignee task summary, computed via `TaskStore.List(group_id, ...)`

**Given** `startNagScheduler` follows the same shape
**When** it ticks and a group's pending overdue tasks for any assignee exceed `NAG_THRESHOLD`
**Then** a DM is sent to that assignee's WhatsApp ID
**And** `metadata` row keyed by `(group_id, "last_nag_date")` is updated to prevent duplicate DMs that day

**Given** the schedulers iterate groups
**When** a group is mid-onboarding (`onboarding_state != "complete"`)
**Then** that group is excluded from both digest and nag iterations

**Given** tenant-zero behavior
**When** Liza's group's digest hour and TZ are set per the migration's backfill from env
**Then** her digest fires at the same time it does today
**And** nag DMs to her members continue to fire at the same threshold and time

**Given** D15 logging
**When** the schedulers act on a group
**Then** every action log line includes `slog.String("group_id", group_id)`

**Given** test coverage
**When** `go test ./cmd/nagger/...` (or wherever scheduler logic is testable) runs
**Then** a unit test simulates the tick logic against a fake time + multiple groups in different TZs and asserts each group fires only when its hour is reached and not yet sent today

### Story 2.8: Bot-removed-from-group lifecycle is a no-op

As Liza-the-operator,
I want the bot's reaction to being kicked from a group to be "do nothing,"
So that friends who reorganize their WhatsApp groups (kick + re-add) don't lose state and don't need to re-onboard.

**Acceptance Criteria:**

**Given** `internal/messenger/whatsapp.go` handles whatsmeow events
**When** a `LeftGroup` (or whatsmeow's equivalent leave/kick event) fires for a `groups` row that exists in the DB
**Then** no DB write occurs (no `groups` row deletion, no member deletion, no task deletion, no history change)
**And** a single `slog.Info("bot removed from group", slog.String("group_id", jid))` line is emitted
**And** no farewell message, no leave sequence, no presence change

**Given** a group from which the bot was previously removed
**When** the bot is re-added and an inbound message arrives
**Then** the existing `groups` row is found (no auto-create needed since the row already exists)
**And** the dispatcher routes by the existing `onboarding_state` — `"complete"` groups go straight to main agent with their language locked; `"in_progress"` groups resume onboarding from the implicit state
**And** no re-onboarding is triggered for a previously-completed group

**Given** test coverage
**When** `go test ./internal/messenger/...` runs
**Then** a test exercises the leave-event handler against a temp DB with a pre-existing `groups` row + members + tasks, asserts zero DB writes, then simulates a subsequent inbound message and asserts the existing row is reused (no second `groups.AutoCreate` invocation)
