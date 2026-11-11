import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { CircleNotch } from "@phosphor-icons/react";
import { AuthShell } from "@/components/light/auth-shell";
import { TextInput } from "@/components/light/text-input";
import { PrimaryButton } from "@/components/light/primary-button";
import { Checkbox } from "@/components/light/checkbox";
import { CoinIcon } from "@/components/ui/coin-icon";
import { ApiClientError } from "@/api/client";
import * as authApi from "@/api/auth";
import { useInstruments } from "@/api/hooks";
import { useAuth } from "@/auth/auth-context";

const TOTAL_STEPS = 5;
const WATCHLIST_MAX_ITEMS = 50;

type Step = 4 | 5;

// Onboarding's own step is derived from the server's state, never from
// sessionStorage (unlike sign-up's own ticket-in-memory handling): a
// signed-in user's display_name/watchlist are already the source of
// truth (ADR-015: "A signed in user who has not completed onboarding
// is sent back to the step they left"), so a refresh here just re-reads
// the same user object AuthProvider's own boot call already fetched,
// with nothing client-only to lose.
export function Onboarding() {
  const { user, applyUser } = useAuth();
  const navigate = useNavigate();

  const [step, setStep] = useState<Step>(user?.display_name ? 5 : 4);
  const [displayName, setDisplayName] = useState(user?.display_name ?? "");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [shake, setShake] = useState(false);

  const { data: instruments, loading: instrumentsLoading } = useInstruments();

  // The useState initializers above only read user once, at this
  // component's own first render -- correct under RequireAuth in the
  // real route tree (user is already loaded by the time /onboarding
  // ever mounts), but not something this component should depend on:
  // user can still change (or arrive) after mount in any other
  // context (tests being the obvious one), so step/displayName track
  // it reactively here too, not just at construction time.
  useEffect(() => {
    if (user?.display_name) {
      setStep(5);
      setDisplayName(user.display_name);
    }
  }, [user?.display_name]);

  // Pre-select whatever is already saved (a user who got to step 5,
  // picked some pairs, then came back for any reason should see their
  // own prior picks, not an empty list). Every hook must run on every
  // render regardless of the onboarding_completed check below (Rules
  // of Hooks) -- this one is a no-op when step !== 5 anyway.
  useEffect(() => {
    if (step !== 5) return;
    let cancelled = false;
    authApi
      .getWatchlist()
      .then((items) => {
        if (cancelled) return;
        setSelected(new Set(items.map((i) => i.instrument_id)));
      })
      .catch(() => {
        // No saved watchlist yet (or a transient fetch error) -- an
        // empty starting selection is the correct fallback either way.
      });
    return () => {
      cancelled = true;
    };
  }, [step]);

  // Checked after every hook above has run (Rules of Hooks): this
  // route is reachable for one render right after onboarding/complete
  // resolves, before the caller's own navigate("/setups") takes effect
  // -- a direct visit to /onboarding once already completed lands here
  // too (a stale bookmark, or clicking back).
  if (user?.onboarding_completed) {
    return <Navigate to="/setups" replace />;
  }

  function nudge() {
    setShake(true);
    setTimeout(() => setShake(false), 400);
  }

  async function onSubmitName(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const updated = await authApi.updateProfile(displayName.trim());
      applyUser(updated);
      setStep(5);
    } catch (err) {
      if (err instanceof ApiClientError && err.code === "invalid_argument") {
        setError(err.message);
      } else {
        setError("Something went wrong. Please try again.");
      }
      nudge();
    } finally {
      setSubmitting(false);
    }
  }

  function toggleInstrument(id: number) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else if (next.size < WATCHLIST_MAX_ITEMS) {
        next.add(id);
      }
      return next;
    });
  }

  async function onSubmitWatchlist(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await authApi.updateWatchlist(Array.from(selected));
      const updated = await authApi.completeOnboarding();
      applyUser(updated);
      navigate("/setups", { replace: true });
    } catch {
      setError("Something went wrong. Please try again.");
      nudge();
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <AuthShell
      onBack={step === 5 ? () => setStep(4) : undefined}
      step={step}
      totalSteps={TOTAL_STEPS}
    >
      <div key={step} className={shake ? "light-step-in light-shake" : "light-step-in"}>
        {step === 4 && (
          <div className="space-y-5">
            <div>
              <h1 className="font-heading text-xl font-bold text-light-text">
                What should we call you?
              </h1>
              <p className="mt-1 text-sm text-light-muted">
                This is how your name appears in ARGUS
              </p>
            </div>

            <form onSubmit={onSubmitName} className="space-y-3.5">
              <TextInput
                label="Display name"
                id="onboarding-display-name"
                required
                autoComplete="name"
                placeholder="e.g. Ada"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                disabled={submitting}
              />

              {error && (
                <p className="text-xs text-light-error" role="alert">
                  {error}
                </p>
              )}

              <PrimaryButton type="submit" loading={submitting} disabled={!displayName.trim()}>
                Continue
              </PrimaryButton>
            </form>
          </div>
        )}

        {step === 5 && (
          <div className="space-y-5">
            <div>
              <h1 className="font-heading text-xl font-bold text-light-text">
                Pick a few pairs to watch
              </h1>
              <p className="mt-1 text-sm text-light-muted">
                Choose at least one -- you can change this later
              </p>
            </div>

            <form onSubmit={onSubmitWatchlist} className="space-y-3.5">
              <div className="max-h-[320px] space-y-1 overflow-y-auto rounded-[10px] border border-light-line p-2">
                {instrumentsLoading && (
                  <div className="flex items-center justify-center gap-2 py-8 text-sm text-light-muted">
                    <CircleNotch className="size-4 animate-spin" />
                    Loading instruments…
                  </div>
                )}
                {!instrumentsLoading && (instruments ?? []).length === 0 && (
                  <p className="px-2 py-8 text-center text-sm text-light-muted">
                    No instruments are available to watch yet.
                  </p>
                )}
                {!instrumentsLoading &&
                  (instruments ?? [])
                    .filter((inst) => inst.enabled)
                    .map((inst) => (
                      // Checkbox already renders its own full-row <label>
                      // (box + label content both clickable) -- wrapping
                      // it in a second <label>/onClick here would nest
                      // labels and double-toggle on a single click, so
                      // the icon+symbol go directly into its own label
                      // prop instead of around it.
                      <div key={inst.id} className="rounded-[7px] px-2 py-2 hover:bg-light-fill">
                        <Checkbox
                          id={`watchlist-${inst.id}`}
                          checked={selected.has(inst.id)}
                          onChange={() => toggleInstrument(inst.id)}
                          label={
                            <span className="flex items-center gap-2.5 text-sm font-medium text-light-text">
                              <CoinIcon symbol={inst.native_symbol} size={20} />
                              {inst.native_symbol.replace(".P", "")}
                            </span>
                          }
                        />
                      </div>
                    ))}
              </div>

              {error && (
                <p className="text-xs text-light-error" role="alert">
                  {error}
                </p>
              )}

              <PrimaryButton type="submit" loading={submitting} disabled={selected.size === 0}>
                Finish
              </PrimaryButton>
            </form>
          </div>
        )}
      </div>
    </AuthShell>
  );
}
