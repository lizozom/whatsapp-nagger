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
 * Scrape Visa Cal (כאל) via israeli-bank-scrapers and return normalized
 * transactions ready to post to the ingest endpoint.
 */
export async function scrapeCal(
  credentials: ProviderCredentials,
  startDate: Date,
): Promise<ProviderResult> {
  const scraper = createScraper({
    companyId: CompanyTypes.visaCal,
    startDate,
    // Toggle via SHOW_BROWSER=1 in .env to watch the login happen live.
    showBrowser: process.env.SHOW_BROWSER === "1",
    combineInstallments: false,
    // Cal sometimes hides small pending charges unless we ask for future months.
    futureMonthsToScrape: 2,
    verbose: true,
    // Cal's site is slow; the default 30s is too tight for cold loads.
    timeout: 120_000,
    defaultTimeout: 120_000,
  });

  const result = await scraper.scrape(credentials);
  if (!result.success) {
    throw new Error(
      `Cal scrape failed: ${result.errorType ?? "unknown"} — ${result.errorMessage ?? "no message"}`,
    );
  }

  const accounts: TransactionsAccount[] = result.accounts ?? [];

  // Log a per-account summary so the user can identify cards when picking a filter.
  for (const account of accounts) {
    const last4 = lastFour(account.accountNumber);
    console.error(
      `[cal] account ${account.accountNumber} (last4=${last4}): ${account.txns.length} tx`,
    );
  }

  // Optional filter: if CAL_CARD_LAST4 is set, keep only matching accounts.
  // Supports comma-separated list for multi-card users who want a subset.
  const filterRaw = process.env.CAL_CARD_LAST4?.trim();
  const filter = filterRaw
    ? new Set(filterRaw.split(",").map((s) => s.trim()).filter(Boolean))
    : undefined;

  const kept = filter
    ? accounts.filter((a) => filter.has(lastFour(a.accountNumber)))
    : accounts;

  if (filter && kept.length === 0) {
    const seen = accounts.map((a) => lastFour(a.accountNumber)).join(", ");
    throw new Error(
      `CAL_CARD_LAST4=${filterRaw} matched no accounts. Available last4: [${seen}]`,
    );
  }
  if (filter) {
    console.error(
      `[cal] filter CAL_CARD_LAST4=${filterRaw} kept ${kept.length}/${accounts.length} accounts`,
    );
  }

  const normalized: NormalizedTransaction[] = [];
  let skippedPending = 0;
  for (const account of kept) {
    const cardLast4 = lastFour(account.accountNumber);
    for (const tx of account.txns) {
      // Skip pending: Cal rewrites the description on settlement (e.g. strips
      // "<City> <CountryCode>" off foreign charges), which changes the dedup
      // hash and produces a duplicate row when the charge later posts.
      if (tx.status === TransactionStatuses.Pending) {
        skippedPending++;
        continue;
      }
      normalized.push(normalizeTx("cal", cardLast4, tx));
    }
  }
  if (skippedPending > 0) {
    console.error(`[cal] skipped ${skippedPending} pending tx (will ingest once posted)`);
  }

  return { transactions: normalized };
}

function lastFour(accountNumber: string | undefined): string {
  if (!accountNumber) return "";
  const digits = accountNumber.replace(/\D/g, "");
  return digits.slice(-4);
}

function normalizeTx(
  _provider: string,
  cardLast4: string,
  tx: LibTransaction,
): NormalizedTransaction {
  // israeli-bank-scrapers returns `chargedAmount` as a signed number:
  // negative for debits, positive for credits. That matches our Go schema.
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

/**
 * The library returns full ISO datetimes (with time component) in `date`.
 * For stable dedupe hashing we want a YYYY-MM-DD date in UTC.
 */
function isoDate(input: string): string {
  const d = new Date(input);
  if (Number.isNaN(d.getTime())) return input;
  return d.toISOString().slice(0, 10);
}
