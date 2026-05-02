# TODO

Things to address after Epic 2 ships. Add new items as they come up; remove
items as they ship. Keep this file short — long-form planning lives in
`_bmad-output/`.

## Soon

- **Per-group sequence task IDs** — SQLite's autoincrement gives globally-unique
  task IDs across groups. Confusing UX (group sees "task #48" when they only
  have 3 tasks); also leaks information about other groups' activity. Add a
  `group_seq` column, backfill via row_number partitioned by group_id, expose
  group_seq as the user-facing "id".
  *(In progress, this session.)*

- **Member gender awareness** — Hebrew is heavily gendered. Bot replies use
  awkward slash-form ("פתח/י", "תסדר/י") because we don't track each member's
  gender. Add `members.gender` column (`m`/`f`/`unknown`/null), capture during
  onboarding ("how should I refer to you — masculine or feminine?"), pass to
  the LLM via system prompt so it picks proper conjugations. Same for the
  hardcoded nag DM Hebrew copy.

- **Clean Hebrew copy in hardcoded strings** — the onboarding closing message
  (`internal/agent/onboarding_agent.go` `completionMessage`) and nag DM
  (`internal/scheduler/scheduler.go` `runNagOnce`) are awkward. Have a Hebrew
  speaker (Liza) review and rewrite.

- **Delete stray `מאמאמצפה` row from prod** — inert leftover from before the
  phone switch (group_id `972548377976-1460381894@g.us`, no tasks). Roll into
  the next deploy as a one-line cleanup migration so we don't need
  start/stop/sftp choreography.

## When you're confident things are stable

- **Re-enable `auto_start_machines`** — disabled during the spam incident
  recovery; the machine no longer auto-wakes on traffic. Once Liza's family
  group is back online and a few days pass cleanly, flip it back in `fly.toml`
  (or via `fly machine update --autostart=true`).

## Backlog (no rush)

- **Story 1.1 — Litestream + `sqlite3` in Fly image.** Deferred from Epic 1.
  Would unlock `fly ssh` ad-hoc DB queries and continuous S3/R2 replication for
  point-in-time recovery. Currently we use `fly sftp` + local sqlite3, which
  works but is fiddly.

- **Per-group nag hour.** Today `NAG_HOUR` is a single global env (operator
  policy). Move to `groups.nag_hour` so each tenant picks their own.

- **Per-group `financial_enabled` toggle** — currently DB-only flip. Could
  expose a chat tool, but only for the operator (Liza). Needs a notion of
  "operator" baked into the schema.
