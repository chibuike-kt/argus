import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { AuthShell } from "@/components/light/auth-shell";
import { TextInput } from "@/components/light/text-input";
import { PasswordInput } from "@/components/light/password-input";
import { PrimaryButton } from "@/components/light/primary-button";
import { Checkbox } from "@/components/light/checkbox";
import { CodeInput } from "@/components/light/code-input";
import { ApiClientError } from "@/api/client";
import * as authApi from "@/api/auth";
import { useAuth } from "@/auth/auth-context";
import {
  clearSignupState,
  getSignupTicket,
  loadSignupState,
  saveSignupState,
  setSignupTicket,
} from "@/lib/signup-state";

const TOTAL_STEPS = 5;
// The resend cooldown is purely a client-side UX throttle on the
// button (architect review of Part 2, item 1): the server itself
// always answers the same generic 202 whether or not the cooldown has
// actually elapsed, so there is nothing to lose by not checking a
// server-reported value here.
const RESEND_COOLDOWN_SECONDS = 60;

const GENERIC_CODE_ERROR = "That code didn't work. Check it and try again, or request a new one.";

type Step = 1 | 2 | 3;

export function SignUp() {
  const { completeSignup } = useAuth();
  const navigate = useNavigate();

  const stored = loadSignupState();
  // Step 3 needs the in-memory-only ticket (lib/signup-state.ts's own
  // doc comment on why it never persists); a reload after a code was
  // sent but before this page load has no ticket to resume with, so it
  // goes back to step 2 to verify again, never straight to step 3.
  const initialStep: Step = stored?.codeSent ? (getSignupTicket() ? 3 : 2) : 1;

  const [step, setStep] = useState<Step>(initialStep);
  const [email, setEmail] = useState(stored?.email ?? "");
  const [acceptedTerms, setAcceptedTerms] = useState(stored?.acceptedTerms ?? false);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retryAfter, setRetryAfter] = useState<number | null>(null);
  const [resendCooldown, setResendCooldown] = useState(step >= 2 ? RESEND_COOLDOWN_SECONDS : 0);
  const [shake, setShake] = useState(false);

  useEffect(() => {
    if (retryAfter === null || retryAfter <= 0) return;
    const id = setTimeout(() => setRetryAfter((s) => (s === null ? null : s - 1)), 1000);
    return () => clearTimeout(id);
  }, [retryAfter]);

  useEffect(() => {
    if (resendCooldown <= 0) return;
    const id = setTimeout(() => setResendCooldown((s) => Math.max(0, s - 1)), 1000);
    return () => clearTimeout(id);
  }, [resendCooldown]);

  const rateLimited = retryAfter !== null && retryAfter > 0;

  function nudge() {
    setShake(true);
    setTimeout(() => setShake(false), 400);
  }

  async function onSubmitEmail(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await authApi.signupStart(email.trim(), acceptedTerms);
      saveSignupState({ email: email.trim(), acceptedTerms, codeSent: true });
      setResendCooldown(RESEND_COOLDOWN_SECONDS);
      setStep(2);
    } catch (err) {
      if (err instanceof ApiClientError && err.code === "rate_limited") {
        setRetryAfter(Math.ceil(err.retryAfterSeconds ?? 30));
      } else {
        setError("Something went wrong. Please try again.");
      }
      nudge();
    } finally {
      setSubmitting(false);
    }
  }

  async function onResend() {
    if (resendCooldown > 0 || submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      await authApi.signupStart(email.trim(), acceptedTerms);
      setResendCooldown(RESEND_COOLDOWN_SECONDS);
    } catch {
      setError("Could not resend the code. Please try again.");
      nudge();
    } finally {
      setSubmitting(false);
    }
  }

  async function onSubmitCode(value: string) {
    setError(null);
    setSubmitting(true);
    try {
      const { ticket } = await authApi.signupVerify(email.trim(), value);
      setSignupTicket(ticket);
      setStep(3);
    } catch (err) {
      if (err instanceof ApiClientError && err.code === "invalid_code") {
        setError(GENERIC_CODE_ERROR);
      } else {
        setError("Something went wrong. Please try again.");
      }
      setCode("");
      nudge();
    } finally {
      setSubmitting(false);
    }
  }

  async function onSubmitPassword(e: FormEvent) {
    e.preventDefault();
    const ticket = getSignupTicket();
    if (!ticket) {
      // The ticket did not survive (a reload, or it simply expired) --
      // back to verify rather than submitting with nothing.
      setStep(2);
      return;
    }
    setError(null);
    setSubmitting(true);
    try {
      await completeSignup(ticket, password);
      clearSignupState();
      navigate("/onboarding", { replace: true });
    } catch (err) {
      if (err instanceof ApiClientError && err.code === "invalid_ticket") {
        setError("Your verification has expired. Please verify your code again.");
        setSignupTicket(null);
        setStep(2);
      } else if (err instanceof ApiClientError && err.code === "invalid_argument") {
        setError(err.message);
      } else {
        setError("Something went wrong. Please try again.");
      }
      nudge();
    } finally {
      setSubmitting(false);
    }
  }

  function onBack() {
    if (step === 1) {
      navigate("/sign-in");
      return;
    }
    setError(null);
    setStep((s) => (s === 3 ? 2 : 1) as Step);
  }

  return (
    <AuthShell onBack={onBack} step={step} totalSteps={TOTAL_STEPS}>
      <div key={step} className={cnStep(shake)}>
        {step === 1 && (
          <div className="space-y-5">
            <div>
              <h1 className="font-heading text-xl font-bold text-light-text">
                Create your account
              </h1>
              <p className="mt-1 text-sm text-light-muted">
                We'll email you a code to verify it's really you
              </p>
            </div>

            <form onSubmit={onSubmitEmail} className="space-y-3.5">
              <TextInput
                label="Email address"
                id="sign-up-email"
                type="email"
                required
                autoComplete="email"
                placeholder="e.g. you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                disabled={submitting || rateLimited}
              />

              <Checkbox
                id="sign-up-terms"
                checked={acceptedTerms}
                onChange={(e) => setAcceptedTerms(e.target.checked)}
                disabled={submitting || rateLimited}
                label={
                  <>
                    I agree to ARGUS's{" "}
                    <Link to="/terms" target="_blank" className="font-medium text-light-primary">
                      Terms
                    </Link>{" "}
                    and{" "}
                    <Link to="/privacy" target="_blank" className="font-medium text-light-primary">
                      Privacy Policy
                    </Link>
                  </>
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

              <PrimaryButton
                type="submit"
                loading={submitting}
                disabled={!email.trim() || !acceptedTerms || rateLimited}
              >
                Continue
              </PrimaryButton>
            </form>

            <p className="text-center text-sm text-light-muted">
              Already have an account?{" "}
              <Link to="/sign-in" className="font-medium text-light-primary">
                Log in
              </Link>
            </p>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-5">
            <div>
              <h1 className="font-heading text-xl font-bold text-light-text">Check your email</h1>
              <p className="mt-1 text-sm text-light-muted">
                Enter the 6-digit code we sent to {email || "your email"}
              </p>
            </div>

            <div className="space-y-3.5">
              <CodeInput
                value={code}
                onChange={setCode}
                onComplete={onSubmitCode}
                error={!!error}
                disabled={submitting}
              />

              {error && (
                <p className="text-xs text-light-error" role="alert">
                  {error}
                </p>
              )}

              <p className="text-sm text-light-muted">
                Didn't receive a code?{" "}
                <button
                  type="button"
                  onClick={onResend}
                  disabled={resendCooldown > 0 || submitting}
                  className="font-medium text-light-primary disabled:cursor-not-allowed disabled:text-light-muted"
                >
                  {resendCooldown > 0 ? `Resend in ${resendCooldown}s` : "Resend"}
                </button>
              </p>

              <PrimaryButton
                type="button"
                loading={submitting}
                disabled={code.length !== 6}
                onClick={() => onSubmitCode(code)}
              >
                Continue
              </PrimaryButton>
            </div>
          </div>
        )}

        {step === 3 && (
          <div className="space-y-5">
            <div>
              <h1 className="font-heading text-xl font-bold text-light-text">
                Create a password
              </h1>
              <p className="mt-1 text-sm text-light-muted">
                At least 12 characters -- make it one you don't use anywhere else
              </p>
            </div>

            <form onSubmit={onSubmitPassword} className="space-y-3.5">
              <PasswordInput
                label="Password"
                id="sign-up-password"
                required
                autoComplete="new-password"
                placeholder="Password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={submitting}
              />

              {error && (
                <p className="text-xs text-light-error" role="alert">
                  {error}
                </p>
              )}

              <PrimaryButton type="submit" loading={submitting} disabled={password.length < 12}>
                Create account
              </PrimaryButton>
            </form>
          </div>
        )}
      </div>
    </AuthShell>
  );
}

// cnStep attaches the shared slide-in-on-step-change motion to each
// step's own root (keyed by step above, so React actually remounts
// this node per step rather than patching it in place -- a CSS class
// on an element that never unmounts never replays), and layers the
// failed-submit nudge on top when one just happened (brief's own
// "Motion and feel": both are transform/opacity only and turn off
// under prefers-reduced-motion, theme.css).
function cnStep(shake: boolean): string {
  return shake ? "light-step-in light-shake" : "light-step-in";
}
