package db

import (
	"path/filepath"
	"testing"
)

func newTestTxStore(t *testing.T) *TxStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tx.db")
	store, err := NewTxStore(dbPath)
	if err != nil {
		t.Fatalf("NewTxStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestComputeTxIDStable(t *testing.T) {
	a := ComputeTxID("cal", "1234", "2026-04-01", -42.50, "SHUFERSAL", "")
	b := ComputeTxID("cal", "1234", "2026-04-01", -42.50, "SHUFERSAL", "")
	if a != b {
		t.Errorf("expected stable id, got %q vs %q", a, b)
	}
	if a == "" {
		t.Error("expected non-empty id")
	}
}

func TestComputeTxIDCaseInsensitiveProvider(t *testing.T) {
	a := ComputeTxID("CAL", "1234", "2026-04-01", -10, "x", "")
	b := ComputeTxID("cal", "1234", "2026-04-01", -10, "x", "")
	if a != b {
		t.Errorf("provider case should be normalized: %q vs %q", a, b)
	}
}

func TestUpsertBatchInsertsAndDedupes(t *testing.T) {
	store := newTestTxStore(t)

	txs := []Transaction{
		{Provider: "cal", CardLast4: "1234", PostedAt: "2026-04-01", AmountILS: -42.5, Description: "SHUFERSAL"},
		{Provider: "cal", CardLast4: "1234", PostedAt: "2026-04-02", AmountILS: -12.0, Description: "CAFE"},
	}

	ins, skip, err := store.UpsertBatch(txs)
	if err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	if ins != 2 || skip != 0 {
		t.Errorf("first run: expected 2 inserted / 0 skipped, got %d / %d", ins, skip)
	}

	// Re-run: same payload should be a no-op.
	ins, skip, err = store.UpsertBatch(txs)
	if err != nil {
		t.Fatalf("UpsertBatch rerun: %v", err)
	}
	if ins != 0 || skip != 2 {
		t.Errorf("rerun: expected 0 inserted / 2 skipped, got %d / %d", ins, skip)
	}
}

func TestUpsertBatchMixedNewAndOld(t *testing.T) {
	store := newTestTxStore(t)

	_, _, err := store.UpsertBatch([]Transaction{
		{Provider: "max", CardLast4: "9999", PostedAt: "2026-04-01", AmountILS: -100, Description: "A"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	ins, skip, err := store.UpsertBatch([]Transaction{
		{Provider: "max", CardLast4: "9999", PostedAt: "2026-04-01", AmountILS: -100, Description: "A"}, // dup
		{Provider: "max", CardLast4: "9999", PostedAt: "2026-04-02", AmountILS: -200, Description: "B"}, // new
	})
	if err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	if ins != 1 || skip != 1 {
		t.Errorf("expected 1/1, got %d/%d", ins, skip)
	}
}

func TestListTransactionsFilters(t *testing.T) {
	store := newTestTxStore(t)
	_, _, err := store.UpsertBatch([]Transaction{
		{Provider: "cal", PostedAt: "2026-03-15", AmountILS: -10, Description: "old"},
		{Provider: "cal", PostedAt: "2026-04-01", AmountILS: -20, Description: "new"},
		{Provider: "max", PostedAt: "2026-04-02", AmountILS: -30, Description: "other provider"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	calOnly, err := store.ListTransactions("cal", "", "", 0)
	if err != nil {
		t.Fatalf("list cal: %v", err)
	}
	if len(calOnly) != 2 {
		t.Errorf("expected 2 cal tx, got %d", len(calOnly))
	}

	sinceApril, err := store.ListTransactions("", "2026-04-01", "", 0)
	if err != nil {
		t.Fatalf("list since: %v", err)
	}
	if len(sinceApril) != 2 {
		t.Errorf("expected 2 since 2026-04-01, got %d", len(sinceApril))
	}
}

func seedExpenseFixture(t *testing.T, store *TxStore) {
	t.Helper()
	_, _, err := store.UpsertBatch([]Transaction{
		// January: groceries + restaurant
		{Provider: "max", CardLast4: "1111", PostedAt: "2026-01-05", AmountILS: -200, Description: "SHUFERSAL", Category: "מזון וצריכה"},
		{Provider: "max", CardLast4: "1111", PostedAt: "2026-01-10", AmountILS: -150, Description: "WOLT", Category: "מסעדות, קפה וברים"},
		{Provider: "max", CardLast4: "1111", PostedAt: "2026-01-15", AmountILS: -80, Description: "CAFE", Category: "מסעדות, קפה וברים"},
		// February: groceries, refund, gas
		{Provider: "max", CardLast4: "1111", PostedAt: "2026-02-02", AmountILS: -300, Description: "SHUFERSAL", Category: "מזון וצריכה"},
		{Provider: "max", CardLast4: "1111", PostedAt: "2026-02-03", AmountILS: 50, Description: "REFUND", Category: "מזון וצריכה"}, // credit
		{Provider: "cal", CardLast4: "2222", PostedAt: "2026-02-20", AmountILS: -400, Description: "PAZ", Category: "דלק"},
		// March: one entry on other card
		{Provider: "cal", CardLast4: "3333", PostedAt: "2026-03-01", AmountILS: -1000, Description: "IKEA", Category: "עיצוב הבית"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestSumByCategory(t *testing.T) {
	store := newTestTxStore(t)
	seedExpenseFixture(t, store)

	rows, err := store.SumBy("category", TxFilter{Since: "2026-01-01", Until: "2026-01-31"})
	if err != nil {
		t.Fatalf("SumBy: %v", err)
	}
	// Expect 2 categories: groceries (200) and restaurants (230).
	if len(rows) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(rows), rows)
	}
	// Top group by spent_ils should be restaurants (230 > 200).
	if rows[0].Key != "מסעדות, קפה וברים" || rows[0].SpentILS != 230 {
		t.Errorf("top row wrong: %+v", rows[0])
	}
	if rows[1].Key != "מזון וצריכה" || rows[1].SpentILS != 200 {
		t.Errorf("second row wrong: %+v", rows[1])
	}
}

// TestSumByNetsRefunds is the ShoesOnLine bug regression test.
// A charge of ₪1054 cancelled by a refund of ₪1054 must show spent_ils = 0,
// not 1054. charges_ils and refunds_ils stay available for transparency.
func TestSumByNetsRefunds(t *testing.T) {
	store := newTestTxStore(t)
	_, _, err := store.UpsertBatch([]Transaction{
		{Provider: "cal", CardLast4: "1234", PostedAt: "2026-03-01", AmountILS: -1054, Description: "SHOESONLINE"},
		{Provider: "cal", CardLast4: "1234", PostedAt: "2026-03-10", AmountILS: 1054, Description: "SHOESONLINE"},
		// A separate groceries transaction so we can check ordering.
		{Provider: "cal", CardLast4: "1234", PostedAt: "2026-03-05", AmountILS: -200, Description: "SHUFERSAL"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := store.SumBy("merchant", TxFilter{})
	if err != nil {
		t.Fatalf("SumBy: %v", err)
	}

	var shoes, shufersal *SumRow
	for i := range rows {
		switch rows[i].Key {
		case "SHOESONLINE":
			shoes = &rows[i]
		case "SHUFERSAL":
			shufersal = &rows[i]
		}
	}
	if shoes == nil || shufersal == nil {
		t.Fatalf("missing expected rows: %+v", rows)
	}

	// ShoesOnLine: bought and returned. spent_ils MUST be 0, not 1054.
	if shoes.SpentILS != 0 {
		t.Errorf("ShoesOnLine spent_ils should be 0 (charge+refund cancel), got %v", shoes.SpentILS)
	}
	if shoes.ChargesILS != 1054 {
		t.Errorf("ShoesOnLine charges_ils should be 1054, got %v", shoes.ChargesILS)
	}
	if shoes.RefundsILS != 1054 {
		t.Errorf("ShoesOnLine refunds_ils should be 1054, got %v", shoes.RefundsILS)
	}
	if shoes.TxCount != 2 {
		t.Errorf("ShoesOnLine tx_count should be 2, got %d", shoes.TxCount)
	}

	// Shufersal: straightforward charge.
	if shufersal.SpentILS != 200 {
		t.Errorf("Shufersal spent_ils wrong: %+v", *shufersal)
	}

	// Shufersal (₪200) should rank above ShoesOnLine (₪0) when ordered by spent_ils DESC.
	if rows[0].Key != "SHUFERSAL" {
		t.Errorf("ordering wrong: top should be SHUFERSAL, got %q", rows[0].Key)
	}
}

func TestSumByMonth(t *testing.T) {
	store := newTestTxStore(t)
	seedExpenseFixture(t, store)

	rows, err := store.SumBy("month", TxFilter{})
	if err != nil {
		t.Fatalf("SumBy month: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 months, got %d", len(rows))
	}
	// March: IKEA 1000. Feb: Shufersal 300 + Paz 400 - refund 50 = 650 net. Jan: 430.
	if rows[0].Key != "2026-03" || rows[0].SpentILS != 1000 {
		t.Errorf("Mar: %+v", rows[0])
	}
	if rows[1].Key != "2026-02" || rows[1].SpentILS != 650 {
		t.Errorf("Feb: %+v", rows[1])
	}
	if rows[2].Key != "2026-01" || rows[2].SpentILS != 430 {
		t.Errorf("Jan: %+v", rows[2])
	}
}

func TestSumByMerchant(t *testing.T) {
	store := newTestTxStore(t)
	seedExpenseFixture(t, store)

	rows, err := store.SumBy("merchant", TxFilter{Provider: "max"})
	if err != nil {
		t.Fatalf("SumBy merchant: %v", err)
	}
	// SHUFERSAL total = 500 (Jan 200 + Feb 300); WOLT 150; CAFE 80; REFUND is a pure credit.
	if rows[0].Key != "SHUFERSAL" || rows[0].SpentILS != 500 {
		t.Errorf("top: %+v", rows[0])
	}
	// REFUND is a pure credit row: spent_ils should be -50 (money came IN, negative outflow)
	// and refunds_ils should be 50.
	var refund *SumRow
	for i := range rows {
		if rows[i].Key == "REFUND" {
			refund = &rows[i]
		}
	}
	if refund == nil {
		t.Fatal("REFUND row missing")
	}
	if refund.SpentILS != -50 {
		t.Errorf("REFUND spent_ils should be -50 (net inflow), got %v", refund.SpentILS)
	}
	if refund.RefundsILS != 50 {
		t.Errorf("REFUND refunds_ils should be 50, got %v", refund.RefundsILS)
	}
	if refund.ChargesILS != 0 {
		t.Errorf("REFUND charges_ils should be 0, got %v", refund.ChargesILS)
	}
}

func TestSumByFilterMerchantContains(t *testing.T) {
	store := newTestTxStore(t)
	seedExpenseFixture(t, store)

	rows, err := store.SumBy("month", TxFilter{MerchantContains: "shufersal"}) // lowercase — should still match
	if err != nil {
		t.Fatalf("SumBy: %v", err)
	}
	// Two months have SHUFERSAL: 2026-01 (200) and 2026-02 (300).
	if len(rows) != 2 {
		t.Fatalf("expected 2 months, got %d", len(rows))
	}
}

func TestSumByInvalidGroupBy(t *testing.T) {
	store := newTestTxStore(t)
	_, err := store.SumBy("bogus", TxFilter{})
	if err == nil {
		t.Error("expected error for invalid groupBy")
	}
}

func TestQueryTransactionsFilters(t *testing.T) {
	store := newTestTxStore(t)
	seedExpenseFixture(t, store)

	// Debits only, January, MAX.
	rows, err := store.QueryTransactions(TxFilter{
		Since:      "2026-01-01",
		Until:      "2026-01-31",
		Provider:   "max",
		DebitsOnly: true,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 January debits, got %d", len(rows))
	}

	// Shufersal only.
	rows, err = store.QueryTransactions(TxFilter{MerchantContains: "SHUFERSAL"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 shufersal rows, got %d", len(rows))
	}
}

func TestIngestRunLifecycle(t *testing.T) {
	store := newTestTxStore(t)

	id, err := store.StartRun("cal")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero run id")
	}
	if err := store.FinishRun(id, "ok", "", 5); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
}
