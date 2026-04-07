#!/bin/bash
# Nightly credit card scraper — run by launchd at 22:00.
# Scrapes Max (and Cal when re-enabled), posts to the Fly ingest endpoint.
set -euo pipefail

SCRAPER_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRAPER_DIR"

# Homebrew node/npx path (launchd runs with a minimal PATH)
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$PATH"

# Source .env for INGEST_URL, INGEST_SECRET, etc.
set -a
source .env
set +a

LOG_DIR="$HOME/Library/Logs/nagger-scraper"
mkdir -p "$LOG_DIR"
LOG="$LOG_DIR/$(date +%Y-%m-%d).log"

echo "=== $(date) ===" >> "$LOG"

run_provider() {
  local provider="$1"
  echo "[$provider] starting" >> "$LOG"
  if npx tsx src/index.ts --provider="$provider" >> "$LOG" 2>&1; then
    return 0
  fi
  echo "[$provider] first attempt failed, retrying in 30s..." >> "$LOG"
  sleep 30
  if npx tsx src/index.ts --provider="$provider" >> "$LOG" 2>&1; then
    echo "[$provider] retry succeeded" >> "$LOG"
    return 0
  fi
  echo "[$provider] FAILED after retry" >> "$LOG"
  return 1
}

run_provider max || true
run_provider cal || true

echo "=== done $(date) ===" >> "$LOG"
