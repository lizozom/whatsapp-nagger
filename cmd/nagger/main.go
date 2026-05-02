package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/lizozom/whatsapp-nagger/internal/agent"
	"github.com/lizozom/whatsapp-nagger/internal/api"
	"github.com/lizozom/whatsapp-nagger/internal/db"
	"github.com/lizozom/whatsapp-nagger/internal/ingest"
	"github.com/lizozom/whatsapp-nagger/internal/messenger"
	"github.com/lizozom/whatsapp-nagger/internal/scheduler"
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

	migrationDB, err := sql.Open("sqlite", tasksDBPath)
	if err != nil {
		slog.Error("open db for migrations", "error", err)
		os.Exit(1)
	}
	if err := db.RunMigrations(migrationDB); err != nil {
		slog.Error("run migrations", "error", err)
		migrationDB.Close()
		os.Exit(1)
	}
	migrationDB.Close()

	// Tenant-zero JID — still used by tenant-zero-only callers (ingest, notify,
	// dashboard auth, schedulers in v1). The dispatcher derives per-message
	// group_id from the inbound envelope; this constant is just for tenant-zero
	// callers that haven't been per-group-ified yet.
	tenantZeroJID := os.Getenv("WHATSAPP_GROUP_JID")
	if tenantZeroJID == "" {
		tenantZeroJID = "dev-group"
	}

	groupStore, err := db.NewGroupStore(tasksDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open group store: %v\n", err)
		os.Exit(1)
	}
	defer groupStore.Close()
	memberStore, err := db.NewMemberStore(tasksDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open member store: %v\n", err)
		os.Exit(1)
	}
	defer memberStore.Close()

	history := agent.NewHistory()
	mainAgent := agent.NewAgent(store, txStore, groupStore, memberStore, history)

	// --- Single HTTP mux for all endpoints ---
	mux := http.NewServeMux()

	// Healthz (always available).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Ingest endpoint (opt-in via INGEST_SECRET). Tenant-zero only per D9.
	ingestSecret := os.Getenv("INGEST_SECRET")
	if ingestSecret != "" {
		mux.Handle("/ingest/transactions", ingest.NewHandler(txStore, ingestSecret, tenantZeroJID))
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
		allowlist := messenger.ParseAllowlist(os.Getenv("ALLOWED_PHONES"))
		wa, waErr := messenger.NewWhatsApp(dbPath, groupJID, allowlist, groupStore)
		if waErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to init WhatsApp: %v\n", waErr)
			os.Exit(1)
		}
		wa.RegisterRoutes(mux)
		m = wa
		otpSender = wa
		slog.Info("WhatsApp messenger connected", slog.Int("allowlist_size", allowlist.Size()))
	default:
		term := messenger.NewTerminal()
		term.Write(tenantZeroJID, "Online. Type [Name]: message to start. Ctrl+C to quit.")
		m = term
	}

	// Notify endpoint — scraper alerts forwarded to tenant-zero group (D9: tenant-zero only in v1).
	if ingestSecret != "" {
		mux.Handle("/notify", &ingest.NotifyHandler{
			Secret: ingestSecret,
			Write:  func(text string) error { return m.Write(tenantZeroJID, text) },
		})
	}

	// Dashboard auth (WhatsApp OTP → JWT). Tenant-zero only per D13.
	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		allowlist := api.BuildAllowlist(api.LoadPersonasFile())
		auth := &api.AuthHandler{
			OTP:          api.NewOTPStore(5 * time.Minute),
			DM:           otpSender,
			Allowlist:    allowlist,
			JWTSecret:    []byte(jwtSecret),
			DashboardURL: os.Getenv("DASHBOARD_URL"),
			GroupID:      tenantZeroJID,
		}
		auth.RegisterAuthRoutes(mux)
		mainAgent.SetDashboardLinker(auth)
		fmt.Fprintf(os.Stderr, "Dashboard auth enabled (%d allowed phones).\n", len(allowlist))
	}

	// Two-agent dispatcher: routes by groups.onboarding_state per message (D1).
	onboardingAgent := agent.NewOnboardingAgent(groupStore, memberStore, history, m)
	dispatcher := agent.NewDispatcher(groupStore, mainAgent, onboardingAgent, m)

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

	// Group-iterating digest scheduler (Story 2.7). Per-group hour + tz from
	// the groups table — no global DIGEST_HOUR env required, but we keep an
	// env presence check to allow disabling all scheduling for dev runs.
	if os.Getenv("DIGEST_HOUR") != "" || os.Getenv("MESSENGER") == "whatsapp" {
		go scheduler.RunDigest(context.Background(), scheduler.DigestDeps{
			Groups:    groupStore,
			Tasks:     store,
			Agent:     mainAgent,
			Messenger: m,
		})
	}

	// Nag DM scheduler. Still uses a global NAG_HOUR (operator policy, not
	// per-group preference) and fires only in WhatsApp mode (terminal has
	// no DM channel).
	if nagHourStr := os.Getenv("NAG_HOUR"); nagHourStr != "" {
		if wa, ok := m.(*messenger.WhatsApp); ok {
			nagHour, err := strconv.Atoi(strings.TrimSuffix(nagHourStr, ":00"))
			if err != nil || nagHour < 0 || nagHour > 23 {
				fmt.Fprintf(os.Stderr, "NAG_HOUR must be 0..23, got %q — nag scheduler disabled\n", nagHourStr)
			} else {
				threshold := 4
				if v, err := strconv.Atoi(os.Getenv("NAG_THRESHOLD")); err == nil && v > 0 {
					threshold = v
				}
				go scheduler.RunNag(context.Background(), scheduler.NagDeps{
					Groups:    groupStore,
					Members:   memberStore,
					Tasks:     store,
					DM:        wa,
					Threshold: threshold,
				}, nagHour)
			}
		}
	}

	for {
		msg, err := m.Read()
		if err != nil {
			break
		}
		if msg.Text == "" {
			continue
		}

		if err := dispatcher.Handle(context.Background(), msg.GroupID, msg.Sender, msg.Text); err != nil {
			slog.Error("dispatch handle failed",
				slog.String("group_id", msg.GroupID),
				slog.String("error", err.Error()))
			m.Write(msg.GroupID, "Error: "+err.Error())
		}
	}
}

