import { NextRequest, NextResponse } from "next/server";
import { setTokenCookie } from "@/lib/auth";

const GO_API = process.env.GO_API_URL || "http://localhost:8080";

export async function POST(request: NextRequest) {
  const body = await request.json();

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

  // Set the JWT as an httpOnly cookie.
  const cookie = setTokenCookie(data.token);
  const response = NextResponse.json({ ok: true, name: data.name });
  response.cookies.set(cookie);
  return response;
}
