import { NextResponse } from "next/server";
import { getUser } from "@/lib/auth";

export async function GET() {
  const user = await getUser();
  if (!user) {
    return NextResponse.json({ authenticated: false }, { status: 401 });
  }
  return NextResponse.json({ authenticated: true, user });
}
