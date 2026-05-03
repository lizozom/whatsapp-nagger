// Package scheduler runs the per-group digest and nag tickers (architecture D14).
//
// One goroutine per scheduler kind, scanning every TickInterval. On each tick:
//   1. List all groups with onboarding_state = "complete".
//   2. For each group, evaluate "should fire now?" against its timezone,
//      digest_hour, and the per-group last_*_date metadata key.
//   3. If yes: generate the message, deliver it, then update metadata atomically.
//
// The decision logic lives in pure functions (ShouldFireDigest, ShouldFireNag)
// so it can be tested against an injected clock with multiple groups in
// different timezones. The goroutine wrappers (RunDigest, RunNag) are thin
// glue around those functions plus I/O.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lizozom/whatsapp-nagger/internal/agent"
	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
)

// TickInterval is how often each scheduler scans the groups table. Five
// minutes (vs the legacy 30s) is enough granularity since the firing window
// is hourly and we deduplicate via the daily metadata key.
const TickInterval = 5 * time.Minute

// DigestDeps bundles the dependencies the digest scheduler needs.
type DigestDeps struct {
	Groups    *db.GroupStore
	Tasks     *db.TaskStore
	Agent     *agent.Agent
	Messenger messenger.IMessenger
	Now       func() time.Time // injectable clock; defaults to time.Now in RunDigest
}

// NagDeps bundles the dependencies the nag scheduler needs. DM is split out
// from Messenger because direct messages aren't on IMessenger (only
// *messenger.WhatsApp implements them).
type NagDeps struct {
	Groups    *db.GroupStore
	Members   *db.MemberStore
	Tasks     *db.TaskStore
	DM        DMSender
	Threshold int
	Now       func() time.Time
}

// DMSender sends a direct (1:1) message to a phone. *messenger.WhatsApp
// satisfies this; terminal mode does not — nag is skipped in terminal mode.
type DMSender interface {
	SendDM(phone, text string) error
}

// ShouldFireDigest reports whether the group's digest should fire at `now`,
// given the date string of the last successful fire (empty = never). Pure
// function — testable without I/O.
func ShouldFireDigest(g db.Group, lastDate string, now time.Time) (bool, string) {
	if g.Timezone == "" || !g.DigestHourSet {
		return false, ""
	}
	loc, err := time.LoadLocation(g.Timezone)
	if err != nil {
		return false, ""
	}
	nowLocal := now.In(loc)
	if nowLocal.Hour() != g.DigestHour {
		return false, ""
	}
	today := nowLocal.Format("2006-01-02")
	return lastDate != today, today
}

// ShouldFireNag reports whether the nag scheduler should evaluate this
// group's overdue counts now, given the date string of the last nag fire.
// Hour comparison uses NagHour in the group's timezone. Pure function.
//
// The nag scheduler still uses a single global NAG_HOUR env (not per-group)
// because nag time is operator policy, not user preference. Per-group nag
// timing could be added later via groups.nag_hour.
func ShouldFireNag(g db.Group, lastDate string, nagHour int, now time.Time) (bool, string) {
	if g.Timezone == "" {
		return false, ""
	}
	loc, err := time.LoadLocation(g.Timezone)
	if err != nil {
		return false, ""
	}
	nowLocal := now.In(loc)
	if nowLocal.Hour() != nagHour {
		return false, ""
	}
	today := nowLocal.Format("2006-01-02")
	return lastDate != today, today
}

// RunDigest spawns the digest tick loop. Blocks until ctx is canceled.
func RunDigest(ctx context.Context, d DigestDeps) {
	if d.Now == nil {
		d.Now = time.Now
	}
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	slog.Info("digest scheduler started", slog.String("tick_interval", TickInterval.String()))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDigestOnce(ctx, d)
		}
	}
}

