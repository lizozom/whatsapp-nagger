import { cookies } from "next/headers";
import * as jose from "jose";

const JWT_SECRET = new TextEncoder().encode(
  process.env.JWT_SECRET || "dev-secret-do-not-use-in-production",
);
const COOKIE_NAME = "nagger_token";

export interface AuthUser {
  phone: string;
  name: string;
}

export async function getUser(): Promise<AuthUser | null> {
  const cookieStore = await cookies();
  const token = cookieStore.get(COOKIE_NAME)?.value;
  if (!token) return null;

  try {
    const { payload } = await jose.jwtVerify(token, JWT_SECRET);
    return {
      phone: payload.sub as string,
      name: payload.name as string,
    };
  } catch {
    return null;
  }
}

export function setTokenCookie(token: string) {
  // Returns cookie options — caller uses cookies().set()
  return {
    name: COOKIE_NAME,
    value: token,
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "strict" as const,
    path: "/",
    maxAge: 365 * 24 * 60 * 60, // 1 year
  };
}

export function clearTokenCookie() {
  return {
    name: COOKIE_NAME,
    value: "",
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "strict" as const,
    path: "/",
    maxAge: 0,
  };
}
