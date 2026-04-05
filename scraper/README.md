# whatsapp-nagger scraper

Local runner that scrapes Israeli credit card / bank transactions via
[`israeli-bank-scrapers`](https://github.com/eshaham/israeli-bank-scrapers)
and posts them to the `whatsapp-nagger` ingest endpoint.

Designed to run on a Mac (Keychain-backed credentials), triggered by `launchd`
on a daily schedule.

## Currently supported

- **Cal** (כאל) — `visaCal`

Max and others are planned follow-ups.

## Setup

```bash
# 1. Install dependencies
cd scraper
npm install

# 2. Configure ingest URL + shared secret
cp .env.example .env
# edit .env — set INGEST_URL and INGEST_SECRET (must match the bot's env)

# 3. Store Cal credentials in the macOS Keychain (one-time)
security add-generic-password -s "nagger-cal" -a "username" -w
security add-generic-password -s "nagger-cal" -a "password" -w
```

After the Keychain entries are created, the scraper reads them at runtime via
the `security` CLI — they never touch disk.

## Running

```bash
# Default (--provider=cal)
npm start

# Dry run: print normalized transactions to stdout, don't POST
npx tsx src/index.ts --provider=cal --dry-run
```

State is persisted at `~/.nagger-scraper/<provider>.json` and tracks the last
successful fetch window. Subsequent runs fetch from `lastWindowStart - 3 days`
so pending→posted transitions and late arrivals still flow through.

## How it posts to the bot

Each run POSTs to `${INGEST_URL}` with:

- **Body:** `{ provider, fetched_at, transactions: [...] }`
- **Header:** `X-Signature: <hex HMAC-SHA256 of the raw body under INGEST_SECRET>`

The bot computes the same HMAC and rejects mismatches with `401`. Transaction
IDs are derived server-side from a stable hash of
`(provider, card_last4, posted_at, amount_ils, description, memo)` so reruns
are idempotent.

## Credential precedence

The scraper looks up credentials in this order:

1. `<PROVIDER>_USERNAME` / `<PROVIDER>_PASSWORD` env vars (e.g. `CAL_USERNAME`)
2. macOS Keychain: service `nagger-<provider>`, accounts `username` / `password`

Env vars take precedence so you can do one-off runs from a non-Mac dev box
without touching the Keychain.

## Scheduling with launchd (planned)

`launchd/com.lizozom.nagger-scraper.plist` will wire this into a daily
background run. Not yet implemented — see the main project roadmap.

## Troubleshooting

- **`ECONNREFUSED`** — the bot isn't running, or `INGEST_URL` is wrong, or the
  bot was started without `INGEST_SECRET` (the ingest server only starts when
  the secret is set).
- **`401 Unauthorized: invalid signature`** — scraper and bot have different
  `INGEST_SECRET` values.
- **Cal login failing / CAPTCHA loop** — Cal sometimes requires an interactive
  first login from a new device. Run the scraper once with
  `showBrowser: true` (edit `src/providers/cal.ts`) to complete any required
  verification, then revert.
- **`Missing credentials for provider "cal"`** — you haven't added the
  Keychain entries or set `CAL_USERNAME` / `CAL_PASSWORD`.
