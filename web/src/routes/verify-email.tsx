import { useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { CircleNotch } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { resendVerification, verifyEmail } from "@/api/auth";

type Phase = "need-token" | "verifying" | "success" | "failure";

export function VerifyEmail() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const tokenFromUrl = params.get("token");

  const [manualToken, setManualToken] = useState("");
  const [phase, setPhase] = useState<Phase>(tokenFromUrl ? "verifying" : "need-token");
  const [resendEmail, setResendEmail] = useState("");
  const [resendSubmitting, setResendSubmitting] = useState(false);
  const [resendMessage, setResendMessage] = useState<string | null>(null);

  // Guards against React StrictMode's double effect-fire in
  // development: this ref is set once and persists across that
  // synthetic double invocation (the component is not actually
  // unmounted between the two calls), so the single-use token is
  // posted exactly once, never twice.
  const triedRef = useRef(false);

  useEffect(() => {
    if (!tokenFromUrl) return;
    if (triedRef.current) return;
    triedRef.current = true;
    verifyEmail(tokenFromUrl)
      .then(() => setPhase("success"))
      .catch(() => setPhase("failure"));
  }, [tokenFromUrl]);

  async function submitManualToken(e: FormEvent) {
    e.preventDefault();
    setPhase("verifying");
    try {
      await verifyEmail(manualToken.trim());
      setPhase("success");
    } catch {
      setPhase("failure");
    }
  }

  // Same neutral confirmation regardless of outcome -- see register.tsx's
  // own onResend for why (ADR-015's generic-response rule).
  async function onResend(e: FormEvent) {
    e.preventDefault();
    setResendSubmitting(true);
    try {
      await resendVerification(resendEmail);
    } catch {
      // Intentionally ignored.
    } finally {
      setResendSubmitting(false);
      setResendMessage("If that email can be verified, a new link is on its way.");
    }
  }

  if (phase === "need-token") {
    return (
      <div className="space-y-4">
        <h1 className="text-lg font-semibold">Verify your email</h1>
        <p className="text-[13px] text-fg-muted">
          Paste the verification token from your email.
        </p>
        <form onSubmit={submitManualToken} className="space-y-3">
          <Field label="Verification token" id="verify-token-manual">
            <Input
              id="verify-token-manual"
              required
              value={manualToken}
              onChange={(e) => setManualToken(e.target.value)}
            />
          </Field>
          <Button type="submit" className="w-full">
            Verify
          </Button>
        </form>
      </div>
    );
  }

  if (phase === "verifying") {
    return (
      <div className="space-y-3 py-4 text-center">
        <CircleNotch className="mx-auto size-5 animate-spin text-fg-subtle" aria-hidden />
        <p className="text-[13px] text-fg-muted">Verifying…</p>
      </div>
    );
  }

  if (phase === "success") {
    return (
      <div className="space-y-4 text-center">
        <h1 className="text-lg font-semibold">Email verified</h1>
        <p className="text-[13px] text-fg-muted">Your account is ready.</p>
        <Button className="w-full" onClick={() => navigate("/login")}>
          Continue to sign in
        </Button>
      </div>
    );
  }

  // failure
  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">Link invalid</h1>
      <p className="text-[13px] text-prov-u" role="alert">
        This link is invalid, was already used, or has expired.
      </p>
      <form onSubmit={onResend} className="space-y-3">
        <Field label="Email" id="verify-resend-email">
          <Input
            id="verify-resend-email"
            type="email"
            required
            value={resendEmail}
            onChange={(e) => setResendEmail(e.target.value)}
            disabled={resendSubmitting}
          />
        </Field>
        <Button type="submit" variant="outline" className="w-full" disabled={resendSubmitting}>
          {resendSubmitting ? "Sending…" : "Resend verification email"}
        </Button>
      </form>
      {resendMessage && <p className="text-[12px] text-fg-muted">{resendMessage}</p>}
    </div>
  );
}
