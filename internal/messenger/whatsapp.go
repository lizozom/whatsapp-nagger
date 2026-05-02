package messenger

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "modernc.org/sqlite"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// groupInfoFn returns the international-format-no-`+` phone numbers of every
// participant in the given group JID, plus the group's display name.
// Abstracted as a function pointer so tests can stub it without going through
// whatsmeow's network call.
type groupInfoFn func(ctx context.Context, jid types.JID) (phones []string, name string, err error)

type WhatsApp struct {
	client        *whatsmeow.Client
	tenantZeroJID types.JID // discovery / pairing target only; runtime gating no longer special-cases this JID
	allowlist     *Allowlist
	groups        *db.GroupStore
	groupInfo     groupInfoFn // test seam — defaults to wa.fetchGroupInfo
	incoming      chan Message
	discovery     bool
	paired        bool
	pairCode      string // 8-digit pairing code for web display
}

func NewWhatsApp(dbPath string, tenantZeroJID string, allowlist *Allowlist, groups *db.GroupStore) (*WhatsApp, error) {
	var gJID types.JID
	discovery := tenantZeroJID == ""
	if !discovery {
		var err error
		gJID, err = types.ParseJID(tenantZeroJID)
		if err != nil {
			return nil, fmt.Errorf("invalid group JID %q: %w", tenantZeroJID, err)
		}
	}

	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open session db: %w", err)
	}

	container := sqlstore.NewWithDB(sqlDB, "sqlite3", nil)
	if err := container.Upgrade(context.Background()); err != nil {
		// If the session DB is corrupt, delete it and start fresh
		fmt.Fprintf(os.Stderr, "Session DB corrupt (%v), deleting and starting fresh...\n", err)
		sqlDB.Close()
		os.Remove(dbPath)
		sqlDB, err = sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath))
		if err != nil {
			return nil, fmt.Errorf("reopen session db: %w", err)
		}
		container = sqlstore.NewWithDB(sqlDB, "sqlite3", nil)
		if err := container.Upgrade(context.Background()); err != nil {
			return nil, fmt.Errorf("upgrade session store (retry): %w", err)
		}
	}

	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	client := whatsmeow.NewClient(device, nil)

	wa := &WhatsApp{
		client:        client,
		tenantZeroJID: gJID,
		allowlist:     allowlist,
		groups:        groups,
		incoming:      make(chan Message, 64),
		discovery:     discovery,
		paired:        client.Store.ID != nil,
	}
	wa.groupInfo = wa.fetchGroupInfo

	if discovery {
		fmt.Fprintln(os.Stderr, "Discovery mode: no WHATSAPP_GROUP_JID set. Will print all group messages with JIDs.")
	}

	client.AddEventHandler(wa.handleEvent)

	// HTTP routes are registered externally via RegisterRoutes.
	// The caller (main.go) owns the mux and the listener.

	if !wa.paired {
		// Run phone pairing in background — don't block startup
		go wa.pairWithPhone()
	} else {
		if err := client.Connect(); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
	}

	// Disconnect cleanly on SIGINT/SIGTERM
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		client.Disconnect()
		os.Exit(0)
	}()

	return wa, nil
}

func (wa *WhatsApp) pairWithPhone() {
	phone := os.Getenv("WHATSAPP_PHONE")
	if phone == "" {
		fmt.Fprintln(os.Stderr, "WHATSAPP_PHONE not set — cannot pair. Set it to your number in international format (e.g. 972501234567)")
		return
	}

	// Need to connect first and wait for QR event before calling PairPhone
	qrChan, err := wa.client.GetQRChannel(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "QR channel error: %v\n", err)
		return
	}
	if err := wa.client.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Connect error: %v\n", err)
		return
	}

	// Wait for first QR event to confirm connection is ready
	firstEvent := <-qrChan
	if firstEvent.Event != "code" {
		fmt.Fprintf(os.Stderr, "Unexpected first event: %s\n", firstEvent.Event)
		return
	}

	code, err := wa.client.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		fmt.Fprintf(os.Stderr, "PairPhone error: %v\n", err)
		return
	}

	wa.pairCode = code
	fmt.Fprintf(os.Stderr, "=== PAIRING CODE: %s ===\n", code)
	fmt.Fprintln(os.Stderr, "Enter this code in WhatsApp > Linked Devices > Link with phone number")
	fmt.Fprintf(os.Stderr, "Or visit: https://whatsapp-nagger.fly.dev/pair\n")

	// Wait for remaining QR channel events (will close on success/timeout)
	for item := range qrChan {
		if item.Event == "success" {
			fmt.Fprintln(os.Stderr, "Paired successfully!")
			wa.paired = true
			return
		}
	}
}

