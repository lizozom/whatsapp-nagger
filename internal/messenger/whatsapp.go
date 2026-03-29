package messenger

import (
	"context"
	"database/sql"
	"fmt"
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
)

type WhatsApp struct {
	client    *whatsmeow.Client
	groupJID  types.JID
	incoming  chan Message
	discovery bool
	paired    bool
	pairCode  string // 8-digit pairing code for web display
}

func NewWhatsApp(dbPath string, groupJID string) (*WhatsApp, error) {
	var gJID types.JID
	discovery := groupJID == ""
	if !discovery {
		var err error
		gJID, err = types.ParseJID(groupJID)
		if err != nil {
			return nil, fmt.Errorf("invalid group JID %q: %w", groupJID, err)
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
		client:    client,
		groupJID:  gJID,
		incoming:  make(chan Message, 64),
		discovery: discovery,
		paired:    client.Store.ID != nil,
	}

	if discovery {
		fmt.Fprintln(os.Stderr, "Discovery mode: no WHATSAPP_GROUP_JID set. Will print all group messages with JIDs.")
	}

	client.AddEventHandler(wa.handleEvent)

	// Start HTTP server for health checks and QR pairing page
	go func() {
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if wa.paired {
				fmt.Fprint(w, "paired")
			} else {
				fmt.Fprint(w, "waiting for QR pairing")
			}
		})
		http.HandleFunc("/pair", func(w http.ResponseWriter, r *http.Request) {
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
		http.ListenAndServe(":8080", nil)
	}()

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

		if chat.String() != wa.groupJID.String() {
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

		fmt.Fprintf(os.Stderr, "[MSG] %s: %s\n", sender, text)
		wa.incoming <- Message{Sender: sender, Text: text}
	}
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

func (wa *WhatsApp) Write(text string) error {
	if !wa.paired {
		return fmt.Errorf("not paired yet — scan QR code first")
	}
	_, err := wa.client.SendMessage(context.Background(), wa.groupJID, &waE2E.Message{
		Conversation: proto.String(text),
	})
	return err
}
