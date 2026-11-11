import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { CircleNotch } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/ui/panel";
import { Pill } from "@/components/ui/pill";
import { listSessions, revokeSession } from "@/api/auth";
import { ApiClientError } from "@/api/client";
import type { ApiAuthSession } from "@/api/types";
import { useAuth } from "@/auth/auth-context";
import { formatUTC } from "@/lib/format";
import { parseUserAgent } from "@/lib/parse-user-agent";

export function Account() {
  const { user, signOutAll } = useAuth();
  const navigate = useNavigate();

  const [sessions, setSessions] = useState<ApiAuthSession[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiClientError | null>(null);
  const [revokingId, setRevokingId] = useState<number | null>(null);
  const [confirmingSignOutAll, setConfirmingSignOutAll] = useState(false);

  const loadSessions = useCallback((signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    listSessions({ signal })
      .then((rows) => setSessions(rows))
      .catch((err: unknown) => {
        if (signal?.aborted) return;
        setError(err instanceof ApiClientError ? err : new ApiClientError(String(err), 0));
      })
      .finally(() => {
        if (signal?.aborted) return;
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    loadSessions(controller.signal);
    return () => controller.abort();
  }, [loadSessions]);

  async function onRevoke(id: number) {
    setRevokingId(id);
    try {
      await revokeSession(id);
      setSessions((rows) => rows?.filter((s) => s.id !== id) ?? rows);
    } catch {
      // Leave the row in place; the Revoke button is still there to retry.
    } finally {
      setRevokingId(null);
    }
  }

  async function onSignOutAll() {
    setConfirmingSignOutAll(false);
    await signOutAll();
    navigate("/login", { replace: true });
  }

  return (
    <div className="mx-auto h-full max-w-[720px] space-y-6 overflow-y-auto p-4 sm:p-6">
      <h1 className="text-2xl font-semibold tracking-tight">Account</h1>

      {user?.display_name && (
        <Panel variant="glass" pad="lg" className="space-y-1.5">
          <div className="text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
            Display name
          </div>
          <div className="text-sm">{user.display_name}</div>
        </Panel>
      )}

      <Panel variant="glass" pad="lg" className="space-y-1.5">
        <div className="text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
          Email
        </div>
        <div className="flex items-center gap-2 text-sm">
          <span>{user?.email}</span>
          {user?.email_verified ? (
            <Pill variant="prov-s" dot>
              Verified
            </Pill>
          ) : (
            <Pill variant="prov-u" dot>
              Not verified
            </Pill>
          )}
        </div>
      </Panel>

      <div>
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-[0.12em] text-fg-subtle">
            Sessions
          </h2>

          <DropdownMenu.Root open={confirmingSignOutAll} onOpenChange={setConfirmingSignOutAll}>
            <DropdownMenu.Trigger asChild>
              <Button variant="outline" size="sm">
                Sign out everywhere
              </Button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content
                align="end"
                sideOffset={8}
                className="glass-strong z-[60] w-64 rounded-lg p-3 shadow-xl"
              >
                <p className="text-[13px] text-fg-muted">
                  Sign out of every session, including this one? You will need to sign in again.
                </p>
                <div className="mt-2.5 flex justify-end gap-1.5">
                  <DropdownMenu.Item
                    onSelect={(e) => e.preventDefault()}
                    onClick={() => setConfirmingSignOutAll(false)}
                    className="cursor-pointer rounded-md px-2.5 py-1 text-[12px] font-medium text-fg-muted outline-none hover:bg-white/5"
                  >
                    Cancel
                  </DropdownMenu.Item>
                  <DropdownMenu.Item
                    onSelect={onSignOutAll}
                    className="cursor-pointer rounded-md bg-down/15 px-2.5 py-1 text-[12px] font-medium text-down outline-none hover:bg-down/25"
                  >
                    Sign out everywhere
                  </DropdownMenu.Item>
                </div>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        </div>

        <Panel variant="glass" pad="none" className="overflow-hidden">
          {error && (
            <div className="px-4 py-8 text-center text-[13px] text-down">
              Could not load sessions: {error.message}
            </div>
          )}

          {!error && loading && (
            <div className="flex items-center justify-center gap-2 px-4 py-10 text-[13px] text-fg-subtle">
              <CircleNotch className="size-4 animate-spin" />
              Loading…
            </div>
          )}

          {!error && !loading && sessions?.length === 0 && (
            <div className="px-4 py-10 text-center text-[13px] text-fg-subtle">
              No active sessions.
            </div>
          )}

          {!error &&
            !loading &&
            sessions?.map((s) => (
              <div
                key={s.id}
                className="flex items-center justify-between gap-3 border-b border-line/60 px-4 py-3 text-[13px] last:border-0"
              >
                <div>
                  <div className="flex items-center gap-2 font-medium">
                    {parseUserAgent(s.user_agent)}
                    {s.current && <Pill variant="default">This device</Pill>}
                  </div>
                  <div className="tnum mt-0.5 text-[12px] text-fg-subtle">
                    {s.ip ?? "Unknown IP"} · Created {formatUTC(s.created_at)} · Last seen{" "}
                    {formatUTC(s.last_seen_at)}
                  </div>
                </div>
                {!s.current && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => onRevoke(s.id)}
                    disabled={revokingId === s.id}
                  >
                    {revokingId === s.id ? "Revoking…" : "Revoke"}
                  </Button>
                )}
              </div>
            ))}
        </Panel>
      </div>
    </div>
  );
}
