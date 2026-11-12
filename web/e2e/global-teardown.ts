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
// example.com) is left behind in argus_test, a dedicated test database
// this repo's own Go integration suite already fills with hundreds of
// throwaway rows every run (see this run's own global-setup.ts
// comment on argus_test's instrument count) -- accumulating more rows
// there is the expected, accepted cost of a test database, not a leak
// into anything shared or production.
import { readFile } from "node:fs/promises";
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

  try {
    const sessions = (await (
      await fetch(`${API_BASE}/api/v1/auth/sessions`, {
        headers: { Cookie: state.sessionCookie, Origin: ORIGIN },
      })
    ).json()) as Array<{ id: number }>;
    for (const s of sessions) {
      await fetch(`${API_BASE}/api/v1/auth/sessions/${s.id}`, {
        method: "DELETE",
        headers: { Cookie: state.sessionCookie, Origin: ORIGIN },
      });
    }
  } catch (err) {
    console.warn(`e2e teardown: could not revoke session for ${state.email}: ${String(err)}`);
  }

  console.log(
    `e2e teardown: revoked the session for ${state.email} (user id ${state.userId}). ` +
      `The user row itself is NOT deleted -- there is no account-deletion endpoint in this API (see this file's own doc comment). It remains in argus_test, the dedicated test database.`,
  );
}
