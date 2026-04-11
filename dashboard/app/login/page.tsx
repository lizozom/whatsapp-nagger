import { Suspense } from "react";
import { redirect } from "next/navigation";
import { getUser } from "@/lib/auth";
import { LoginForm } from "./login-form";

interface Props {
  searchParams: Promise<{ redirect?: string; phone?: string; code?: string }>;
}

export default async function LoginPage({ searchParams }: Props) {
  const params = await searchParams;

  // If already authenticated, skip straight to dashboard — even if magic link
  // params are in the URL. Otherwise clicking the magic link twice would fail
  // (the OTP was consumed on the first click).
  const user = await getUser();
  if (user) {
    redirect(params.redirect || "/finances");
  }

  return (
    <Suspense>
      <LoginForm />
    </Suspense>
  );
}
