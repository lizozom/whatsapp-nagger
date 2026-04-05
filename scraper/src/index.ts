import { parseArgs } from "node:util";
import { loadConfig, loadProviderCredentials } from "./config.ts";
import { postPayload, type IngestPayload } from "./post.ts";
import { scrapeCal } from "./providers/cal.ts";
import { scrapeMax } from "./providers/max.ts";
import { computeWindowStart, readState, writeState } from "./state.ts";

// Providers temporarily disabled. Remove from this set to re-enable.
const DISABLED_PROVIDERS = new Set<string>();

// Load .env if present. Done inline to avoid an extra dependency — the scraper
// package should stay tiny.
await loadDotEnv();

interface Args {
  provider: string;
  dryRun: boolean;
}

function parseCliArgs(): Args {
  const { values } = parseArgs({
    options: {
      provider: { type: "string", default: "max" },
      "dry-run": { type: "boolean", default: false },
    },
    strict: true,
  });
  return {
    provider: String(values.provider ?? "max"),
    dryRun: Boolean(values["dry-run"]),
  };
}

async function runProvider(
  name: string,
  credentials: { username: string; password: string },
  backfillDays: number,
) {
  const state = await readState(name);
  const startDate = computeWindowStart(state, backfillDays);
  console.error(`[${name}] fetching from ${startDate.toISOString().slice(0, 10)}`);

  switch (name) {
    case "cal":
      return { ...(await scrapeCal(credentials, startDate)), startDate };
    case "max":
      return { ...(await scrapeMax(credentials, startDate)), startDate };
    default:
      throw new Error(`unknown provider: ${name}`);
  }
}

async function main() {
  const args = parseCliArgs();

  if (DISABLED_PROVIDERS.has(args.provider)) {
    throw new Error(
      `provider "${args.provider}" is currently disabled. ` +
        `Remove it from DISABLED_PROVIDERS in src/index.ts to re-enable.`,
    );
  }

  const config = loadConfig();

  const credentials = await loadProviderCredentials(args.provider);
  const { transactions, startDate } = await runProvider(
    args.provider,
    credentials,
    config.backfillDays,
  );

  console.error(`[${args.provider}] scraped ${transactions.length} transactions`);

  if (args.dryRun) {
    console.log(JSON.stringify(transactions, null, 2));
    return;
  }

  const payload: IngestPayload = {
    provider: args.provider,
    fetched_at: new Date().toISOString(),
    transactions,
  };

  const resp = await postPayload(config.ingestUrl, config.ingestSecret, payload);
  console.error(
    `[${args.provider}] ingest ok: inserted=${resp.inserted} skipped=${resp.skipped} received=${resp.received} run_id=${resp.run_id}`,
  );

  await writeState(args.provider, {
    lastSuccessAt: new Date().toISOString(),
    lastWindowStart: startDate.toISOString().slice(0, 10),
    lastTxCount: transactions.length,
  });
}

/**
 * Minimal .env loader — no dependency on dotenv. Parses KEY=value lines,
 * ignores comments and blank lines, does not overwrite existing process.env.
 */
async function loadDotEnv(): Promise<void> {
  try {
    const { readFile } = await import("node:fs/promises");
    const raw = await readFile(".env", "utf8");
    for (const line of raw.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const eq = trimmed.indexOf("=");
      if (eq < 0) continue;
      const key = trimmed.slice(0, eq).trim();
      let value = trimmed.slice(eq + 1).trim();
      if (
        (value.startsWith('"') && value.endsWith('"')) ||
        (value.startsWith("'") && value.endsWith("'"))
      ) {
        value = value.slice(1, -1);
      }
      if (!(key in process.env)) {
        process.env[key] = value;
      }
    }
  } catch (err: unknown) {
    if (err && typeof err === "object" && "code" in err && (err as { code: string }).code === "ENOENT") {
      return;
    }
    throw err;
  }
}

main().catch((err) => {
  console.error("scraper failed:", err instanceof Error ? err.stack ?? err.message : err);
  const state = process.argv.find((a) => a.startsWith("--provider="))?.split("=")[1] ?? "cal";
  void writeState(state, {
    lastError: err instanceof Error ? err.message : String(err),
  });
  process.exit(1);
});
