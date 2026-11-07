import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Eye, EyeSlash } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ApiClientError } from "@/api/client";
import { useAuth } from "@/auth/auth-context";
import { safeNextPath } from "@/auth/safe-next";

// Generic for every failure cause (web auth UI brief, Part A3): the
// backend deliberately returns the identical invalid_credentials
// response for an unknown email, a wrong password and an unverified
// account alike, so this UI must not invent a distinction it was never
// given -- one message, always.
const GENERIC_LOGIN_ERROR = "Email or password is incorrect, or the email is not verified yet";

export function Login() {
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retryAfter, setRetryAfter] = useState<number | null>(null);

  // Visible countdown from the Retry-After header on a 429 -- the form
  // stays disabled until it reaches zero.
  useEffect(() => {
    if (retryAfter === null || retryAfter <= 0) return;
    const id = setTimeout(() => setRetryAfter((s) => (s === null ? null : s - 1)), 1000);
    return () => clearTimeout(id);
  }, [retryAfter]);

  const rateLimited = retryAfter !== null && retryAfter > 0;

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
    <div className="space-y-5">
      <div>
        <h1 className="text-lg font-semibold">Sign in</h1>
        <p className="mt-1 text-[13px] text-fg-muted">Welcome back.</p>
      </div>

      <form onSubmit={onSubmit} className="space-y-4">
        <Field label="Email" id="login-email">
          <Input
            id="login-email"
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={submitting || rateLimited}
          />
        </Field>

        <Field label="Password" id="login-password">
          <div className="relative">
            <Input
              id="login-password"
              type={showPassword ? "text" : "password"}
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={submitting || rateLimited}
              className="pr-10"
            />
            <button
              type="button"
              onClick={() => setShowPassword((v) => !v)}
              aria-label={showPassword ? "Hide password" : "Show password"}
              className="focus-ring absolute inset-y-0 right-0 grid w-10 place-items-center text-fg-subtle transition-colors hover:text-fg"
            >
              {showPassword ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
            </button>
          </div>
        </Field>

        {error && (
          <p className="text-[12px] text-prov-u" role="alert">
            {error}
          </p>
        )}

        {rateLimited && (
          <p className="text-[12px] text-prov-u" role="alert">
            Too many attempts. Try again in {retryAfter}s.
          </p>
        )}

        {/* Forgot-password link goes here once backend Part 2 (reset-password) ships. */}

        <Button type="submit" className="w-full" disabled={submitting || rateLimited}>
          {submitting ? "Signing in…" : "Sign in"}
        </Button>
      </form>

      <p className="text-center text-[13px] text-fg-muted">
        No account?{" "}
        <Link to="/register" className="font-medium text-fg hover:underline">
          Create one
        </Link>
      </p>
    </div>
  );
}
