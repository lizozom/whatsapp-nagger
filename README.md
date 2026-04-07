# whatsapp-nagger

A WhatsApp bot that lives in your family group chat, manages a shared task backlog, tracks credit card expenses, and passive-aggressively nags people until things get done.

> "The sink has been broken for 4 days. I assume we are waiting for a miracle. Fix it."

> "You ordered Wolt 21 times this month and spent more on it than on electricity. The kitchen exists. Use it."

## What it does

### Task management
- **Listens** to your WhatsApp group in real time
- **Extracts tasks** from natural conversation ("I'll fix the fence tomorrow")
- **Tracks a backlog** in SQLite — who owes what, and for how long
- **Nags** assignees with dry, sarcastic reminders when tasks rot
- **Sends a daily digest** ("Wall of Shame") at a scheduled time — tasks grouped by assignee
- **Tags people** in WhatsApp messages with @mentions to force notifications

### Expense tracking
- **Ingests credit card transactions** from Israeli banks (Cal, Max) via a local scraper sidecar
- **Answers expense questions** in chat — "how much did we spend this month?", "top merchants in February", "compare me vs Bob"
- **Billing-cycle aware** — "this month" means your actual statement cycle (e.g. 10th to 9th), not the calendar month
- **Per-family-member breakdown** — cards are mapped to owners, so "how much did I spend?" works
- **Net of refunds** — a purchase that was fully returned shows as zero, not double-counted

Powered by Claude (Anthropic) with tool calling for structured task and expense management — no regex parsing, no brittle keyword matching.

## Architecture

```
WhatsApp Group
     |
     v
  whatsmeow (Multi-Device API)
     |
     v
  IMessenger interface
     |
     v
  Claude Agent (tool calling)
     |
     +--> SQLite (tasks + metadata + transactions)
     |
     +--> expenses_summary / list_transactions (tool calls)

  Local Mac (nightly cron)
     |
     v
  Node scraper (israeli-bank-scrapers)
     |
     v
  POST /ingest/transactions (HMAC-SHA256)
     |
     v
  SQLite transactions table (on Fly)
```

The `IMessenger` interface lets you swap between a **terminal mode** (for local development) and **WhatsApp mode** (for production) with a single env var.

The expense scraper runs as a **separate process** on a local machine (Mac with launchd), pushes transactions to the bot via an authenticated HTTP endpoint. This keeps bank credentials off the cloud server.

## Tech Stack

