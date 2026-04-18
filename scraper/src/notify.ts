import { createHmac } from "node:crypto";

/**
 * Send an HMAC-signed alert to the bot's /notify endpoint.
 * Called from run.sh when a provider fails.
 *
 * Usage: npx tsx src/notify.ts "provider failed: reason"
 */

// Minimal .env loader (same as index.ts — no extra dependency).
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

await loadDotEnv();

const message = process.argv[2];
if (!message) {
  console.error("usage: npx tsx src/notify.ts <message>");
  process.exit(1);
}

const ingestUrl = process.env.INGEST_URL;
const ingestSecret = process.env.INGEST_SECRET;

if (!ingestUrl || !ingestSecret) {
  console.error("INGEST_URL and INGEST_SECRET must be set");
  process.exit(1);
}

// Derive /notify URL from the ingest URL (same host).
const notifyUrl = ingestUrl.replace(/\/ingest\/transactions$/, "/notify");

const body = JSON.stringify({ message });
const signature = createHmac("sha256", ingestSecret).update(body).digest("hex");

const res = await fetch(notifyUrl, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Signature": signature,
  },
  body,
});

if (!res.ok) {
  const text = await res.text();
  console.error(`notify failed: ${res.status} ${text}`);
  process.exit(1);
}

console.error("notify sent");
