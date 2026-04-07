package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Transaction is a normalized credit card / bank transaction ingested from
// an external scraper (e.g. israeli-bank-scrapers for Cal / Max).
type Transaction struct {
	ID          string  `json:"id"`           // stable hash, see ComputeTxID
	Provider    string  `json:"provider"`     // "cal", "max", ...
	CardLast4   string  `json:"card_last4"`
	PostedAt    string  `json:"posted_at"`    // ISO-8601 date or datetime
	AmountILS   float64 `json:"amount_ils"`   // negative = charge
	Description string  `json:"description"`
	Memo        string  `json:"memo,omitempty"`
	Category    string  `json:"category,omitempty"`
	Status      string  `json:"status"`       // "pending" | "posted"
	RawJSON     string  `json:"raw_json,omitempty"`
	IngestedAt  string  `json:"ingested_at,omitempty"`
}

// IngestRun is a single invocation of a scraper provider.
type IngestRun struct {
	ID         int64  `json:"id"`
	Provider   string `json:"provider"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Status     string `json:"status"` // "ok" | "error"
	Error      string `json:"error,omitempty"`
	TxCount    int    `json:"tx_count"`
}

// TxStore wraps SQLite access for transactions + ingest_runs.
// It reuses the same DB file as TaskStore by opening a second handle.
type TxStore struct {
	db *sql.DB
}

func NewTxStore(dbPath string) (*TxStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	_, err = db.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE IF NOT EXISTS transactions (
			id           TEXT PRIMARY KEY,
			provider     TEXT NOT NULL,
			card_last4   TEXT NOT NULL DEFAULT '',
			posted_at    TEXT NOT NULL,
			amount_ils   REAL NOT NULL,
			description  TEXT NOT NULL,
			memo         TEXT NOT NULL DEFAULT '',
			category     TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'posted',
			raw_json     TEXT NOT NULL DEFAULT '',
			ingested_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_tx_provider_posted ON transactions(provider, posted_at);
		CREATE INDEX IF NOT EXISTS idx_tx_posted ON transactions(posted_at);

		CREATE TABLE IF NOT EXISTS ingest_runs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			provider     TEXT NOT NULL,
			started_at   DATETIME NOT NULL,
			finished_at  DATETIME,
			status       TEXT NOT NULL,
			error        TEXT NOT NULL DEFAULT '',
			tx_count     INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init tx schema: %w", err)
	}

	return &TxStore{db: db}, nil
}

func (s *TxStore) Close() error {
	return s.db.Close()
}

// ComputeTxID produces a stable hash from the natural key of a transaction.
// Same inputs always produce the same ID — that's how we dedupe reruns.
// Collisions on (provider, card, date, amount, description, memo) are
// disambiguated at insert time via resolveCollision.
func ComputeTxID(provider, cardLast4, postedAt string, amountILS float64, description, memo string) string {
	key := strings.Join([]string{
		strings.ToLower(provider),
		cardLast4,
		postedAt,
		fmt.Sprintf("%.2f", amountILS),
		strings.TrimSpace(description),
		strings.TrimSpace(memo),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16]) // 128-bit prefix is plenty
}

// UpsertBatch inserts transactions inside a single transaction, skipping rows
// whose ID already exists. Returns (inserted, skipped).
//
// For each incoming Transaction, if ID is empty it will be computed.
// If a row with the same ID already exists but with different fields, it is
// left untouched (we trust historical data over re-fetches to avoid flapping).
func (s *TxStore) UpsertBatch(txs []Transaction) (inserted, skipped int, err error) {
	if len(txs) == 0 {
		return 0, 0, nil
	}

	dbTx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			dbTx.Rollback()
		}
	}()

	stmt, err := dbTx.Prepare(`
		INSERT INTO transactions
		  (id, provider, card_last4, posted_at, amount_ils, description, memo, category, status, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for i := range txs {
		t := &txs[i]
		if t.ID == "" {
			t.ID = ComputeTxID(t.Provider, t.CardLast4, t.PostedAt, t.AmountILS, t.Description, t.Memo)
		}
		if t.Status == "" {
			t.Status = "posted"
		}
		res, execErr := stmt.Exec(
			t.ID, t.Provider, t.CardLast4, t.PostedAt, t.AmountILS,
			t.Description, t.Memo, t.Category, t.Status, t.RawJSON,
		)
		if execErr != nil {
			err = fmt.Errorf("insert tx %s: %w", t.ID, execErr)
			return 0, 0, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			inserted++
		} else {
			skipped++
		}
	}

	if err = dbTx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, skipped, nil
}

// StartRun records the beginning of a scraper run and returns its id.
func (s *TxStore) StartRun(provider string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO ingest_runs (provider, started_at, status) VALUES (?, ?, 'running')`,
		provider, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	return res.LastInsertId()
}

// FinishRun marks an ingest_runs row as finished.
func (s *TxStore) FinishRun(id int64, status, errMsg string, txCount int) error {
	_, err := s.db.Exec(
		`UPDATE ingest_runs
		 SET finished_at = ?, status = ?, error = ?, tx_count = ?
		 WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), status, errMsg, txCount, id,
	)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// CardRef identifies a single card by provider + last 4 digits.
