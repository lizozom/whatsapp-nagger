import { NextResponse } from "next/server";
import { setTokenCookie } from "@/lib/auth";

const GO_API = process.env.GO_API_URL || "http://localhost:8080";

export async function POST(request: Request) {
  let body;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid json" }, { status: 400 });
  }

  try {
    const res = await fetch(`${GO_API}/api/auth/verify`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    if (!res.ok) {
      const text = await res.text();
      return new NextResponse(text, { status: res.status });
    }

    const data = await res.json();
    const cookie = setTokenCookie(data.token);
    const response = NextResponse.json({ ok: true, name: data.name });
    response.cookies.set(cookie);
    return response;
  } catch (err) {
    return NextResponse.json(
      { error: "Auth service unavailable. Is the Go bot running?" },
      { status: 503 },
    );
  }
}
