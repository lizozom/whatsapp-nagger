# whatsapp-nagger

A proactive WhatsApp-based agent that lives in a family group chat. Manages a shared task backlog, tracks credit card expenses, sends reminders (nags), and provides daily summaries. Tone: no-nonsense Israeli software engineer — pragmatic, direct, slightly sarcastic.

Environment: Private WhatsApp family group.

## Tech Stack

- **Language:** Go (bot core), TypeScript (scraper + dashboard)
- **WhatsApp:** whatsmeow (Multi-Device API)
- **AI:** Anthropic Claude Haiku 4.5 (tool calling)
- **Database:** SQLite (tasks, metadata, transactions, ingest runs)
- **Scraper:** TypeScript + israeli-bank-scrapers (Cal, Max)
- **Dashboard:** Next.js (App Router) at `dashboard/` — read-only web view of tasks + finances. Auth via WhatsApp DM OTP → JWT.
- **Deploy:** Fly.io (persistent volume for SQLite)
- **Scheduling:** macOS launchd (scraper), in-process tickers (daily digest, nag DMs)

## Architecture

Observe → Reason → Act loop.

- `IMessenger` interface wraps terminal (dev) or WhatsApp (prod)
- Claude agent uses tool calling for task CRUD, expense queries, and dashboard magic links
- SQLite is the single source of truth for tasks and transactions
- Transaction scraper runs as a separate Node sidecar on a local Mac, pushes via HMAC-authenticated HTTP to the bot on Fly
- Single HTTP mux owned by `cmd/nagger/main.go` serves: `/healthz`, `/health`, `/pair` (WhatsApp pairing code), `/ingest/transactions` (HMAC), `/notify` (HMAC scraper alerts → group), and dashboard auth routes
- Dashboard auth: phone-allowlist (from `personas.md`) → DM-delivered OTP → JWT cookie. The `dashboard_link` tool produces a one-tap pre-authenticated URL for a user.

## Data Schema (SQLite)

```sql
tasks: id, content, assignee, status (pending/done), due_date, created_at, updated_at
metadata: key, value  -- e.g. last_digest_date

transactions: id (stable hash), provider, card_last4, posted_at, amount_ils,
              description, memo, category, status (pending/posted), raw_json, ingested_at
ingest_runs: id, provider, started_at, finished_at, status, error, tx_count
```

## Agent Tools

Task tools: `add_task`, `list_tasks`, `update_task` (status + due_date only — content/assignee editing is on the multi-tenancy roadmap), `delete_task`.

Expense tools:
- `expenses_summary` — aggregate by category/merchant/month/provider/card_last4/owner. Default range is current billing cycle (BILLING_DAY env var). spent_ils is net of refunds.
- `list_transactions` — drill into individual charges with filters (date, provider, category, merchant_contains, owner, debits_only).

Dashboard tool:
- `dashboard_link` — generates a one-tap magic link to the web dashboard, pre-authenticated for the requesting user. Resolves the sender's name (from `[Sender]` prefix) to a phone via `personas.md`.

Owner resolution: `CARD_OWNERS` env var maps names to provider/last4 pairs. The LLM resolves "how much did I spend?" via the [Sender] prefix.

## Schedulers

- **Daily digest** (`startDigestScheduler`) — fires at `DIGEST_HOUR` IDT, sends a per-assignee summary to the group. Idempotent via `metadata.last_digest_date`.
- **Nag DMs** (`startNagScheduler`) — fires at `NAG_HOUR` IDT, DMs anyone whose pending overdue tasks exceed `NAG_THRESHOLD` (default 4). Idempotent via `metadata.last_nag_date`.

Both run as goroutines off `main.go`, both currently single-tenant (process-global state).

## Commands

```bash
# Run in terminal mode
go run ./cmd/nagger

# Run in WhatsApp mode
MESSENGER=whatsapp go run ./cmd/nagger

# Test
go test ./...

# Run scraper (from scraper/ directory)
npm start                    # default provider (max)
npm start -- --provider=cal
npm start -- --dry-run

# Deploy
bash deploy.sh
```

## Checking when transaction sync last ran

The scraper runs nightly at 22:00 IDT via launchd (`com.lizozom.nagger-scraper`) on the local Mac and pushes to Fly via HTTP. The Fly image has no `sqlite3`, so don't try to query the prod DB over `fly ssh`. Instead, read the scraper's own logs:

```bash
# Latest run log (one file per day)
ls -lt ~/Library/Logs/nagger-scraper/ | head
tail -40 ~/Library/Logs/nagger-scraper/$(date +%Y-%m-%d).log

# launchd plist (schedule + log paths)
~/Library/LaunchAgents/com.lizozom.nagger-scraper.plist
```

Each daily log shows per-provider `scraped` / `inserted` / `skipped` counts and the `run_id` assigned by the bot — that run_id corresponds to `ingest_runs.id` in the prod SQLite DB.

## Conventions

- Keep code flat — minimal abstraction until patterns emerge
- `IMessenger` interface is the seam between dev and prod modes
- All Claude interactions use tool calling (not free-form text parsing)
- SQLite is the single source of truth for task and transaction state
- Structured config (card ownership, billing day) goes in env vars, not markdown files
- Personal identifiers (names, phones, card numbers) must only appear in gitignored files (.env, personas.md) — use placeholders (Alice/Bob) in committed code and tests
- Conversation history is capped at 20 messages to bound token usage
- Never auto-bump versions; user controls version manually

## Multi-Tenancy Work In Progress

A rebuild to support ~10 friend groups (1–2 members each) from this same instance is in flight. Planning lives at:

- `_bmad-output/planning-artifacts/prd.md` — product requirements
- `_bmad-output/project-context.md` — brownfield analysis: single-tenant assumptions, multi-tenancy seams, isolation rules

**Hard rules (read these before editing anything in `internal/`):**
- `group_id` is never a parameter on the LLM-facing tool schema — it's always derived server-side from the inbound message envelope.
- No tool lists groups, members of other groups, or any cross-group aggregation. The operator (Liza) does not get an admin back-door through the agent.
- `expenses_summary` and `list_transactions` are conditionally registered based on `groups.financial_enabled` for the calling group. They must not appear in the tool schema for groups without financial access.
- The Next.js dashboard, OTP/JWT auth, and `/ingest/transactions` endpoint stay tenant-zero only in v1.