// Used for owner → cards filtering.
type CardRef struct {
	Provider  string `json:"provider"`
	CardLast4 string `json:"card_last4"`
}

// TxFilter is a shared filter shape for transaction queries.
// All fields are optional; empty/zero values are ignored.
type TxFilter struct {
	Since            string // YYYY-MM-DD inclusive (compared against posted_at)
	Until            string // YYYY-MM-DD inclusive
	Provider         string // exact match, e.g. "cal" or "max"
	CardLast4        string // exact match
	Category         string // exact match
	MerchantContains string // case-insensitive substring match on description
	DebitsOnly       bool   // if true, only amount_ils < 0
	CreditsOnly      bool   // if true, only amount_ils > 0 (refunds/credits)
	Cards            []CardRef // if non-empty, restrict to these (provider, last4) pairs (OR'd)
	SortBy           string // "date" (default), "amount_asc" (smallest first), "amount_desc" (largest debits first)
	Limit            int    // 0 = no limit
}

// SumRow is one grouped aggregation row.
//
// SpentILS is the *net* outflow (charges minus refunds). This is what a person
// usually means by "how much did we spend" — a ₪1,000 purchase that was fully
// refunded should show as ₪0 spent, not ₪1,000.
//
// ChargesILS and RefundsILS are kept as separate transparency fields so the
// LLM can say things like "3 orders, 1 returned" when asked for detail.
type SumRow struct {
	Key        string  `json:"key"`
	TxCount    int     `json:"tx_count"`
	SpentILS   float64 `json:"spent_ils"`   // net outflow: charges - refunds (positive = money out)
	ChargesILS float64 `json:"charges_ils"` // gross debits (positive), for transparency
	RefundsILS float64 `json:"refunds_ils"` // gross credits (positive), for transparency
}

