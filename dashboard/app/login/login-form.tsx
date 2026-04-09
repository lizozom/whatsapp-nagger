"use client";

import { useState, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const redirect = searchParams.get("redirect") || "/finances";

  const urlPhone = searchParams.get("phone") || "";
  const urlCode = searchParams.get("code") || "";

  const [step, setStep] = useState<"phone" | "otp">(
    urlPhone && urlCode ? "otp" : "phone",
  );
  const [phone, setPhone] = useState(urlPhone);
  const [code, setCode] = useState(urlCode);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  // Auto-verify magic link on mount.
  useEffect(() => {
    if (urlPhone && urlCode) {
      handleVerify(urlPhone, urlCode);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handleSendOTP() {
    setError("");
    setLoading(true);
    try {
      const res = await fetch("/api/auth/otp", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ phone }),
      });
      if (!res.ok) {
        const text = await res.text();
        setError(text || "Failed to send OTP");
        return;
      }
      setStep("otp");
    } catch {
      setError("Network error");
    } finally {
      setLoading(false);
    }
  }

  async function handleVerify(p?: string, c?: string) {
    const verifyPhone = p || phone;
    const verifyCode = c || code;
    setError("");
    setLoading(true);
    try {
      const res = await fetch("/api/auth/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ phone: verifyPhone, code: verifyCode }),
      });
      if (!res.ok) {
        const text = await res.text();
        setError(text || "Verification failed");
        setStep("otp");
        return;
      }
      router.push(redirect);
    } catch {
      setError("Network error");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex items-center justify-center min-h-[60vh]">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Nagger Dashboard</CardTitle>
          <p className="text-sm text-muted-foreground">
            {step === "phone"
              ? "Enter your phone number to receive a code via WhatsApp"
              : "Enter the code sent to your WhatsApp"}
          </p>
        </CardHeader>
        <CardContent>
          {error && (
            <div className="mb-4 p-2 text-sm text-red-600 bg-red-50 rounded">
              {error}
            </div>
          )}

          {step === "phone" ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                handleSendOTP();
              }}
              className="space-y-4"
            >
              <input
                type="tel"
                placeholder="972501234567"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                className="w-full px-3 py-2 border rounded-md text-sm"
                autoFocus
                required
              />
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? "Sending..." : "Send Code"}
              </Button>
            </form>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                handleVerify();
              }}
              className="space-y-4"
            >
              <input
                type="text"
                placeholder="6-digit code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="w-full px-3 py-2 border rounded-md text-sm text-center tracking-widest text-lg"
                maxLength={6}
                autoFocus
                required
              />
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? "Verifying..." : "Verify"}
              </Button>
              <button
                type="button"
                onClick={() => setStep("phone")}
                className="w-full text-sm text-muted-foreground hover:underline"
              >
                Use a different number
              </button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
