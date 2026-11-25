import { ApiClientError, getJSON, putJSON, apiBaseUrl } from "@/api/client";
import { ConflictError, RejectedError, VersionedSync, type Remote, type SyncAdapter } from "@/lib/versioned-sync";
import { LocalStorageDrawingsPersistence, type DrawingsPersistence } from "./store";
import type { Drawing } from "./types";

// Drawings per instrument, stored on the server per user (owner decision D1,
// brief-argus-product-overhaul.md Phase 1), with the existing localStorage
// copy as the instant cache. A drawing belongs to the instrument and shows
// on every timeframe (owner decision D17; ADR-008: "User drawings must
// remain anchored consistently when users change chart timeframes"). One
// VersionedSync per instrument; drawings merge by id, so two tabs editing
// different drawings on the same instrument never overwrite each other.

// [U] UI values, the same as preferences.
const DEBOUNCE_MS = 1000;
const MAX_BACKOFF_MS = 30_000;
const META_PREFIX = "argus:drawings-sync:v1:";
// A scope is an instrument id (D17). Before D17 it was "id:timeframe".
const SCOPE = /^([1-9][0-9]*)$/;
const ORDER_KEY = "__order";

export const drawingsAdapter: SyncAdapter<Drawing[]> = {
  keys(doc) {
    return [...doc.map((d) => d.id), ORDER_KEY];
  },
  get(doc, key) {
    if (key === ORDER_KEY) return doc.map((d) => d.id);
    return doc.find((d) => d.id === key);
  },
  set(doc, key, value) {
    if (key === ORDER_KEY) {
      if (!Array.isArray(value)) return doc;
      const byId = new Map(doc.map((d) => [d.id, d]));
      const ordered: Drawing[] = [];
      for (const id of value as string[]) {
        const d = byId.get(id);
        if (d) {
          ordered.push(d);
          byId.delete(id);
        }
      }
      return [...ordered, ...byId.values()];
    }
    const i = doc.findIndex((d) => d.id === key);
    if (value === undefined) return i < 0 ? doc : doc.filter((d) => d.id !== key);
    const next = [...doc];
    if (i < 0) next.push(value as Drawing);
    else next[i] = value as Drawing;
    return next;
  },
};

function asDrawings(v: unknown): Drawing[] {
  return Array.isArray(v) ? (v.filter((d) => d && typeof d === "object" && typeof d.id === "string" && typeof d.kind === "string") as Drawing[]) : [];
}

function remoteFor(instrumentId: number): Remote<Drawing[]> {
  const path = `/me/drawings?instrument_id=${instrumentId}`;
  return {
    async get() {
      const r = await getJSON<{ data: unknown; version: number }>(path);
      return { data: asDrawings(r.data), version: r.version };
    },
    async put(data, version, opts) {
      if (opts?.keepalive) {
        await fetch(`${apiBaseUrl()}${path}`, {
          method: "PUT",
          keepalive: true,
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ data, version }),
        });
        return { data, version: version + 1 };
      }
      try {
        const r = await putJSON<{ data: unknown; version: number }>(path, { data, version });
        return { data: asDrawings(r.data), version: r.version };
      } catch (err) {
        if (err instanceof ApiClientError && err.status === 409) throw new ConflictError(err.message);
        if (err instanceof ApiClientError && [400, 404, 413].includes(err.status)) throw new RejectedError(err.message);
        throw err;
      }
    },
  };
}

interface Meta {
  baseVersion: number;
  dirty: string[];
}

function readMeta(userId: number, scope: string): Meta | null {
  try {
    const raw = window.localStorage.getItem(`${META_PREFIX}${userId}:${scope}`);
    if (!raw) return null;
    const m = JSON.parse(raw) as Partial<Meta>;
    return {
      baseVersion: Number.isInteger(m.baseVersion) ? (m.baseVersion as number) : 0,
      dirty: Array.isArray(m.dirty) ? m.dirty.filter((k): k is string => typeof k === "string") : [],
    };
  } catch {
    return null;
  }
}

function writeMeta(userId: number, scope: string, meta: Meta): void {
  try {
    window.localStorage.setItem(`${META_PREFIX}${userId}:${scope}`, JSON.stringify(meta));
  } catch {
    /* in-memory and server copies still work */
  }
}

