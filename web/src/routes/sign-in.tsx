import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { AuthShell } from "@/components/light/auth-shell";
import { AppleIcon, GoogleIcon } from "@/components/light/brand-icons";
import { TextInput } from "@/components/light/text-input";
import { PasswordInput } from "@/components/light/password-input";
import { PrimaryButton } from "@/components/light/primary-button";
import { SocialButton } from "@/components/light/social-button";
import { OrDivider } from "@/components/light/or-divider";
import { ApiClientError } from "@/api/client";
import { useAuth } from "@/auth/auth-context";
import { safeNextPath } from "@/auth/safe-next";

// Google/Apple login is not decided (ADR-015 keeps it out of scope) --
// the buttons render in the reference's own style but disabled, behind
// this one constant, per the brief. Flip it only once a real provider
// is chosen and wired.
const SOCIAL_LOGIN_ENABLED = false;

// Password reset itself (a real /auth/forgot-password + /auth/reset-
// password backend) is not built yet -- ADR-015 reserves the shape
// and internal/httpapi's own deny-by-default list already pre-lists
// both paths as public, inert until they exist. The link is shown
// (per direct request) but points at a placeholder page that says so
// honestly, rather than a form that would submit to nothing real.
const FORGOT_PASSWORD_ENABLED = true;

const GENERIC_LOGIN_ERROR = "Email or password is incorrect, or the email is not verified yet";

export function SignIn() {
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retryAfter, setRetryAfter] = useState<number | null>(null);

  useEffect(() => {
    if (retryAfter === null || retryAfter <= 0) return;
    const id = setTimeout(() => setRetryAfter((s) => (s === null ? null : s - 1)), 1000);
    return () => clearTimeout(id);
  }, [retryAfter]);

  const rateLimited = retryAfter !== null && retryAfter > 0;
  // Continue stays dimmed (PrimaryButton's own disabled opacity) until
  // both fields actually have something in them -- not just whenever
  // the form happens to be submittable for some other reason (rate
  // limit, in-flight submit), which are handled separately below.
  const canSubmit = email.trim().length > 0 && password.length > 0;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await signIn(email, password);
      navigate(safeNextPath(params.get("next")), { replace: true });
    } catch (err) {
      if (err instanceof ApiClientError && err.code === "rate_limited") {
        setRetryAfter(Math.ceil(err.retryAfterSeconds ?? 30));
      } else {
        setError(GENERIC_LOGIN_ERROR);
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <AuthShell onBack={() => navigate(-1)}>
      <div className="space-y-5">
        <div>
          <h1 className="font-heading text-xl font-bold text-light-text">
            Log into your account
          </h1>
          <p className="mt-1 text-sm text-light-muted">
            Enter your details and continue from where you left things off
          </p>
        </div>

        <form onSubmit={onSubmit} className="space-y-3.5">
          <TextInput
            label="Email address"
            id="sign-in-email"
            type="email"
            required
            autoComplete="email"
            placeholder="e.g. you@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={submitting || rateLimited}
          />

          <PasswordInput
            label="Password"
            id="sign-in-password"
            required
            autoComplete="current-password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={submitting || rateLimited}
            /* Forgot-password link goes here once backend Part 2 (password reset) ships. */
            labelAddon={
              FORGOT_PASSWORD_ENABLED ? (
                <Link to="/forgot-password" className="text-xs font-medium text-light-primary">
                  Forgot password?
                </Link>
              ) : undefined
            }
          />

          {error && (
            <p className="text-xs text-light-error" role="alert">
              {error}
            </p>
          )}
          {rateLimited && (
            <p className="text-xs text-light-error" role="alert">
              Too many attempts. Try again in {retryAfter}s.
            </p>
          )}

          <OrDivider />

          <div className="space-y-2.5">
            <SocialButton
              icon={<GoogleIcon />}
              disabled={!SOCIAL_LOGIN_ENABLED}
              soon={!SOCIAL_LOGIN_ENABLED}
            >
              Log in with Google
            </SocialButton>
            <SocialButton
              icon={<AppleIcon />}
              disabled={!SOCIAL_LOGIN_ENABLED}
              soon={!SOCIAL_LOGIN_ENABLED}
            >
              Log in with Apple
            </SocialButton>
          </div>

          <PrimaryButton type="submit" loading={submitting} disabled={!canSubmit || rateLimited}>
            Continue
          </PrimaryButton>
        </form>

        <p className="text-center text-sm text-light-muted">
          New to ARGUS?{" "}
          <Link to="/sign-up" className="font-medium text-light-primary">
            Sign up
          </Link>
        </p>
      </div>
    </AuthShell>
  );
}
