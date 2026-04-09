import { NextResponse } from "next/server";

const GO_API = process.env.GO_API_URL || "http://localhost:8080";

export async function POST(request: Request) {
  let body;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid json" }, { status: 400 });
  }

  try {
    const res = await fetch(`${GO_API}/api/auth/otp`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
    });
    const text = await res.text();
    return new NextResponse(text, {
      status: res.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch {
    return NextResponse.json(
      { error: "Auth service unavailable. Is the Go bot running?" },
      { status: 503 },
    );
  }
}