// SyncedDrawingsPersistence is the drawings store's persistence: local
// cache first (instant), the server per chart once a user is signed in.
export class SyncedDrawingsPersistence implements DrawingsPersistence {
  private local = new LocalStorageDrawingsPersistence();
  private syncs = new Map<string, VersionedSync<Drawing[]>>();
  private legacy = new Map<string, string[]>();
  // Charts already loaded this session, so a user attached later (session
  // restore finishes after the chart mounts) still starts their sync.
  private loaded = new Set<string>();
  private userId: number | null = null;
  // Set by the store wiring: pushes a server-side change into the store.
  onRemote: ((scope: string, drawings: Drawing[]) => void) | null = null;

  // attachUser starts (or, with null, stops) syncing for a signed-in user.
  attachUser(userId: number | null): void {
    if (userId === this.userId) return;
    for (const s of this.syncs.values()) {
      s.flushKeepalive();
      s.stop();
    }
    this.syncs.clear();
    this.userId = userId;
    if (userId !== null) for (const scope of this.loaded) this.ensureSync(scope, this.local.load(scope) ?? []);
  }

  // aliasLegacy names the keys an instrument's drawings were cached under
  // before: one per timeframe ("id:1m" ... "id:1d", before D17) and, older
  // still, by symbol ("BTC:1m" ...). The first load of the instrument with
  // nothing cached under its own key merges them all, once (mergeDrawingLists).
  aliasLegacy(scope: string, legacyScopes: string[]): void {
    this.legacy.set(scope, legacyScopes);
  }

  load(scope: string): Drawing[] | null {
    let data = this.local.load(scope);
    const legacyKeys = this.legacy.get(scope);
    if (data === null && legacyKeys && legacyKeys.length > 0) {
      const found = legacyKeys.map((k) => this.local.load(k)).filter((d): d is Drawing[] => d !== null);
      if (found.length > 0) {
        data = mergeDrawingLists(found);
        this.local.save(scope, data);
      }
    }
    this.loaded.add(scope);
    this.ensureSync(scope, data ?? []);
    return data;
  }

  save(scope: string, drawings: Drawing[]): void {
    this.local.save(scope, drawings);
    this.syncs.get(scope)?.replace(drawings);
  }

  flushKeepalive(): void {
    for (const s of this.syncs.values()) s.flushKeepalive();
  }

  nudge(): void {
    for (const s of this.syncs.values()) s.nudge();
  }

  private ensureSync(scope: string, data: Drawing[]): void {
    const m = SCOPE.exec(scope);
    const userId = this.userId;
    if (!m || userId === null || this.syncs.has(scope)) return;
    // Never synced from this browser: every drawing already here is the
    // user's own unsynced work (drawings made before server sync) -- push
    // it, merged onto whatever the server holds, rather than letting an
    // empty server copy replace it.
    const meta = readMeta(userId, scope) ?? {
      baseVersion: 0,
      dirty: data.length > 0 ? drawingsAdapter.keys(data) : [],
    };
    const sync = new VersionedSync(drawingsAdapter, remoteFor(Number(m[1])), { data, ...meta }, {
      debounceMs: DEBOUNCE_MS,
      maxBackoffMs: MAX_BACKOFF_MS,
      onChange: (state) => {
        this.local.save(scope, state.data);
        writeMeta(userId, scope, { baseVersion: state.baseVersion, dirty: state.dirty });
        this.onRemote?.(scope, state.data);
      },
    });
    this.syncs.set(scope, sync);
    void sync.pull().catch(() => {}); // server down: the local copy stands
  }
}

// mergeDrawingLists merges drawing lists cached per timeframe before D17
// into one, the rule the server's merge uses (internal/preferences,
// MergeTimeframeDrawings): every drawing kept once per id; a duplicate
// keeps its most recently changed copy (larger updatedAt; a tie goes to
// the later list); z order (last is in front) follows the lists in the
// order given, each in its own order, a duplicate sitting where its kept
// copy sat.
export function mergeDrawingLists(lists: Drawing[][]): Drawing[] {
  const best = new Map<string, { seq: number; d: Drawing }>();
  let seq = 0;
  for (const list of lists) {
    for (const d of list) {
      const prev = best.get(d.id);
      if (!prev || (d.updatedAt ?? 0) >= (prev.d.updatedAt ?? 0)) best.set(d.id, { seq, d });
      seq++;
    }
  }
  return [...best.values()].sort((a, b) => a.seq - b.seq).map((x) => x.d);
}
