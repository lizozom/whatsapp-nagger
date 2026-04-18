package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/lizozom/whatsapp-nagger/internal/agent"
	"github.com/lizozom/whatsapp-nagger/internal/api"
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

	txStore, err := db.NewTxStore(tasksDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open tx store: %v\n", err)
		os.Exit(1)
	}
	defer txStore.Close()

	a := agent.NewAgent(store, txStore)

	// --- Single HTTP mux for all endpoints ---
	mux := http.NewServeMux()

	// Healthz (always available).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Ingest endpoint (opt-in via INGEST_SECRET).
	ingestSecret := os.Getenv("INGEST_SECRET")
	if ingestSecret != "" {
		mux.Handle("/ingest/transactions", ingest.NewHandler(txStore, ingestSecret))
	}

	// Messenger setup.
	var m messenger.IMessenger
	var otpSender api.OTPSender // non-nil only in WhatsApp mode
	switch os.Getenv("MESSENGER") {
	case "whatsapp":
		groupJID := os.Getenv("WHATSAPP_GROUP_JID")
		dbPath := os.Getenv("WHATSAPP_DB_PATH")
		if dbPath == "" {
			dbPath = "whatsapp_session.db"
		}
		wa, waErr := messenger.NewWhatsApp(dbPath, groupJID)
		if waErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to init WhatsApp: %v\n", waErr)
			os.Exit(1)
		}
		wa.RegisterRoutes(mux)
		m = wa
		otpSender = wa
		fmt.Fprintln(os.Stderr, "WhatsApp messenger connected.")
	default:
		term := messenger.NewTerminal()
		term.Write("Online. Type [Name]: message to start. Ctrl+C to quit.")
		m = term
	}

	// Notify endpoint — scraper alerts forwarded to group chat (same HMAC secret).
	if ingestSecret != "" {
		mux.Handle("/notify", &ingest.NotifyHandler{
			Secret: ingestSecret,
			Write:  m.Write,
		})
	}

	// Dashboard auth (WhatsApp OTP → JWT).
	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		allowlist := api.BuildAllowlist(api.LoadPersonasFile())
		auth := &api.AuthHandler{
			OTP:          api.NewOTPStore(5 * time.Minute),
			DM:           otpSender,
			Allowlist:    allowlist,
			JWTSecret:    []byte(jwtSecret),
			DashboardURL: os.Getenv("DASHBOARD_URL"),
		}
		auth.RegisterAuthRoutes(mux)
		a.SetDashboardLinker(auth)
		fmt.Fprintf(os.Stderr, "Dashboard auth enabled (%d allowed phones).\n", len(allowlist))
	}

	// Start the single HTTP server.
	port := os.Getenv("INGEST_PORT")
	if port == "" {
		port = "8080"
	}
	srv := api.NewServer(":"+port, mux)
	go func() {
		fmt.Fprintf(os.Stderr, "HTTP server listening on :%s\n", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
		}
	}()

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