func runDigestOnce(ctx context.Context, d DigestDeps) {
	groups, err := d.Groups.ListComplete(ctx)
	if err != nil {
		slog.Error("digest: list groups", slog.String("error", err.Error()))
		return
	}
	now := d.Now()
	for _, g := range groups {
		lastDate, _ := d.Tasks.GetMeta(g.ID, "last_digest_date")
		fire, today := ShouldFireDigest(g, lastDate, now)
		if !fire {
			continue
		}
		// Generate via the LLM — the per-group system prompt locks the
		// language and shapes the digest format.
		text, err := d.Agent.HandleMessage(ctx, g.ID, "System", "Generate the daily digest.")
		if err != nil {
			slog.Error("digest: generate", slog.String("group_id", g.ID), slog.String("error", err.Error()))
			continue
		}
		resolved, mentions := agent.ResolveMentions(text)
		var sendErr error
		if len(mentions) > 0 {
			sendErr = d.Messenger.WriteWithMentions(g.ID, resolved, mentions)
		} else {
			sendErr = d.Messenger.Write(g.ID, text)
		}
		if sendErr != nil {
			slog.Error("digest: send", slog.String("group_id", g.ID), slog.String("error", sendErr.Error()))
			continue
		}
		if err := d.Tasks.SetMeta(g.ID, "last_digest_date", today); err != nil {
			slog.Error("digest: mark sent", slog.String("group_id", g.ID), slog.String("error", err.Error()))
		}
		slog.Info("digest sent", slog.String("group_id", g.ID), slog.String("date", today))
	}
}

// RunNag spawns the nag tick loop. Blocks until ctx is canceled.
func RunNag(ctx context.Context, d NagDeps, nagHour int) {
	if d.Now == nil {
		d.Now = time.Now
	}
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	slog.Info("nag scheduler started",
		slog.Int("hour", nagHour),
		slog.Int("threshold", d.Threshold),
		slog.String("tick_interval", TickInterval.String()))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runNagOnce(ctx, d, nagHour)
		}
	}
}

func runNagOnce(ctx context.Context, d NagDeps, nagHour int) {
	groups, err := d.Groups.ListComplete(ctx)
	if err != nil {
		slog.Error("nag: list groups", slog.String("error", err.Error()))
		return
	}
	now := d.Now()
	for _, g := range groups {
		lastDate, _ := d.Tasks.GetMeta(g.ID, "last_nag_date")
		fire, today := ShouldFireNag(g, lastDate, nagHour, now)
		if !fire {
			continue
		}
		counts, err := d.Tasks.CountOverdueByAssignee(g.ID, today)
		if err != nil {
			slog.Error("nag: count overdue", slog.String("group_id", g.ID), slog.String("error", err.Error()))
			continue
		}
		members, err := d.Members.List(ctx, g.ID)
		if err != nil {
			slog.Error("nag: list members", slog.String("group_id", g.ID), slog.String("error", err.Error()))
			continue
		}
		nameToPhone := make(map[string]string, len(members))
		for _, m := range members {
			if m.DisplayName != "" {
				nameToPhone[m.DisplayName] = m.WhatsAppID
			}
		}
		nagged := 0
		for assignee, count := range counts {
			if count < d.Threshold {
				continue
			}
			phone, ok := nameToPhone[assignee]
			if !ok || phone == "" {
				slog.Info("nag: no phone for assignee, skipping",
					slog.String("group_id", g.ID), slog.String("assignee", assignee))
				continue
			}
			msg := fmt.Sprintf("You have %d overdue tasks. That's not a flex. Open the group and sort it out before I start nagging in public.", count)
			if g.Language == "he" {
				msg = fmt.Sprintf("יש לך %d משימות באיחור 😬 הגיע הזמן לטפל בזה — אחרת אני מתחיל לנדנד פומבית בקבוצה.", count)
			}
			if err := d.DM.SendDM(phone, msg); err != nil {
				slog.Error("nag: send DM",
					slog.String("group_id", g.ID),
					slog.String("assignee", assignee),
					slog.String("error", err.Error()))
				continue
			}
			slog.Info("nag DM sent",
				slog.String("group_id", g.ID),
				slog.String("assignee", assignee),
				slog.Int("overdue_count", count))
			nagged++
		}
		if err := d.Tasks.SetMeta(g.ID, "last_nag_date", today); err != nil {
			slog.Error("nag: mark sent", slog.String("group_id", g.ID), slog.String("error", err.Error()))
		}
		slog.Info("nag pass complete", slog.String("group_id", g.ID), slog.Int("nagged", nagged))
	}
}
