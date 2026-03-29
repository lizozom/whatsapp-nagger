# whatsapp-nagger

A proactive WhatsApp-based agent that lives in a family group chat. Manages a shared task backlog, sends reminders (nags), and provides daily summaries. Tone: no-nonsense Israeli software engineer — pragmatic, direct, slightly sarcastic.

Environment: Private WhatsApp family group.

## Tech Stack

- **Language:** Go
- **WhatsApp:** whatsmeow (Multi-Device API)
- **AI:** Anthropic Claude 4.5 Sonnet (tool calling)
- **Database:** SQLite (tasks, session, event log)
- **Deploy:** Fly.io (persistent volume for SQLite)

## Architecture

Observe → Reason → Act loop.

### Phase 1: Dev Mode (current)

- `IMessenger` interface wrapping stdin/stdout
- Terminal input simulates group messages: `[User]: I'll do the dishes later`
- Local `tasks.db`

### Phase 2: Production

- Linked device via whatsmeow (no second SIM needed)
- Triggers: inbound messages (real-time) + scheduled daily digest ("Wall of Shame")

## Data Schema (SQLite)

```sql
tasks: id, content, assignee, status (pending/done), created_at, updated_at
metadata: key, value  -- e.g. last processed message timestamp
```

## Agent System Prompt

> You are whatsapp-nagger. Your job is to ensure the family backlog is cleared.
> If a task is mentioned, log it. If a task is finished, mark it.
> If a task is rotting, nag the assignee with a dry, sarcastic remark.
>
> Tone: "The sink has been broken for 4 days. I assume we are waiting for a miracle. Fix it."
>
> Keep it short. No "As an AI" or "I am happy to help". Just do the work.

## Development Milestones

- [ ] Mock Loop — Go CLI piping stdin to Claude 4.5 Sonnet, SQLite ops via tool calling
- [ ] Backlog Logic — "Daily Summary" generator
- [ ] SIM Integration — Switch `IMessenger` from terminal to whatsmeow
- [ ] Deployment — Dockerize, push to Railway with persistent volume

## Commands

```bash
# Run (once built)
go run ./cmd/nagger

# Test
go test ./...
```

## Conventions

- Keep code flat — minimal abstraction until patterns emerge
- `IMessenger` interface is the seam between dev and prod modes
- All Claude interactions use tool calling (not free-form text parsing)
- SQLite is the single source of truth for task state
