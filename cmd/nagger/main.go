package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/lizozom/whatsapp-nagger/internal/agent"
	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/ingest"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
)

func main() {
	godotenv.Load()

	tasksDBPath := os.Getenv("TASKS_DB_PATH")
	if tasksDBPath == "" {
		tasksDBPath = "tasks.db"
	}
	store, err := db.NewTaskStore(tasksDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Transaction store (expense tracking). Always opened — the agent uses it
	// for the expense tools, and the ingest server (below) writes to it.
	txStore, err := db.NewTxStore(tasksDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open tx store: %v\n", err)
		os.Exit(1)
	}
	defer txStore.Close()

	a := agent.NewAgent(store, txStore)

	// Optional ingest HTTP server for external scrapers (Cal / Max / ...).
	// Enabled when INGEST_SECRET is set.
	if secret := os.Getenv("INGEST_SECRET"); secret != "" {
		port := os.Getenv("INGEST_PORT")
		if port == "" {
			port = "8080"
		}
		srv := ingest.NewServer(":"+port, ingest.NewHandler(txStore, secret))
		go func() {
			fmt.Fprintf(os.Stderr, "Ingest server listening on :%s\n", port)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "Ingest server error: %v\n", err)
			}
		}()
	}

	var m messenger.IMessenger
	switch os.Getenv("MESSENGER") {
	case "whatsapp":
		groupJID := os.Getenv("WHATSAPP_GROUP_JID") // empty = discovery mode
		dbPath := os.Getenv("WHATSAPP_DB_PATH")
		if dbPath == "" {
			dbPath = "whatsapp_session.db"
		}
		m, err = messenger.NewWhatsApp(dbPath, groupJID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to init WhatsApp: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "WhatsApp messenger connected.")
	default:
		term := messenger.NewTerminal()
		term.Write("Online. Type [Name]: message to start. Ctrl+C to quit.")
		m = term
	}

	if digestHour := os.Getenv("DIGEST_HOUR"); digestHour != "" {
		go startDigestScheduler(digestHour, a, m, store)
	}

	for {
		msg, err := m.Read()
		if err != nil {
			break
		}
		if msg.Text == "" {
			continue
		}

		response, err := a.HandleMessage(msg.Sender, msg.Text)
		if err != nil {
			m.Write("Error: " + err.Error())
			continue
		}

		sendWithMentions(m, response)
	}
}

func sendWithMentions(m messenger.IMessenger, text string) error {
	resolved, mentions := agent.ResolveMentions(text)
	if len(mentions) > 0 {
		return m.WriteWithMentions(resolved, mentions)
	}
	return m.Write(text)
}

func startDigestScheduler(digestHour string, a *agent.Agent, m messenger.IMessenger, store *db.TaskStore) {
	loc, _ := time.LoadLocation("Asia/Jerusalem")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().In(loc)
		currentTime := now.Format("15:04")
		today := now.Format("2006-01-02")

		if currentTime != digestHour {
			continue
		}

		lastDate, _ := store.GetMeta("last_digest_date")
		if lastDate == today {
			continue
		}

		fmt.Fprintln(os.Stderr, "Firing daily digest...")
		digest, err := a.HandleMessage("System", "Generate the daily digest.")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Digest error: %v\n", err)
			continue
		}

		if err := sendWithMentions(m, digest); err != nil {
			fmt.Fprintf(os.Stderr, "Digest send error: %v\n", err)
			continue
		}

		store.SetMeta("last_digest_date", today)
		fmt.Fprintln(os.Stderr, "Daily digest sent.")
	}
}
