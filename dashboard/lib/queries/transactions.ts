import { getDb } from "../db";
import type { SumRow, Transaction, MonthlySpend } from "../types";
import { normalizeCategory } from "../categories";

interface RawRow {
  description: string;
  category: string;
  amount_ils: number;
}

/**
 * Groups transactions by NORMALIZED category. Fetches raw rows and
 * aggregates in JS so we can apply merchant overrides (which SQL can't do
 * without a complex CASE expression).
 */
export function sumByCategory(since: string, until: string): SumRow[] {
  const fullRows = getDb()
    .prepare(
      `SELECT description, COALESCE(category, '') AS category, amount_ils
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ?`,
    )
    .all(since, until) as RawRow[];

  const byKey: Record<string, SumRow> = {};
  for (const r of fullRows) {
    const key = normalizeCategory(r.description, r.category);
    if (!byKey[key]) {
      byKey[key] = {
        key,
        tx_count: 0,
        spent_ils: 0,
        charges_ils: 0,
        refunds_ils: 0,
      };
    }
    const bucket = byKey[key];
    bucket.tx_count++;
    bucket.spent_ils += -r.amount_ils;
    if (r.amount_ils < 0) bucket.charges_ils += -r.amount_ils;
    if (r.amount_ils > 0) bucket.refunds_ils += r.amount_ils;
  }

  return Object.values(byKey)
    .map((r) => ({
      ...r,
      spent_ils: Math.round(r.spent_ils * 100) / 100,
      charges_ils: Math.round(r.charges_ils * 100) / 100,
      refunds_ils: Math.round(r.refunds_ils * 100) / 100,
    }))
    .sort((a, b) => b.spent_ils - a.spent_ils);
}

export function sumByMerchant(
  since: string,
  until: string,
  limit = 15,
): SumRow[] {
  // Fetch raw rows so we can attach the normalized category per merchant.
  const fullRows = getDb()
    .prepare(
      `SELECT description, COALESCE(category, '') AS category, amount_ils
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ?`,
    )
    .all(since, until) as Array<{
    description: string;
    category: string;
    amount_ils: number;
  }>;

  const byMerchant: Record<string, SumRow> = {};
  for (const r of fullRows) {
    if (!byMerchant[r.description]) {
      byMerchant[r.description] = {
        key: r.description,
        tx_count: 0,
        spent_ils: 0,
        charges_ils: 0,
        refunds_ils: 0,
        category: normalizeCategory(r.description, r.category),
      };
    }
    const b = byMerchant[r.description];
    b.tx_count++;
    b.spent_ils += -r.amount_ils;
    if (r.amount_ils < 0) b.charges_ils += -r.amount_ils;
    if (r.amount_ils > 0) b.refunds_ils += r.amount_ils;
  }

  return Object.values(byMerchant)
    .map((r) => ({
      ...r,
      spent_ils: Math.round(r.spent_ils * 100) / 100,
      charges_ils: Math.round(r.charges_ils * 100) / 100,
      refunds_ils: Math.round(r.refunds_ils * 100) / 100,
    }))
    .sort((a, b) => b.spent_ils - a.spent_ils)
    .slice(0, limit);
}

export function monthlySpend(months = 6): MonthlySpend[] {
  return getDb()
    .prepare(
      `SELECT substr(posted_at,1,7) AS month,
              ROUND(-SUM(amount_ils), 2) AS spent,
              COUNT(*) AS tx_count
       FROM transactions
       GROUP BY month
       ORDER BY month DESC
       LIMIT ?`,
    )
    .all(months) as MonthlySpend[];
}

export function totals(since: string, until: string) {
  return getDb()
    .prepare(
      `SELECT COUNT(*) AS tx_count,
              ROUND(-COALESCE(SUM(amount_ils), 0), 2) AS spent_ils,
              ROUND(COALESCE(SUM(CASE WHEN amount_ils < 0 THEN -amount_ils ELSE 0 END), 0), 2) AS charges_ils,
              ROUND(COALESCE(SUM(CASE WHEN amount_ils > 0 THEN amount_ils ELSE 0 END), 0), 2) AS refunds_ils
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ?`,
    )
    .get(since, until) as {
    tx_count: number;
    spent_ils: number;
    charges_ils: number;
    refunds_ils: number;
  };
}

export function topTransactions(
  since: string,
  until: string,
  limit = 20,
): Transaction[] {
  return getDb()
    .prepare(
      `SELECT id, provider, card_last4, posted_at, amount_ils, description, memo, category, status
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ? AND amount_ils < 0
       ORDER BY amount_ils ASC
       LIMIT ?`,
    )
    .all(since, until, limit) as Transaction[];
}

/** All transactions in a cycle. Used by the paginated table on the dashboard. */
export function allTransactionsInCycle(
  since: string,
  until: string,
): Transaction[] {
  return getDb()
    .prepare(
      `SELECT id, provider, card_last4, posted_at, amount_ils, description, memo, category, status
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ?
       ORDER BY posted_at DESC, id ASC`,
    )
    .all(since, until) as Transaction[];
}
