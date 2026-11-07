import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Check } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { register, resendVerification } from "@/api/auth";
import { ApiClientError } from "@/api/client";
import { cn } from "@/lib/cn";

const MIN_LENGTH = 12;
const MAX_LENGTH = 128;
const RESEND_COOLDOWN_S = 60;

function Requirement({ met, children }: { met: boolean; children: ReactNode }) {
  return (
    <li className={cn("flex items-center gap-1.5", met ? "text-fg" : "text-fg-subtle")}>
      <Check weight={met ? "bold" : "regular"} className="size-3" />
      {children}
    </li>
  );
}

// rateLimitMessage composes a readable sentence from the backend's
// Retry-After signal -- RateLimitedError.Error()'s own raw string
// ("service: rate limited, retry after 1h0m0s") is an internal detail,
// not something to show verbatim.
function rateLimitMessage(err: ApiClientError): string {
  if (err.retryAfterSeconds === undefined) return "Too many attempts. Try again later.";
  const minutes = Math.ceil(err.retryAfterSeconds / 60);
  return `Too many attempts. Try again in about ${minutes} minute${minutes === 1 ? "" : "s"}.`;
}

type Phase = "form" | "sent";

export function Register() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>("form");
  const [resendCooldown, setResendCooldown] = useState(0);
  const [resendMessage, setResendMessage] = useState<string | null>(null);

  useEffect(() => {
    if (resendCooldown <= 0) return;
    const id = setTimeout(() => setResendCooldown((s) => s - 1), 1000);
    return () => clearTimeout(id);
  }, [resendCooldown]);

  const lengthOk = password.length >= MIN_LENGTH && password.length <= MAX_LENGTH;
  const matches = password.length > 0 && password === confirm;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError("Passwords do not match.");
      return;
    }
    setSubmitting(true);
    try {
      await register(email, password);
      setPhase("sent");
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.code === "rate_limited" ? rateLimitMessage(err) : err.message);
      } else {
        setError("Something went wrong. Try again.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  // Same neutral confirmation regardless of outcome -- the backend
  // answers identically for every email (ADR-015), so a resend failure
  // (rate limit, network) must not change what this says either, or
  // the difference itself would disclose whether the address exists.
  async function onResend() {
    setResendCooldown(RESEND_COOLDOWN_S);
    try {
      await resendVerification(email);
    } catch {
      // Intentionally ignored; see the comment above.
    }
    setResendMessage("If that email can be verified, a new link is on its way.");
  }

  if (phase === "sent") {
    return (
      <div className="space-y-4">
        <div>
          <h1 className="text-lg font-semibold">Check your email</h1>
          <p className="mt-1 text-[13px] text-fg-muted">
            If <span className="text-fg">{email}</span> can be registered, we've sent a
            verification link. Open it to finish creating your account.
          </p>
        </div>
        <Button
          variant="outline"
          className="w-full"
          onClick={onResend}
          disabled={resendCooldown > 0}
        >
          {resendCooldown > 0 ? `Resend in ${resendCooldown}s` : "Resend email"}
        </Button>
        {resendMessage && <p className="text-[12px] text-fg-muted">{resendMessage}</p>}
        <p className="text-center text-[13px] text-fg-muted">
          <Link to="/login" className="font-medium text-fg hover:underline">
            Back to sign in
          </Link>
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-lg font-semibold">Create account</h1>
        <p className="mt-1 text-[13px] text-fg-muted">Start watching the market with ARGUS.</p>
      </div>

      <form onSubmit={onSubmit} className="space-y-4">
        <Field label="Email" id="register-email">
          <Input
            id="register-email"
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={submitting}
          />
        </Field>

        <Field
          label="Password"
          id="register-password"
          hint={
            <ul className="space-y-0.5">
              <Requirement met={password.length >= MIN_LENGTH}>
                At least {MIN_LENGTH} characters
              </Requirement>
              <Requirement met={password.length > 0 && password.length <= MAX_LENGTH}>
                At most {MAX_LENGTH} characters
              </Requirement>
            </ul>
          }
        >
          <Input
            id="register-password"
            type="password"
            autoComplete="new-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={submitting}
            invalid={password.length > 0 && !lengthOk}
          />
        </Field>

        <Field label="Confirm password" id="register-confirm">
          <Input
            id="register-confirm"
            type="password"
            autoComplete="new-password"
            required
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            disabled={submitting}
            invalid={confirm.length > 0 && !matches}
          />
        </Field>

        {error && (
          <p className="text-[12px] text-prov-u" role="alert">
            {error}
          </p>
        )}

        <Button type="submit" className="w-full" disabled={submitting}>
          {submitting ? "Creating account…" : "Create account"}
        </Button>
      </form>

      <p className="text-center text-[13px] text-fg-muted">
        Already have an account?{" "}
        <Link to="/login" className="font-medium text-fg hover:underline">
          Sign in
        </Link>
      </p>
    </div>
  );
}
