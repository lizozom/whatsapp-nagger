import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { homedir } from "node:os";

/**
 * Per-provider state, persisted as JSON under ~/.nagger-scraper/.
 * Used to compute delta run windows and to surface last-run status.
 */
export interface ProviderState {
  lastSuccessAt?: string; // ISO datetime of most recent successful run
  lastWindowStart?: string; // ISO date: start of the last fetch window we used
  lastTxCount?: number;
  lastError?: string;
}

const STATE_DIR = join(homedir(), ".nagger-scraper");

function stateFile(provider: string): string {
  return join(STATE_DIR, `${provider}.json`);
}

export async function readState(provider: string): Promise<ProviderState> {
  const file = stateFile(provider);
  try {
    const raw = await readFile(file, "utf8");
    return JSON.parse(raw) as ProviderState;
  } catch (err: unknown) {
    if (err && typeof err === "object" && "code" in err && (err as { code: string }).code === "ENOENT") {
      return {};
    }
    throw err;
  }
}

export async function writeState(provider: string, state: ProviderState): Promise<void> {
  const file = stateFile(provider);
  await mkdir(dirname(file), { recursive: true });
  await writeFile(file, JSON.stringify(state, null, 2) + "\n", "utf8");
}

/**
 * Compute the start date for the next scrape window.
 *
 * - First run: today minus `backfillDays`.
 * - Subsequent runs: lastWindowStart minus 3 days of overlap, so we catch
 *   pending→posted transitions and any late-arriving transactions without
 *   relying on the provider being perfectly ordered.
 *
 * Returns a Date at UTC midnight.
 */
export function computeWindowStart(state: ProviderState, backfillDays: number): Date {
  const now = new Date();
  const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));

  if (!state.lastWindowStart) {
    start.setUTCDate(start.getUTCDate() - backfillDays);
    return start;
  }

  const prev = new Date(state.lastWindowStart);
  if (Number.isNaN(prev.getTime())) {
    start.setUTCDate(start.getUTCDate() - backfillDays);
    return start;
  }

  // Overlap window: re-fetch last 3 days so pending charges can flip to posted.
  prev.setUTCDate(prev.getUTCDate() - 3);
  return prev;
}
