import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

export interface Config {
  ingestUrl: string;
  ingestSecret: string;
  backfillDays: number;
}

export interface ProviderCredentials {
  username: string;
  password: string;
}

/**
 * Load top-level scraper config from environment variables.
 * Throws if required values are missing.
 */
export function loadConfig(): Config {
  const ingestUrl = process.env.INGEST_URL;
  const ingestSecret = process.env.INGEST_SECRET;

  if (!ingestUrl) {
    throw new Error("INGEST_URL is required");
  }
  if (!ingestSecret) {
    throw new Error("INGEST_SECRET is required");
  }

  const backfillDays = Number(process.env.BACKFILL_DAYS ?? "35");
  if (!Number.isFinite(backfillDays) || backfillDays <= 0) {
    throw new Error(`BACKFILL_DAYS must be a positive number, got ${process.env.BACKFILL_DAYS}`);
  }

  return { ingestUrl, ingestSecret, backfillDays };
}

/**
 * Look up a password from the macOS Keychain.
 * `service` is the Keychain "where" (-s), `account` is the "account" (-a).
 * Returns undefined if the entry doesn't exist.
 */
async function readKeychain(service: string, account: string): Promise<string | undefined> {
  try {
    const { stdout } = await execFileAsync("security", [
      "find-generic-password",
      "-s",
      service,
      "-a",
      account,
      "-w",
    ]);
    return stdout.trim();
  } catch (err: unknown) {
    // `security` exits with code 44 when the item is not found.
    if (err && typeof err === "object" && "code" in err) {
      return undefined;
    }
    throw err;
  }
}

/**
 * Load credentials for a provider (e.g. "cal", "max").
 *
 * Order of precedence:
 *   1. Env vars: <PROVIDER>_USERNAME / <PROVIDER>_PASSWORD (uppercase)
 *   2. macOS Keychain: service "nagger-<provider>", accounts "username" / "password"
 *
 * This lets you develop on a non-Mac box with env vars while keeping the
 * production local runner on macOS using the Keychain.
 */
export async function loadProviderCredentials(provider: string): Promise<ProviderCredentials> {
  const upper = provider.toUpperCase();
  const envUser = process.env[`${upper}_USERNAME`];
  const envPass = process.env[`${upper}_PASSWORD`];

  if (envUser && envPass) {
    return { username: envUser, password: envPass };
  }

  const service = `nagger-${provider}`;
  const [username, password] = await Promise.all([
    readKeychain(service, "username"),
    readKeychain(service, "password"),
  ]);

  if (!username || !password) {
    throw new Error(
      `Missing credentials for provider "${provider}". ` +
        `Set ${upper}_USERNAME / ${upper}_PASSWORD env vars, or add Keychain entries:\n` +
        `  security add-generic-password -s "${service}" -a "username" -w\n` +
        `  security add-generic-password -s "${service}" -a "password" -w`,
    );
  }

  return { username, password };
}
