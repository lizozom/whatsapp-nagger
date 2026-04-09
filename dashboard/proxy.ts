import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

const PUBLIC_PATHS = ["/login", "/api/auth", "/healthz", "/health", "/ingest"];

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;

  // Allow public paths.
  if (PUBLIC_PATHS.some((p) => pathname.startsWith(p))) {
    return NextResponse.next();
  }

  // Check for auth cookie (presence check — JWT validated server-side in pages).
  const token = request.cookies.get("nagger_token");
  if (!token?.value) {
    const loginUrl = new URL("/login", request.url);
    loginUrl.searchParams.set("redirect", pathname);
    return NextResponse.redirect(loginUrl);
  }

  return NextResponse.next();
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
