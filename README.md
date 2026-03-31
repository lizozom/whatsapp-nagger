# whatsapp-nagger

A WhatsApp bot that lives in your family group chat, manages a shared task backlog, and passive-aggressively nags people until things get done.

> "The sink has been broken for 4 days. I assume we are waiting for a miracle. Fix it."

## What it does

- **Listens** to your WhatsApp group in real time
- **Extracts tasks** from natural conversation ("I'll fix the fence tomorrow")
- **Tracks a backlog** in SQLite — who owes what, and for how long
- **Nags** assignees with dry, sarcastic reminders when tasks rot
- **Sends a daily digest** ("Wall of Shame") at a scheduled time — tasks grouped by assignee
- **Tags people** in WhatsApp messages with @mentions to force notifications
- **On-demand digest** — ask for the digest in chat anytime ("show me the digest")

Powered by Claude (Anthropic) with tool calling for structured task management — no regex parsing, no brittle keyword matching.

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
     v
  SQLite (tasks + metadata)
```

The `IMessenger` interface lets you swap between a **terminal mode** (for local development) and **WhatsApp mode** (for production) with a single env var.

## Tech Stack

| Component | Choice |
|-----------|--------|
| Language | Go |
| WhatsApp | [whatsmeow](https://github.com/tulir/whatsmeow) (Multi-Device API) |
| AI | Anthropic Claude via tool calling |
| Database | SQLite (tasks, session, metadata) |
| Deploy | Fly.io (persistent volume) |

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
  DIGEST_HOUR=08:30

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
  agent/                 Claude agent with tool calling
  db/                    SQLite task store + metadata
  messenger/
    messenger.go         IMessenger interface + Mention type
    terminal.go          Terminal/stdin implementation
    whatsapp.go          WhatsApp (whatsmeow) implementation
  version/               Version + deploy date (set via ldflags)
deploy.sh                Deploy script (reads version, injects ldflags)
Dockerfile               Multi-stage build
fly.toml                 Fly.io configuration
personas.md.example      Example personas file
.env.example             Example environment config
```

## License

MIT
