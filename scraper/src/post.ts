import { createHmac } from "node:crypto";

export interface NormalizedTransaction {
  card_last4: string;
  posted_at: string; // ISO date (YYYY-MM-DD) or full ISO datetime
  amount_ils: number; // negative = charge
  description: string;
  memo?: string;
  category?: string;
  status?: "pending" | "posted";
}

export interface IngestPayload {
  provider: string;
  run_id?: string;
  fetched_at: string; // ISO datetime
  transactions: NormalizedTransaction[];
}

export interface IngestResponse {
  run_id: number;
  inserted: number;
  skipped: number;
  received: number;
  status: string;
}

/**
 * Hex-encoded HMAC-SHA256. Must match the Go side's ingest.ComputeSignature.
 */
export function sign(secret: string, body: string): string {
  return createHmac("sha256", secret).update(body).digest("hex");
}

/**
 * POST a normalized payload to the ingest endpoint, signed with HMAC-SHA256.
 */
export async function postPayload(
  url: string,
  secret: string,
  payload: IngestPayload,
): Promise<IngestResponse> {
  const body = JSON.stringify(payload);
  const signature = sign(secret, body);

  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Signature": signature,
    },
    body,
  });

  const text = await res.text();
  if (!res.ok) {
    throw new Error(`ingest POST failed: ${res.status} ${res.statusText}: ${text}`);
  }

  try {
    return JSON.parse(text) as IngestResponse;
  } catch {
    throw new Error(`ingest returned non-JSON 200 response: ${text}`);
  }
}
