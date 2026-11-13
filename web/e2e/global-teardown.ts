// global-teardown.ts: best-effort cleanup after the whole suite.
//
// What this can do: revoke the throwaway user's own session (DELETE
// /auth/sessions/{id}, the real endpoint, so the session row and its
// cookie are both dead immediately).
//
// What this cannot do, and why: there is no account-deletion endpoint
// anywhere in api/openapi.yaml -- checked it exhaustively (grepped for
// "delete:" and every /me, /auth path); the only delete operation in
// the whole API is session revocation. ADR-016 (retention, deletion,
// privacy) governs how a user's data is actually removed, and nothing
// in this phase's own scope gives the frontend -- or this test suite
// -- a way to invoke that. Writing a raw SQL DELETE against the user
// row would bypass ADR-016's own rules entirely (soft-delete, grace
// periods, whatever it specifies) for the sake of a test's own
// tidiness, which is exactly the kind of silent deviation CLAUDE.md
// rule 3 says to flag instead of guessing at -- so this does not do
// that. The user row (email, times out at e2e-<timestamp>-<random>@
// example.com) is left behind in argus_e2e -- but only until the next
// run: Makefile's own e2e-resetdb target drops and recreates that
// database before every e2e-stack run ("fresh database per e2e run"),
// so this row never actually accumulates across runs the way it would
// on a long-lived database. Not a leak into anything shared or
// production either way: argus_e2e is this suite's own dedicated
// database, never the dev argus one and never argus_test (the Go
// integration suite's own).
import { readFile, rm } from "node:fs/promises";
import { AUTH_FILE, type E2EAuthState } from "./global-setup";

const API_BASE = `http://localhost:${process.env.E2E_API_PORT ?? "8090"}`;
const ORIGIN = "http://localhost:4173";

export default async function globalTeardown(): Promise<void> {
  let state: E2EAuthState;
  try {
    state = JSON.parse(await readFile(AUTH_FILE, "utf-8")) as E2EAuthState;
  } catch {
    return; // global setup itself must have failed before writing the file
  }

  let revoked = false;
  try {
    const res = await fetch(`${API_BASE}/api/v1/auth/sessions`, {
      headers: { Cookie: state.sessionCookie, Origin: ORIGIN },
    });
    if (!res.ok) throw new Error(`GET /auth/sessions -> ${res.status}: ${await res.text()}`);
    const sessions = (await res.json()) as Array<{ id: number }>;
    for (const s of sessions) {
      await fetch(`${API_BASE}/api/v1/auth/sessions/${s.id}`, {
        method: "DELETE",
        headers: { Cookie: state.sessionCookie, Origin: ORIGIN },
      });
    }
    revoked = true;
  } catch (err) {
    console.warn(`e2e teardown: could not revoke session for ${state.email}: ${String(err)}`);
  } finally {
    // Never leave this run's own file for a LATER failed run's own
    // teardown to misread as still current (see global-setup.ts's own
    // doc comment on the same file at its own startup).
    await rm(AUTH_FILE, { force: true });
  }

  if (revoked) {
    console.log(
      `e2e teardown: revoked the session for ${state.email} (user id ${state.userId}). ` +
        `The user row itself is NOT deleted -- there is no account-deletion endpoint in this API (see this file's own doc comment). It remains in the e2e database.`,
    );
  }
}