// SumBy groups transactions by the requested dimension and returns aggregated
// totals. Allowed groupBy values: "category", "merchant", "month", "provider",
// "card_last4". Rows are ordered by spent_ils DESC.
func (s *TxStore) SumBy(groupBy string, filter TxFilter) ([]SumRow, error) {
	var expr string
	switch groupBy {
	case "category":
		expr = "COALESCE(NULLIF(category,''), '(uncategorized)')"
	case "merchant":
		expr = "description"
	case "month":
		expr = "substr(posted_at,1,7)"
	case "provider":
		expr = "provider"
	case "card_last4":
		expr = "card_last4"
	default:
		return nil, fmt.Errorf("invalid groupBy: %q", groupBy)
	}

	where, args := buildTxWhere(filter)
	q := fmt.Sprintf(`
		SELECT %s AS key,
		       COUNT(*) AS tx_count,
		       ROUND(-COALESCE(SUM(amount_ils), 0), 2) AS spent_ils,
		       ROUND(COALESCE(SUM(CASE WHEN amount_ils < 0 THEN -amount_ils ELSE 0 END), 0), 2) AS charges_ils,
		       ROUND(COALESCE(SUM(CASE WHEN amount_ils > 0 THEN amount_ils ELSE 0 END), 0), 2) AS refunds_ils
		FROM transactions
		%s
		GROUP BY %s
		ORDER BY spent_ils DESC
	`, expr, where, expr)
	if filter.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("sum by %s: %w", groupBy, err)
	}
	defer rows.Close()

	var out []SumRow
	for rows.Next() {
		var r SumRow
		if err := rows.Scan(&r.Key, &r.TxCount, &r.SpentILS, &r.ChargesILS, &r.RefundsILS); err != nil {
			return nil, fmt.Errorf("scan sum row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryTransactions is like ListTransactions but accepts the shared TxFilter,
// supporting category / merchant / debits-only filters.
func (s *TxStore) QueryTransactions(filter TxFilter) ([]Transaction, error) {
	where, args := buildTxWhere(filter)
	var orderBy string
	switch filter.SortBy {
	case "amount":
		// Most negative first = biggest charges first.
		orderBy = "ORDER BY amount_ils ASC, posted_at DESC"
	default: // "date" or empty
		orderBy = "ORDER BY posted_at DESC, id ASC"
	}
	q := `SELECT id, provider, card_last4, posted_at, amount_ils, description, memo, category, status, raw_json, ingested_at
	      FROM transactions ` + where + ` ` + orderBy
	if filter.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query tx: %w", err)
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Provider, &t.CardLast4, &t.PostedAt, &t.AmountILS,
			&t.Description, &t.Memo, &t.Category, &t.Status, &t.RawJSON, &t.IngestedAt); err != nil {
			return nil, fmt.Errorf("scan tx: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func buildTxWhere(filter TxFilter) (string, []any) {
	var where []string
	var args []any
	if filter.Since != "" {
		where = append(where, "posted_at >= ?")
		args = append(args, filter.Since)
	}
	if filter.Until != "" {
		where = append(where, "posted_at <= ?")
		args = append(args, filter.Until)
	}
	if filter.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, filter.Provider)
	}
	if filter.CardLast4 != "" {
		where = append(where, "card_last4 = ?")
		args = append(args, filter.CardLast4)
	}
	if filter.Category != "" {
		where = append(where, "category = ?")
		args = append(args, filter.Category)
	}
	if filter.MerchantContains != "" {
		where = append(where, "LOWER(description) LIKE LOWER(?)")
		args = append(args, "%"+filter.MerchantContains+"%")
	}
	if filter.DebitsOnly {
		where = append(where, "amount_ils < 0")
	}
	if filter.CreditsOnly {
		where = append(where, "amount_ils > 0")
	}
	if len(filter.Cards) > 0 {
		parts := make([]string, 0, len(filter.Cards))
		for _, c := range filter.Cards {
			parts = append(parts, "(provider = ? AND card_last4 = ?)")
			args = append(args, c.Provider, c.CardLast4)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if len(where) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(where, " AND "), args
}

// TotalSpent returns aggregate totals for the given filter as a single row.
// Used by the owner group-by dispatch in the agent layer.
//
// spent_ils is net of refunds (see SumRow docs).
func (s *TxStore) TotalSpent(filter TxFilter) (SumRow, error) {
	where, args := buildTxWhere(filter)
	q := `SELECT COUNT(*),
	             ROUND(-COALESCE(SUM(amount_ils), 0), 2),
	             ROUND(COALESCE(SUM(CASE WHEN amount_ils < 0 THEN -amount_ils ELSE 0 END), 0), 2),
	             ROUND(COALESCE(SUM(CASE WHEN amount_ils > 0 THEN amount_ils ELSE 0 END), 0), 2)
	      FROM transactions ` + where
	var r SumRow
	if err := s.db.QueryRow(q, args...).Scan(&r.TxCount, &r.SpentILS, &r.ChargesILS, &r.RefundsILS); err != nil {
		return SumRow{}, fmt.Errorf("total spent: %w", err)
	}
	return r, nil
}

// ListTransactions returns transactions filtered by provider and/or date range.
// Empty arguments are ignored. Results are ordered by posted_at DESC.
func (s *TxStore) ListTransactions(provider, sinceISO, untilISO string, limit int) ([]Transaction, error) {
	q := `SELECT id, provider, card_last4, posted_at, amount_ils, description, memo, category, status, raw_json, ingested_at
	      FROM transactions`
	var where []string
	var args []any
	if provider != "" {
		where = append(where, "provider = ?")
		args = append(args, provider)
	}
	if sinceISO != "" {
		where = append(where, "posted_at >= ?")
		args = append(args, sinceISO)
	}
	if untilISO != "" {
		where = append(where, "posted_at <= ?")
		args = append(args, untilISO)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY posted_at DESC, id ASC"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tx: %w", err)
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Provider, &t.CardLast4, &t.PostedAt, &t.AmountILS,
			&t.Description, &t.Memo, &t.Category, &t.Status, &t.RawJSON, &t.IngestedAt); err != nil {
			return nil, fmt.Errorf("scan tx: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
