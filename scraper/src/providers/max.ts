import { createScraper, CompanyTypes } from "israeli-bank-scrapers";
import type {
  Transaction as LibTransaction,
  TransactionsAccount,
} from "israeli-bank-scrapers/lib/transactions.js";
import { TransactionStatuses } from "israeli-bank-scrapers/lib/transactions.js";
import type { ProviderCredentials } from "../config.ts";
import type { NormalizedTransaction } from "../post.ts";

export interface ProviderResult {
  transactions: NormalizedTransaction[];
}

/**
 * Scrape Max (מקס, ex-Leumi Card) via israeli-bank-scrapers and return
 * normalized transactions ready to post to the ingest endpoint.
 *
 * Optional MAX_CARD_LAST4 env var filters to specific card(s), comma-separated.
 */
export async function scrapeMax(
  credentials: ProviderCredentials,
  startDate: Date,
): Promise<ProviderResult> {
  const scraper = createScraper({
    companyId: CompanyTypes.max,
    startDate,
    showBrowser: process.env.SHOW_BROWSER === "1",
    combineInstallments: false,
    futureMonthsToScrape: 2,
    verbose: false,
    timeout: 120_000,
    defaultTimeout: 120_000,
  });

  const result = await scraper.scrape(credentials);
  if (!result.success) {
    throw new Error(
      `Max scrape failed: ${result.errorType ?? "unknown"} — ${result.errorMessage ?? "no message"}`,
    );
  }

  const accounts: TransactionsAccount[] = result.accounts ?? [];

  for (const account of accounts) {
    const last4 = lastFour(account.accountNumber);
    console.error(
      `[max] account ${account.accountNumber} (last4=${last4}): ${account.txns.length} tx`,
    );
  }

  const filterRaw = process.env.MAX_CARD_LAST4?.trim();
  const filter = filterRaw
    ? new Set(filterRaw.split(",").map((s) => s.trim()).filter(Boolean))
    : undefined;

  const kept = filter
    ? accounts.filter((a) => filter.has(lastFour(a.accountNumber)))
    : accounts;

  if (filter && kept.length === 0) {
    const seen = accounts.map((a) => lastFour(a.accountNumber)).join(", ");
    throw new Error(
      `MAX_CARD_LAST4=${filterRaw} matched no accounts. Available last4: [${seen}]`,
    );
  }
  if (filter) {
    console.error(
      `[max] filter MAX_CARD_LAST4=${filterRaw} kept ${kept.length}/${accounts.length} accounts`,
    );
  }

  const normalized: NormalizedTransaction[] = [];
  let skippedPending = 0;
  for (const account of kept) {
    const cardLast4 = lastFour(account.accountNumber);
    for (const tx of account.txns) {
      // Skip pending: description/amount can change on settlement, which
      // changes the dedup hash and produces duplicate rows.
      if (tx.status === TransactionStatuses.Pending) {
        skippedPending++;
        continue;
      }
      normalized.push(normalizeTx(cardLast4, tx));
    }
  }
  if (skippedPending > 0) {
    console.error(`[max] skipped ${skippedPending} pending tx (will ingest once posted)`);
  }

  return { transactions: normalized };
}

function lastFour(accountNumber: string | undefined): string {
  if (!accountNumber) return "";
  const digits = accountNumber.replace(/\D/g, "");
  return digits.slice(-4);
}

function normalizeTx(cardLast4: string, tx: LibTransaction): NormalizedTransaction {
  const amountILS = Number(tx.chargedAmount);
  const postedAt = isoDate(tx.date);

  return {
    card_last4: cardLast4,
    posted_at: postedAt,
    amount_ils: amountILS,
    description: (tx.description ?? "").trim(),
    memo: (tx.memo ?? "").trim() || undefined,
    category: tx.category,
    status: "posted",
  };
}

function isoDate(input: string): string {
  const d = new Date(input);
  if (Number.isNaN(d.getTime())) return input;
  return d.toISOString().slice(0, 10);
}
