package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
)

// Dispatcher routes inbound messages to either the main agent or the
// onboarding agent based on groups.onboarding_state (architecture D1).
// It also bumps groups.last_active_at after each successful handle (D18).
//
// One Dispatcher instance is owned by main.go; both schedulers and the
// inbound message loop call into it.
type Dispatcher struct {
	groups     *db.GroupStore
	main       *Agent
	onboarding *OnboardingAgent
	messenger  messenger.IMessenger
}

func NewDispatcher(groups *db.GroupStore, main *Agent, onboarding *OnboardingAgent, m messenger.IMessenger) *Dispatcher {
	return &Dispatcher{
		groups:     groups,
		main:       main,
		onboarding: onboarding,
		messenger:  m,
	}
}

// Handle processes one inbound message. Loads the group row, routes to the
// appropriate agent, and updates last_active_at. The agents themselves own
// the response delivery — onboarding is silent today (stub) and the main
// agent's response is sent here after HandleMessage returns.
func (d *Dispatcher) Handle(ctx context.Context, groupID, sender, text string) error {
	group, err := d.groups.Get(ctx, groupID)
	if err != nil {
		return fmt.Errorf("dispatcher: load group %s: %w", groupID, err)
	}
	if group == nil {
		// Tenant zero may not have a row in dev (no migration run); fall through
		// to the main agent rather than dropping. In prod the migration backfill
		// guarantees a row exists for tenant zero, and the messenger's auto-create
		// guarantees one for any allowlisted friend group.
		slog.Info("dispatcher: no group row, defaulting to main",
			slog.String("group_id", groupID))
		return d.handleMain(ctx, groupID, sender, text)
	}

	if group.OnboardingState == "complete" {
		if err := d.handleMain(ctx, groupID, sender, text); err != nil {
			return err
		}
	} else {
		if err := d.onboarding.Handle(ctx, groupID, sender, text); err != nil {
			return fmt.Errorf("dispatcher: onboarding handle: %w", err)
		}
	}

	if err := d.groups.UpdateLastActive(ctx, groupID); err != nil {
		// Last-active is observability — log and swallow rather than failing
		// the message handle.
		slog.Error("dispatcher: update last_active_at",
			slog.String("group_id", groupID), slog.String("error", err.Error()))
	}
	return nil
}

func (d *Dispatcher) handleMain(ctx context.Context, groupID, sender, text string) error {
	response, err := d.main.HandleMessage(ctx, groupID, sender, text)
	if err != nil {
		return fmt.Errorf("dispatcher: main handle: %w", err)
	}
	if response == "" {
		return nil
	}
	resolved, mentions := ResolveMentions(response)
	if len(mentions) > 0 {
		return d.messenger.WriteWithMentions(groupID, resolved, mentions)
	}
	return d.messenger.Write(groupID, response)
}