func (wa *WhatsApp) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.PairSuccess:
		fmt.Fprintln(os.Stderr, "Paired successfully!")
		wa.paired = true
	case *events.Connected:
		fmt.Fprintln(os.Stderr, "WhatsApp connected.")
		wa.paired = true
	case *events.Message:
		chat := v.Info.Chat
		isGroup := chat.Server == types.GroupServer

		if wa.discovery {
			if !isGroup {
				return
			}
			text := extractText(v.Message)
			sender := v.Info.PushName
			if sender == "" {
				sender = v.Info.Sender.User
			}
			fmt.Fprintf(os.Stderr, "[DISCOVERY] Group JID: %s | Sender: %s | Text: %s\n", chat.String(), sender, text)
			return
		}

		// Skip messages sent by this bot (prevent echo loop)
		if wa.client.Store.ID != nil && v.Info.Sender.ToNonAD().String() == wa.client.Store.ID.ToNonAD().String() {
			return
		}

		text := extractText(v.Message)
		if text == "" {
			return
		}

		sender := v.Info.PushName
		if sender == "" {
			sender = v.Info.Sender.User
		}

		// Allowlist + auto-create gating (Story 2.1). Returns true only for
		// tenant-zero groups; non-tenant-zero allowlisted messages are silently
		// consumed pending Story 2.2's dispatcher.
		if !wa.gateInbound(context.Background(), chat) {
			return
		}

		fmt.Fprintf(os.Stderr, "[MSG] %s: %s\n", sender, text)
		wa.incoming <- Message{GroupID: chat.String(), Sender: sender, Text: text}

	case *events.GroupInfo:
		// Bot kicked / removed from a group → no-op (D17). The groups row,
		// members, tasks, and conversation history are all preserved so a
		// later re-add resumes seamlessly via Story 2.1's auto-create skip
		// (existing row → no AutoCreate, dispatcher routes by state).
		if wa.client.Store.ID == nil {
			return
		}
		if isBotLeaving(v.Leave, *wa.client.Store.ID) {
			slog.Info("bot removed from group",
				slog.String("group_id", v.JID.String()))
		}
	}
}

// isBotLeaving reports whether the bot's JID appears in the Leave list of a
// GroupInfo event. Pure function for testability — handles ID/AD-suffix
// normalization via ToNonAD so device-suffix variants compare correctly.
func isBotLeaving(leavers []types.JID, botJID types.JID) bool {
	bot := botJID.ToNonAD().String()
	for _, j := range leavers {
		if j.ToNonAD().String() == bot {
			return true
		}
	}
	return false
}

// gateInbound applies allowlist gating + group auto-create, then delivers
// every allowlisted message to the main loop. The dispatcher (Story 2.2)
// downstream routes by groups.onboarding_state. Returns false only for
// groups with no allowlisted participants — those are dropped silently
// with no DB writes, no agent invocation, no presence change.
//
// Every allowlisted group is treated identically: if a groups row exists,
// reuse it; otherwise auto-create. The legacy "tenant-zero" group has no
// runtime special-case — its row exists because the migration backfill
// created it, just like a friend group's row exists because AutoCreate did.
func (wa *WhatsApp) gateInbound(ctx context.Context, chat types.JID) bool {
	phones, name, err := wa.groupInfo(ctx, chat)
	if err != nil {
		// Fail closed — without a participant list we can't prove the group
		// has an allowlisted member, so we treat it as non-allowlisted.
		slog.Info("dropped inbound (group info lookup failed)",
			slog.String("group_id", "<dropped>"),
			slog.String("error", err.Error()),
		)
		return false
	}

	allowed := wa.allowlist.FilterAllowed(phones)
	if len(allowed) == 0 {
		slog.Info("dropped inbound (no allowlisted participants)",
			slog.String("group_id", "<dropped>"),
		)
		return false
	}

	jid := chat.String()

	if wa.groups != nil {
		row, err := wa.groups.Get(ctx, jid)
		if err != nil {
			slog.Error("groups.Get failed", slog.String("group_id", jid), slog.String("error", err.Error()))
			return false
		}
		if row == nil {
			if err := wa.groups.AutoCreate(ctx, jid, name, allowed); err != nil {
				slog.Error("groups.AutoCreate failed", slog.String("group_id", jid), slog.String("error", err.Error()))
				return false
			}
			slog.Info("auto-created group", slog.String("group_id", jid), slog.String("name", name))
		}
	}

	return true
}

