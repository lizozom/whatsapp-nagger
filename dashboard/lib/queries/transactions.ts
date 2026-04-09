import { getDb } from "../db";
import type { SumRow, Transaction, MonthlySpend } from "../types";

export function sumByCategory(since: string, until: string): SumRow[] {
  return getDb()
    .prepare(
      `SELECT COALESCE(NULLIF(category,''), '(uncategorized)') AS key,
              COUNT(*) AS tx_count,
              ROUND(-SUM(amount_ils), 2) AS spent_ils,
              ROUND(SUM(CASE WHEN amount_ils < 0 THEN -amount_ils ELSE 0 END), 2) AS charges_ils,
              ROUND(SUM(CASE WHEN amount_ils > 0 THEN amount_ils ELSE 0 END), 2) AS refunds_ils
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ?
       GROUP BY key ORDER BY spent_ils DESC`,
    )
    .all(since, until) as SumRow[];
}

export function sumByMerchant(
  since: string,
  until: string,
  limit = 15,
): SumRow[] {
  return getDb()
    .prepare(
      `SELECT description AS key,
              COUNT(*) AS tx_count,
              ROUND(-SUM(amount_ils), 2) AS spent_ils,
              ROUND(SUM(CASE WHEN amount_ils < 0 THEN -amount_ils ELSE 0 END), 2) AS charges_ils,
              ROUND(SUM(CASE WHEN amount_ils > 0 THEN amount_ils ELSE 0 END), 2) AS refunds_ils
       FROM transactions
       WHERE posted_at >= ? AND posted_at <= ?
       GROUP BY description ORDER BY spent_ils DESC
       LIMIT ?`,
    )
    .all(since, until, limit) as SumRow[];
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
