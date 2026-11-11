// Signup's own client-side state (web light auth redesign brief, Part
// 3): email and whether terms were accepted/a code was sent live in
// sessionStorage so a refresh resumes the flow instead of silently
// restarting it; the signup ticket does not -- it lives in this
// module's own in-memory variable only (ADR-015: a signup ticket is as
// sensitive as a password-reset token, the same reason a password is
// never persisted either). A reload losing the ticket is by design:
// the verify step is repeated, not treated as a hole to patch around.

const STORAGE_KEY = "argus.signup";

export interface StoredSignupState {
  email: string;
  acceptedTerms: boolean;
  /** Whether step 1 already sent a code for this email. */
  codeSent: boolean;
}

let ticketInMemory: string | null = null;

export function getSignupTicket(): string | null {
  return ticketInMemory;
}

export function setSignupTicket(ticket: string | null): void {
  ticketInMemory = ticket;
}

export function loadSignupState(): StoredSignupState | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<StoredSignupState>;
    if (typeof parsed.email !== "string" || parsed.email.length === 0) return null;
    return {
      email: parsed.email,
      acceptedTerms: parsed.acceptedTerms === true,
      codeSent: parsed.codeSent === true,
    };
  } catch {
    // Storage can throw (private mode, quota, or disabled entirely) --
    // the flow still works for this one page load, starting at step 1.
    return null;
  }
}

export function saveSignupState(state: StoredSignupState): void {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Same as above: a failed write just means a refresh restarts the
    // flow, not a thrown error the user sees.
  }
}

// clearSignupState wipes both the persisted state and the in-memory
// ticket -- called once signup actually completes (the flow is over)
// or when the user backs all the way out of it.
export function clearSignupState(): void {
  try {
    sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
  ticketInMemory = null;
}