// fetchGroupInfo resolves the participants' international-format phones plus
// the group's display name via whatsmeow's GetGroupInfo. PhoneNumber is
// preferred over JID because newer WhatsApp deployments expose participants
// as LIDs in the JID field — PhoneNumber holds the actual phone.
func (wa *WhatsApp) fetchGroupInfo(ctx context.Context, jid types.JID) ([]string, string, error) {
	info, err := wa.client.GetGroupInfo(ctx, jid)
	if err != nil {
		return nil, "", err
	}
	phones := make([]string, 0, len(info.Participants))
	for _, p := range info.Participants {
		switch {
		case p.PhoneNumber.User != "":
			phones = append(phones, p.PhoneNumber.User)
		case p.JID.Server == types.DefaultUserServer && p.JID.User != "":
			phones = append(phones, p.JID.User)
		}
	}
	return phones, info.Name, nil
}

func extractText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if t := msg.GetConversation(); t != "" {
		return t
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	return ""
}

func (wa *WhatsApp) Read() (Message, error) {
	msg, ok := <-wa.incoming
	if !ok {
		return Message{}, fmt.Errorf("channel closed")
	}
	return msg, nil
}

func (wa *WhatsApp) Write(groupID, text string) error {
	if !wa.paired {
		return fmt.Errorf("not paired yet — scan QR code first")
	}
	jid, err := types.ParseJID(groupID)
	if err != nil {
		return fmt.Errorf("invalid group_id %q: %w", groupID, err)
	}
	_, err = wa.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String("🤖 " + text),
	})
	return err
}

// RegisterRoutes adds WhatsApp-specific HTTP handlers (health check, pairing
// page) to the provided mux. Call this from main.go instead of having the
// WhatsApp struct start its own HTTP listener.
func (wa *WhatsApp) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if wa.paired {
			fmt.Fprint(w, "paired")
		} else {
			fmt.Fprint(w, "waiting for QR pairing")
		}
	})
	mux.HandleFunc("/pair", func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("QR_TOKEN")
		if token != "" && r.URL.Query().Get("token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if wa.paired {
			fmt.Fprint(w, "<h1>Already paired!</h1>")
			return
		}
		if wa.pairCode == "" {
			fmt.Fprint(w, `<h1>Generating pairing code...</h1><meta http-equiv='refresh' content='3'>`)
			return
		}
		fmt.Fprintf(w, `<html><body style="display:flex;justify-content:center;align-items:center;height:100vh;margin:0;flex-direction:column;font-family:sans-serif">
<h2>WhatsApp Pairing Code</h2>
<p style="font-size:48px;letter-spacing:8px;font-weight:bold">%s</p>
<p>Open WhatsApp &gt; Linked Devices &gt; Link a Device &gt; Link with phone number</p>
<p>Enter this code. It expires in ~2 minutes.</p>
<meta http-equiv='refresh' content='10'>
</body></html>`, wa.pairCode)
	})
}

// SendDM sends a direct (private) message to an individual phone number.
// phone should be in international format without +, e.g. "972501234567".
// This is NOT part of the IMessenger interface — it's used for OTP delivery.
func (wa *WhatsApp) SendDM(phone, text string) error {
	if !wa.paired {
		return fmt.Errorf("not paired yet")
	}
	jid, err := types.ParseJID(phone + "@s.whatsapp.net")
	if err != nil {
		return fmt.Errorf("invalid phone %q: %w", phone, err)
	}
	_, err = wa.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(text),
	})
	return err
}

func (wa *WhatsApp) WriteWithMentions(groupID, text string, mentions []Mention) error {
	if !wa.paired {
		return fmt.Errorf("not paired yet — scan QR code first")
	}
	if len(mentions) == 0 {
		return wa.Write(groupID, text)
	}
	jid, err := types.ParseJID(groupID)
	if err != nil {
		return fmt.Errorf("invalid group_id %q: %w", groupID, err)
	}

	jids := make([]string, len(mentions))
	for i, m := range mentions {
		jids[i] = m.Phone + "@s.whatsapp.net"
	}

	_, err = wa.client.SendMessage(context.Background(), jid, &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("🤖 " + text),
			ContextInfo: &waE2E.ContextInfo{
				MentionedJID: jids,
			},
		},
	})
	return err
}
