package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// GET /api/transactions?since=&until=&provider=&category=&merchant=&card_last4=&debits_only=&credits_only=&sort_by=&limit=
func (rt *Router) handleTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	since, until := defaultBillingCycleRange(q.Get("since"), q.Get("until"))
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	filter := db.TxFilter{
		Since:            since,
		Until:            until,
		Provider:         q.Get("provider"),
		CardLast4:        q.Get("card_last4"),
		Category:         q.Get("category"),
		MerchantContains: q.Get("merchant"),
		DebitsOnly:       q.Get("debits_only") == "true",
		CreditsOnly:      q.Get("credits_only") == "true",
		SortBy:           q.Get("sort_by"),
		Limit:            limit,
	}

	txs, err := rt.Tx.QueryTransactions(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"transactions": txs,
		"count":        len(txs),
		"since":        since,
		"until":        until,
	})
}

// GET /api/transactions/summary?group_by=category&since=&until=&provider=&category=&merchant=&limit=
func (rt *Router) handleTransactionsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	groupBy := q.Get("group_by")
	if groupBy == "" {
		groupBy = "category"
	}
	since, until := defaultBillingCycleRange(q.Get("since"), q.Get("until"))
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	filter := db.TxFilter{
		Since:            since,
		Until:            until,
		Provider:         q.Get("provider"),
		Category:         q.Get("category"),
		MerchantContains: q.Get("merchant"),
		Limit:            limit,
	}

	rows, err := rt.Tx.SumBy(groupBy, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"group_by": groupBy,
		"rows":     rows,
		"since":    since,
		"until":    until,
	})
}

// GET /api/transactions/totals?since=&until=&provider=
func (rt *Router) handleTransactionsTotals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	since, until := defaultBillingCycleRange(q.Get("since"), q.Get("until"))

	filter := db.TxFilter{
		Since:    since,
		Until:    until,
		Provider: q.Get("provider"),
	}

	row, err := rt.Tx.TotalSpent(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"spent_ils":   row.SpentILS,
		"charges_ils": row.ChargesILS,
		"refunds_ils": row.RefundsILS,
		"tx_count":    row.TxCount,
		"since":       since,
		"until":       until,
	})
}

// writeJSON encodes v as JSON and writes it to w.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// defaultBillingCycleRange mirrors the logic in internal/agent/agent.go.
// Returns the current billing cycle if since/until are empty.
func defaultBillingCycleRange(since, until string) (string, string) {
	if since != "" && until != "" {
		return since, until
	}
	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Asia/Jerusalem"
	}
	loc, _ := time.LoadLocation(tz)
	now := time.Now().In(loc)

	billingDay := 10
	if v := os.Getenv("BILLING_DAY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 28 {
			billingDay = n
		}
	}

	var cycleStart time.Time
	if now.Day() >= billingDay {
		cycleStart = time.Date(now.Year(), now.Month(), billingDay, 0, 0, 0, 0, loc)
	} else {
		cycleStart = time.Date(now.Year(), now.Month()-1, billingDay, 0, 0, 0, 0, loc)
	}
	cycleEnd := cycleStart.AddDate(0, 1, -1)

	if since == "" {
		since = cycleStart.Format("2006-01-02")
	}
	if until == "" {
		until = cycleEnd.Format("2006-01-02")
	}
	return since, until
}