| Component | Choice |
|-----------|--------|
| Language | Go |
| WhatsApp | [whatsmeow](https://github.com/tulir/whatsmeow) (Multi-Device API) |
| AI | Anthropic Claude via tool calling |
| Database | SQLite (tasks, metadata, transactions, ingest runs) |
| Scraper | TypeScript + [israeli-bank-scrapers](https://github.com/eshaham/israeli-bank-scrapers) |
| Deploy | Fly.io (persistent volume) |
| Scheduling | macOS launchd (scraper), in-process ticker (daily digest) |

## Quick Start

### 1. Clone and configure

```bash
git clone https://github.com/lizozom/whatsapp-nagger.git
cd whatsapp-nagger
cp .env.example .env
cp personas.md.example personas.md
```

Edit `.env` and add your `ANTHROPIC_API_KEY`. Edit `personas.md` with your family members (see [Personas](#personas) below).

### 2. Run in terminal mode

```bash
go run ./cmd/nagger
```

Type messages as `[Name]: message text` to simulate group chat:

```
> [Alice]: I'll take out the trash later
> [Bob]: what's on the list?
> [Alice]: how much did we spend this month?
```

### 3. Connect to WhatsApp

Set these in your `.env`:

```bash
MESSENGER=whatsapp
WHATSAPP_PHONE=972501234567  # Your number, international format, no +
```

Run the bot. It generates an 8-digit pairing code. Open WhatsApp > Linked Devices > Link a Device > **Link with phone number** and enter the code.

Your phone stays connected normally — the bot runs as a linked device (like WhatsApp Web).

### 4. Find your Group JID

Leave `WHATSAPP_GROUP_JID` empty on first run. The bot enters **discovery mode** and logs all group messages with their JIDs:

```
[DISCOVERY] Group JID: 120363XXXXXXXXXX@g.us | Sender: Alice | Text: hello
```

Copy the JID into your `.env` as `WHATSAPP_GROUP_JID` and restart.

## Personas

The bot uses a `personas.md` file to understand your family members — their names, roles, and how aggressively to nag them. This file is **gitignored** so your family details stay private.

Copy the example and customize it:

```bash
cp personas.md.example personas.md
```

Add a `**Phone:**` field (international format, no `+`) to enable @mentions in WhatsApp:

```markdown
## Alice
- **Phone:** 972501234567
- **Role:** Parent / Engineer
- **Nag style:** Light touch
```

The bot loads personas from (in order of priority):
1. **`personas.md`** file (or path in `PERSONAS_FILE` env var)
2. **`FAMILY_MEMBERS`** env var (a single string description)
3. **Neither** — the bot still works, it just infers who's who from conversation context

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `ANTHROPIC_API_KEY` | Your Anthropic API key | required |
| `MESSENGER` | `terminal` or `whatsapp` | `terminal` |
| `WHATSAPP_GROUP_JID` | Target group JID (e.g. `120363...@g.us`) | empty = discovery mode |
| `WHATSAPP_PHONE` | Phone number for pairing (international format) | required for WhatsApp |
| `WHATSAPP_DB_PATH` | Path to WhatsApp session DB | `whatsapp_session.db` |
| `TASKS_DB_PATH` | Path to tasks SQLite DB | `tasks.db` |
| `PERSONAS_FILE` | Path to personas markdown file | `personas.md` |
| `FAMILY_MEMBERS` | Fallback: family members as a string | none |
| `TIMEZONE` | Timezone for dates and daily digest | `Asia/Jerusalem` |
| `DIGEST_HOUR` | Daily digest time, 24h format (e.g. `08:30`) | disabled if unset |
| `QR_TOKEN` | Secret token to protect the `/pair` web endpoint | disabled if unset |
| `INGEST_SECRET` | Shared HMAC secret for `/ingest/transactions`. Enables the ingest server when set. | disabled if unset |
| `INGEST_PORT` | Port for the ingest HTTP server | `8080` |
| `BILLING_DAY` | Day of month when credit card statements cut (1-28) | `10` |
| `CARD_OWNERS` | Maps family members to cards. Format: `Alice:max/1234,cal/5678;Bob:max/9999` | none |

## Expense Tracking

The bot can track and answer questions about credit card expenses. This feature has three parts:

### 1. Transaction ingest endpoint (Go)

When `INGEST_SECRET` is set, the bot exposes an HTTP endpoint for receiving transactions:

- `POST /ingest/transactions` — authenticated via HMAC-SHA256 in the `X-Signature` header
- `GET /healthz` — liveness probe

Transactions are upserted by a stable hash of `(provider, card_last4, posted_at, amount_ils, description, memo)`, so the same payload can be safely re-sent without creating duplicates.

### 2. Scraper sidecar (Node/TypeScript)

The `scraper/` directory contains a local runner that fetches transactions from Israeli credit card providers and posts them to the ingest endpoint. Currently supports:

- **Max** (מקס)
- **Cal** (כאל / Visa Cal)

The scraper reads bank credentials from the **macOS Keychain** (with env var fallback), so sensitive credentials never leave your local machine or touch the cloud server.

```bash
cd scraper
npm install
cp .env.example .env
# Edit .env with your INGEST_URL and INGEST_SECRET

# Store credentials in macOS Keychain
security add-generic-password -s "nagger-max" -a "username" -w
security add-generic-password -s "nagger-max" -a "password" -w

# Run
npm start                    # default provider (max)
npm start -- --provider=cal  # specific provider
npm start -- --dry-run       # print transactions without posting
```

See [`scraper/README.md`](scraper/README.md) for full setup instructions.

### 3. Nightly schedule (launchd)

The scraper is designed to run on a schedule via macOS launchd. A wrapper script (`scraper/run.sh`) handles both providers with automatic retry on failure. Install it with:

```bash
# Copy the plist to LaunchAgents
cp scraper/launchd/com.example.nagger-scraper.plist ~/Library/LaunchAgents/

# Edit the plist to point to your scraper directory, then load it
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.nagger-scraper.plist
```

### 4. Agent tools

Once transactions are in SQLite, the bot gains two Claude tools:

- **`expenses_summary`** — aggregate expenses by category, merchant, month, provider, card, or family member. Supports date ranges, owner filtering, and merchant search.
- **`list_transactions`** — drill into individual charges with the same filter options.

Example questions the bot can answer:
- "How much did we spend this month?"
- "Top 5 merchants in February"
- "How much did I spend vs Bob?"
- "What did we buy at Shufersal in March?"
- "Biggest single charge this year"

The default date range is the **current billing cycle** (driven by `BILLING_DAY`), not the calendar month. Amounts are reported **net of refunds** — a ₪1,000 purchase that was returned shows as ₪0 spent.

## Daily Digest

Set `DIGEST_HOUR` (e.g. `08:30`) to receive a daily "Wall of Shame" summary in your group. The digest lists every pending task grouped by assignee, with @mentions to force notifications.

You can also request the digest anytime in chat — just ask "show me the digest" or "what's the daily summary".

The scheduled digest fires once per day (tracked in SQLite to prevent double-sends across restarts).

## Versioning

The version lives in `internal/version/version.go`. Bump it manually before deploying.

## Deploy to Fly.io

```bash
# Install Fly CLI
curl -L https://fly.io/install.sh | sh

# Create app and persistent volume
fly apps create whatsapp-nagger
fly volumes create nagger_data --region fra --size 1

# Set secrets
fly secrets set \
  ANTHROPIC_API_KEY=sk-... \
  WHATSAPP_GROUP_JID=120363...@g.us \
  WHATSAPP_PHONE=972501234567 \
  DIGEST_HOUR=08:30 \
  INGEST_SECRET=$(openssl rand -hex 32) \
  BILLING_DAY=10 \
  CARD_OWNERS='Alice:max/1234,cal/5678;Bob:max/9999'

# Deploy (injects version + deploy date via ldflags)
bash deploy.sh
```

On first deploy, visit `https://your-app.fly.dev/pair` to get the pairing code and link WhatsApp.

Upload your personas file to the persistent volume:

```bash
echo "put personas.md /data/personas.md" | fly sftp shell
```

## Project Structure

```
cmd/nagger/              Entry point
internal/
  agent/                 Claude agent with tool calling (tasks + expenses)
  db/
    db.go                SQLite task store + metadata
    transactions.go      Transaction store, filters, aggregations
  ingest/
    handler.go           HMAC-authenticated HTTP ingest endpoint
    hmac.go              Signature computation + verification
  messenger/
    messenger.go         IMessenger interface + Mention type
    terminal.go          Terminal/stdin implementation
    whatsapp.go          WhatsApp (whatsmeow) implementation
  version/               Version + deploy date (set via ldflags)
scraper/                 Node/TypeScript credit card scraper sidecar
  src/
    index.ts             Orchestrator (CLI flags, provider dispatch)
    config.ts            Env + macOS Keychain credential loader
    post.ts              HMAC-signed HTTP POST client
    state.ts             Per-provider run state (~/.nagger-scraper/)
    providers/
      cal.ts             Visa Cal (כאל) scraper
      max.ts             Max (מקס) scraper
  run.sh                 Nightly wrapper script with retry logic
deploy.sh                Deploy script (reads version, injects ldflags)
Dockerfile               Multi-stage Go build
fly.toml                 Fly.io configuration
personas.md.example      Example personas file
.env.example             Example environment config
```

## Security

- **Bank credentials** are stored in the macOS Keychain on your local machine and never reach the cloud server. The scraper runs locally and only sends normalized transaction data over HTTPS.
- **Transaction ingest** is authenticated via HMAC-SHA256. Requests without a valid signature are rejected with 401.
- **WhatsApp** connects via whatsmeow's E2E encrypted protocol (outbound connection from the bot, no inbound endpoint).
- **Anthropic API** calls are outbound HTTPS with an API key stored in Fly secrets.
- **Conversation history** is held in-memory only (capped at 20 messages) and is not persisted to disk.
- **Personal data** (names, phones, card numbers) should only live in gitignored files (`.env`, `personas.md`). The codebase uses placeholder values in examples and tests.

## Known Limitations

- Cal (כאל) refunds are sometimes misreported by `israeli-bank-scrapers` as negative charges instead of positive credits, causing overcounting for Cal merchants that had refunds. Max refunds work correctly. Tracked in [#1](https://github.com/lizozom/whatsapp-nagger/issues/1).
- Provider categories from Max/Cal are unreliable — for example, Wolt grocery deliveries appear under "restaurants" rather than "groceries".
- Hebrew merchant names require querying in Hebrew — the bot doesn't yet resolve English aliases (e.g. "TerminalX" vs "טרמינל איקס").

## License

MIT
