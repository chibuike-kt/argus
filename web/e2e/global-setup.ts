// global-setup.ts: runs once before the whole Playwright suite. Creates
// one throwaway user through the real HTTP signup flow (never a
// hand-made cookie, never a direct database write -- the Claude Code
// auto-mode classifier declined a direct-DB-write attempt for this
// exact purpose earlier in this brief's own work, and going through
// the real endpoints is the correct way to do it anyway), looks up a
// real instrument and setup id at run time (never hardcoded), and
// writes everything the suite and global-teardown need into a single
// git-ignored file -- generated fresh by this run, never committed,
// never a file a human hand-edits.
import { readFileSync } from "node:fs";
import { mkdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const API_BASE = `http://localhost:${process.env.E2E_API_PORT ?? "8090"}`;
const ORIGIN = "http://localhost:4173"; // matches Makefile's own e2e-stack ARGUS_AUTH_ALLOWED_ORIGINS
const API_LOG_PATH = process.env.E2E_API_LOG ?? "/tmp/argus-e2e-api.log";

// package.json's own "type": "module" means no CommonJS __dirname here.
const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const AUTH_DIR = path.join(__dirname, ".auth");
export const AUTH_FILE = path.join(AUTH_DIR, "session.json");

export interface E2EAuthState {
  sessionCookie: string;
  userId: number;
  email: string;
  btcInstrumentId: number;
  ethInstrumentId: number;
  btcSetupId: number;
  btcProvider: string;
  ethProvider: string;
}

async function api(method: string, pathName: string, cookie: string | undefined, body?: unknown) {
  const res = await fetch(`${API_BASE}/api/v1${pathName}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      Origin: ORIGIN,
      ...(cookie ? { Cookie: cookie } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    throw new Error(`${method} ${pathName} -> ${res.status}: ${await res.text()}`);
  }
  return res;
}

// readSignupCode polls the test API's own log file (the dev LogMailer
// writes every "sent" email there -- internal/identity/mailer, never a
// real email in dev/test) for the 6-digit code just sent to email.
// Short retry loop: signup/start's own handler sends (and so logs) the
// email synchronously before it returns, but the log line reaching
// disk is not instantaneous.
async function readSignupCode(email: string): Promise<string> {
  for (let attempt = 0; attempt < 20; attempt++) {
    let log = "";
    try {
      log = readFileSync(API_LOG_PATH, "utf-8");
    } catch {
      // log file not created yet
    }
    const lines = log.split("\n").filter((l) => l.includes(email) && l.includes("signup code"));
    const last = lines[lines.length - 1];
    if (last) {
      const match = /signup code is (\d{6})/.exec(last);
      if (match) return match[1];
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(
    `signup code for ${email} not found in ${API_LOG_PATH} after 5s -- is the test API's own stdout actually redirected there (Makefile's e2e-stack target does this via 'tee')?`,
  );
}

function cookieValue(setCookieHeader: string | null, name: string): string {
  if (!setCookieHeader) throw new Error("no Set-Cookie header in signup/complete's own response");
  const match = new RegExp(`${name}=([^;]+)`).exec(setCookieHeader);
  if (!match) throw new Error(`Set-Cookie header did not contain ${name}`);
  return `${name}=${match[1]}`;
}

export default async function globalSetup(): Promise<void> {
  // A failed run from earlier leaves this file's own previous
  // (successful) contents in place, since nothing before this deletes
  // it -- global-teardown's own "no file -> global setup must have
  // failed" assumption otherwise breaks the moment THIS run fails
  // after an EARLIER successful one: teardown would read that stale
  // file and try to revoke a session on a backend/database that no
  // longer has any idea what it is.
  await rm(AUTH_FILE, { force: true });

  const email = `e2e-${Date.now()}-${Math.floor(Math.random() * 1e6)}@example.com`;

  await api("POST", "/auth/signup/start", undefined, { email, accepted_terms: true });
  const code = await readSignupCode(email);

  const verifyRes = await api("POST", "/auth/signup/verify", undefined, { email, code });
  const { ticket } = (await verifyRes.json()) as { ticket: string };

  const completeRes = await fetch(`${API_BASE}/api/v1/auth/signup/complete`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: ORIGIN },
    body: JSON.stringify({ ticket, password: "Correct-Horse-Battery-Staple-9" }),
  });
  if (!completeRes.ok) throw new Error(`signup/complete -> ${completeRes.status}: ${await completeRes.text()}`);
  const sessionCookie = cookieValue(completeRes.headers.get("set-cookie"), "argus_session");
  const user = (await completeRes.json()) as { id: number };

  await api("PUT", "/me/profile", sessionCookie, { display_name: "Playwright E2E" });

  // Look up real instruments at run time -- never hardcoded ids. The
  // dev fixture seed (db/seed/dev_fixture_instruments.sql, applied by
  // Makefile's own e2e-stack target) always creates exactly these two
  // symbols, but never at a fixed id -- argus_e2e is reset fresh before
  // every run (Makefile's own e2e-resetdb), so in practice these are
  // the first two rows, but paginating (after_id, the API's own
  // cursor) rather than trusting that is what makes this correct even
  // if that ever changes, not just convenient today.
  type Instrument = { id: number; native_symbol: string; provider: string };
  let btc: Instrument | undefined;
  let eth: Instrument | undefined;
  let afterId: number | undefined;
  for (let page = 0; page < 20 && (!btc || !eth); page++) {
    const qs = afterId ? `&after_id=${afterId}` : "";
    const batch = (await (
      await api("GET", `/instruments?enabled=all&limit=500${qs}`, sessionCookie)
    ).json()) as Instrument[];
    if (batch.length === 0) break;
    btc ??= batch.find((i) => i.native_symbol === "BTC-PERP");
    eth ??= batch.find((i) => i.native_symbol === "ETH-PERP");
    afterId = batch[batch.length - 1].id;
  }
  if (!btc || !eth) {
    throw new Error(
      "BTC-PERP/ETH-PERP not found after paginating every instrument -- did Makefile's e2e-stack target's own seed-dev step run against argus_e2e?",
    );
  }

  await api("PUT", "/watchlist", sessionCookie, { instrument_ids: [btc.id, eth.id] });
  await api("POST", "/me/onboarding/complete", sessionCookie);

  // "Fresh database per e2e run" (Makefile's own e2e-resetdb) means
  // setups do not already exist the way they always did on a long-
  // lived, never-reset argus_e2e -- the analysis engine
  // (cmd/argus-worker's own processing loop, ticking once a minute)
  // has to actually run against the just-ingested candles before any
  // exist. Poll rather than guess a fixed sleep: real end-to-end
  // confirmation that processing has caught up, not a timing guess.
  let setups: Array<{ id: number }> = [];
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    setups = (await (await api("GET", `/setups?instrument_id=${btc.id}`, sessionCookie)).json()) as Array<{ id: number }>;
    if (setups.length > 0) break;
    await new Promise((resolve) => setTimeout(resolve, 3_000));
  }
  if (setups.length === 0) {
    throw new Error(
      `no setups found for BTC-PERP (instrument ${btc.id}) after waiting 90s -- the worker's own processing loop (cmd/argus-worker, ticks once a minute) may not be running, or the dev fixture ingest did not complete`,
    );
  }

  const state: E2EAuthState = {
    sessionCookie,
    userId: user.id,
    email,
    btcInstrumentId: btc.id,
    ethInstrumentId: eth.id,
    btcSetupId: setups[0].id,
    btcProvider: btc.provider,
    ethProvider: eth.provider,
  };

  await mkdir(AUTH_DIR, { recursive: true });
  await writeFile(AUTH_FILE, JSON.stringify(state, null, 2));
}
