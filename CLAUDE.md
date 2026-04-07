# whatsapp-nagger

A proactive WhatsApp-based agent that lives in a family group chat. Manages a shared task backlog, tracks credit card expenses, sends reminders (nags), and provides daily summaries. Tone: no-nonsense Israeli software engineer — pragmatic, direct, slightly sarcastic.

Environment: Private WhatsApp family group.

## Tech Stack

- **Language:** Go
- **WhatsApp:** whatsmeow (Multi-Device API)
- **AI:** Anthropic Claude Haiku 4.5 (tool calling)
- **Database:** SQLite (tasks, metadata, transactions, ingest runs)
- **Scraper:** TypeScript + israeli-bank-scrapers (Cal, Max)
- **Deploy:** Fly.io (persistent volume for SQLite)
- **Scheduling:** macOS launchd (scraper), in-process ticker (daily digest)

## Architecture

Observe → Reason → Act loop.

- `IMessenger` interface wraps terminal (dev) or WhatsApp (prod)
- Claude agent uses tool calling for task CRUD + expense queries
- SQLite is the single source of truth for tasks and transactions
- Transaction scraper runs as a separate Node sidecar on a local Mac, pushes via HMAC-authenticated HTTP to the bot on Fly

## Data Schema (SQLite)

```sql
tasks: id, content, assignee, status (pending/done), due_date, created_at, updated_at
metadata: key, value  -- e.g. last_digest_date

transactions: id (stable hash), provider, card_last4, posted_at, amount_ils,
              description, memo, category, status (pending/posted), raw_json, ingested_at
ingest_runs: id, provider, started_at, finished_at, status, error, tx_count
```

## Agent Tools

Task tools: `add_task`, `list_tasks`, `update_task`, `delete_task`

Expense tools:
- `expenses_summary` — aggregate by category/merchant/month/provider/card_last4/owner. Default range is current billing cycle (BILLING_DAY env var). spent_ils is net of refunds.
- `list_transactions` — drill into individual charges with filters (date, provider, category, merchant_contains, owner, debits_only).

Owner resolution: `CARD_OWNERS` env var maps names to provider/last4 pairs. The LLM resolves "how much did I spend?" via the [Sender] prefix.

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

## Conventions

- Keep code flat — minimal abstraction until patterns emerge
- `IMessenger` interface is the seam between dev and prod modes
- All Claude interactions use tool calling (not free-form text parsing)
- SQLite is the single source of truth for task and transaction state
- Structured config (card ownership, billing day) goes in env vars, not markdown files
- Personal identifiers (names, phones, card numbers) must only appear in gitignored files (.env, personas.md) — use placeholders (Alice/Bob) in committed code and tests
- Conversation history is capped at 20 messages to bound token usage
- Never auto-bump versions; user controls version manually
