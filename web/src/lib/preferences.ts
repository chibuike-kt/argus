import { useSyncExternalStore } from "react";
import { apiBaseUrl, ApiClientError, getJSON, putJSON } from "@/api/client";
import { fixturesEnabled } from "@/mocks/fixtures-as-api";
import {
  ConflictError,
  RejectedError,
  VersionedSync,
  type Remote,
  type SyncAdapter,
  type SyncState,
} from "./versioned-sync";

// Everything the user sets, stored on the server per user (owner decision
// D1, brief-argus-product-overhaul.md Phase 1) with localStorage as the
// instant first-paint cache. The shape mirrors internal/preferences.Document
// exactly: the server rejects any key not named there.

export type Timeframe = "1m" | "5m" | "15m" | "1h" | "4h" | "1d";

export interface ChartViewPref {
  from_time: number;
  to_time: number;
  bars_visible: number;
  bar_spacing: number;
  right_offset: number;
  pinned_to_latest: boolean;
}

export interface DrawingToolsPref {
  magnet?: boolean;
  stay_in_drawing_mode?: boolean;
  locked?: boolean;
  hidden?: boolean;
}

export interface Prefs {
  selected_instrument_id?: number;
  timeframe?: Timeframe;
  right_panel_tab?: "analysis" | "watchlist";
  right_panel_width?: number;
  tool_rail_collapsed?: boolean;
  other_setups_open?: boolean;
  watchlist_groups_collapsed?: Record<string, boolean>;
  drawing_tools?: DrawingToolsPref;
  chart_views?: Record<string, ChartViewPref>;
}

// [U] UI values (brief: "debounced about 1 s"; backoff is ours).
export const PREFS_DEBOUNCE_MS = 1000;
export const PREFS_MAX_BACKOFF_MS = 30_000;
// The server's own cap: a larger local copy is junk, never sent.
const MAX_LOCAL_BYTES = 64 * 1024;

const CACHE_PREFIX = "argus:prefs:v1:user:";
const LAST_USER_KEY = "argus:prefs:v1:last-user";

// Keys whose value is a map/object: synced and merged per entry
// ("chart_views/4:1m"), so two tabs changing different entries never
// overwrite each other. Every other key is one scalar.
const MAP_KEYS = new Set(["watchlist_groups_collapsed", "drawing_tools", "chart_views"]);
const TIMEFRAMES = new Set(["1m", "5m", "15m", "1h", "4h", "1d"]);
const SEP = "/";

export const prefsAdapter: SyncAdapter<Prefs> = {
  keys(doc) {
    const out: string[] = [];
    for (const [k, v] of Object.entries(doc)) {
      if (MAP_KEYS.has(k) && v && typeof v === "object") {
        for (const sub of Object.keys(v)) out.push(k + SEP + sub);
      } else if (v !== undefined) {
        out.push(k);
      }
    }
    return out;
  },
  get(doc, key) {
    const i = key.indexOf(SEP);
    const rec = doc as Record<string, unknown>;
    if (i < 0) return rec[key];
    const m = rec[key.slice(0, i)] as Record<string, unknown> | undefined;
    return m?.[key.slice(i + 1)];
  },
  set(doc, key, value) {
    const next = { ...doc } as Record<string, unknown>;
    const i = key.indexOf(SEP);
    if (i < 0) {
      if (value === undefined) delete next[key];
      else next[key] = value;
      return next as Prefs;
    }
    const top = key.slice(0, i);
    const sub = key.slice(i + 1);
    const m = { ...((next[top] as Record<string, unknown> | undefined) ?? {}) };
    if (value === undefined) delete m[sub];
    else m[sub] = value;
    if (Object.keys(m).length === 0) delete next[top];
    else next[top] = m;
    return next as Prefs;
  },
};

const isBool = (v: unknown): v is boolean => typeof v === "boolean";
const isFiniteNum = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

// sanitize keeps only what the server's schema accepts, so a corrupt or
// stale local copy can never crash the app or be pushed into a 400 loop.
export function sanitizePrefs(raw: unknown): Prefs {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
  const r = raw as Record<string, unknown>;
  const out: Prefs = {};
  if (Number.isInteger(r.selected_instrument_id) && (r.selected_instrument_id as number) > 0)
    out.selected_instrument_id = r.selected_instrument_id as number;
  if (typeof r.timeframe === "string" && TIMEFRAMES.has(r.timeframe)) out.timeframe = r.timeframe as Timeframe;
  if (r.right_panel_tab === "analysis" || r.right_panel_tab === "watchlist") out.right_panel_tab = r.right_panel_tab;
  if (Number.isInteger(r.right_panel_width) && (r.right_panel_width as number) >= 200 && (r.right_panel_width as number) <= 1200)
    out.right_panel_width = r.right_panel_width as number;
  if (isBool(r.tool_rail_collapsed)) out.tool_rail_collapsed = r.tool_rail_collapsed;
  if (isBool(r.other_setups_open)) out.other_setups_open = r.other_setups_open;
  if (r.watchlist_groups_collapsed && typeof r.watchlist_groups_collapsed === "object") {
    const g: Record<string, boolean> = {};
    for (const [k, v] of Object.entries(r.watchlist_groups_collapsed as object))
      if (isBool(v) && k.length > 0 && k.length <= 64 && !k.includes(SEP)) g[k] = v;
    if (Object.keys(g).length) out.watchlist_groups_collapsed = g;
  }
  if (r.drawing_tools && typeof r.drawing_tools === "object") {
    const d = r.drawing_tools as Record<string, unknown>;
    const t: DrawingToolsPref = {};
    for (const k of ["magnet", "stay_in_drawing_mode", "locked", "hidden"] as const) if (isBool(d[k])) t[k] = d[k] as boolean;
    if (Object.keys(t).length) out.drawing_tools = t;
  }
  if (r.chart_views && typeof r.chart_views === "object") {
    const views: Record<string, ChartViewPref> = {};
    for (const [k, v] of Object.entries(r.chart_views as object)) {
      if (!/^[1-9][0-9]*:(1m|5m|15m|1h|4h|1d)$/.test(k) || !v || typeof v !== "object") continue;
      const c = v as Record<string, unknown>;
      if (
        isFiniteNum(c.from_time) && isFiniteNum(c.to_time) && c.from_time > 0 && c.to_time >= c.from_time &&
        isFiniteNum(c.bars_visible) && c.bars_visible > 0 && isFiniteNum(c.bar_spacing) && c.bar_spacing > 0 &&
        isFiniteNum(c.right_offset) && isBool(c.pinned_to_latest)
      ) {
        views[k] = {
          from_time: c.from_time, to_time: c.to_time, bars_visible: c.bars_visible,
          bar_spacing: c.bar_spacing, right_offset: c.right_offset, pinned_to_latest: c.pinned_to_latest,
        };
      }
    }
    if (Object.keys(views).length) out.chart_views = views;
  }
  return out;
}

function readCache(userId: number): SyncState<Prefs> {
  try {
    const raw = window.localStorage.getItem(CACHE_PREFIX + userId);
    if (!raw || raw.length > MAX_LOCAL_BYTES * 2) return { data: {}, baseVersion: 0, dirty: [] };
    const parsed = JSON.parse(raw) as { data?: unknown; baseVersion?: unknown; dirty?: unknown };
    const data = sanitizePrefs(parsed.data);
    const baseVersion = Number.isInteger(parsed.baseVersion) ? (parsed.baseVersion as number) : 0;
    const dirty = Array.isArray(parsed.dirty) ? parsed.dirty.filter((k): k is string => typeof k === "string") : [];
    return { data, baseVersion, dirty };
  } catch {
    return { data: {}, baseVersion: 0, dirty: [] }; // corrupt or blocked storage: start clean
  }
}

function writeCache(userId: number, state: SyncState<Prefs>): void {
  try {
    window.localStorage.setItem(CACHE_PREFIX + userId, JSON.stringify(state));
    window.localStorage.setItem(LAST_USER_KEY, String(userId));
  } catch {
    // Storage full or blocked: the in-memory copy and the server still work.
  }
}

function lastUser(): number | null {
  try {
    const v = Number(window.localStorage.getItem(LAST_USER_KEY));
    return Number.isInteger(v) && v > 0 ? v : null;
  } catch {
    return null;
  }
}

const serverRemote: Remote<Prefs> = {
  async get() {
    const r = await getJSON<{ data: unknown; version: number }>("/me/preferences");
    return { data: sanitizePrefs(r.data), version: r.version };
  },
  async put(data, version, opts) {
    if (opts?.keepalive) {
      const resp = await fetch(`${apiBaseUrl()}/me/preferences`, {
        method: "PUT",
        keepalive: true,
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ data, version }),
      });
      if (!resp.ok) throw new Error(`keepalive PUT ${resp.status}`);
      return { data, version: version + 1 };
    }
    try {
      const r = await putJSON<{ data: unknown; version: number }>("/me/preferences", { data, version });
      return { data: sanitizePrefs(r.data), version: r.version };
    } catch (err) {
      if (err instanceof ApiClientError && err.status === 409) throw new ConflictError(err.message);
      if (err instanceof ApiClientError && (err.status === 400 || err.status === 413)) throw new RejectedError(err.message);
      throw err;
    }
  },
};

// Fixture mode has no server: changes still apply and cache locally.
const localOnlyRemote: Remote<Prefs> = {
  get: () => new Promise(() => {}),
  put: (data, version) => Promise.resolve({ data, version: version + 1 }),
};

type Listener = () => void;

class PreferencesStore {
  private userId: number | null = null;
  private sync: VersionedSync<Prefs> | null = null;
  private snapshot: Prefs = {};
  private listeners = new Set<Listener>();
  // Chosen when a user is attached, not at module load, so fixture mode is
  // honoured whenever it is switched on.
  private remoteFor: () => Remote<Prefs>;

  constructor(remoteFor: () => Remote<Prefs>) {
    this.remoteFor = remoteFor;
    // First paint, before the session is known: the last signed-in user's
    // cached copy, read-only until attach() confirms who is signed in.
    const last = typeof window === "undefined" ? null : lastUser();
    if (last !== null) this.snapshot = readCache(last).data;
  }

  get(): Prefs {
    return this.snapshot;
  }

  subscribe = (l: Listener): (() => void) => {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  };

  // attach starts syncing for userId (on sign-in or session restore): the
  // local cache first, then the server copy, which wins when newer.
  attach(userId: number): void {
    if (this.userId === userId && this.sync) return;
    this.sync?.stop();
    this.userId = userId;
    const initial = readCache(userId);
    this.sync = new VersionedSync(prefsAdapter, this.remoteFor(), initial, {
      debounceMs: PREFS_DEBOUNCE_MS,
      maxBackoffMs: PREFS_MAX_BACKOFF_MS,
      onChange: (state) => {
        writeCache(userId, state);
        this.publish(state.data);
      },
    });
    this.publish(initial.data);
    // Server down or offline: keep working from the cache, quietly.
    void this.sync.pull().catch(() => {});
  }

  // detach stops syncing (sign-out); nothing from this user stays in memory.
  detach(): void {
    this.sync?.flushKeepalive();
    this.sync?.stop();
    this.sync = null;
    this.userId = null;
    this.publish({});
  }

  set<K extends keyof Prefs>(key: K, value: Prefs[K]): void {
    if (JSON.stringify(this.snapshot[key]) === JSON.stringify(value)) return;
    if (this.sync) this.sync.set(key, value);
    else this.publish(prefsAdapter.set(this.snapshot, key, value)); // not signed in yet: memory only
  }

  // setEntry sets one entry of a map-valued key (undefined removes it).
  setEntry<K extends "watchlist_groups_collapsed" | "drawing_tools" | "chart_views">(
    key: K,
    sub: string,
    value: unknown,
  ): void {
    const path = key + SEP + sub;
    if (JSON.stringify(prefsAdapter.get(this.snapshot, path)) === JSON.stringify(value)) return;
    if (this.sync) this.sync.set(path, value);
    else this.publish(prefsAdapter.set(this.snapshot, path, value));
  }

  flushKeepalive(): void {
    this.sync?.flushKeepalive();
  }

  nudge(): void {
    this.sync?.nudge();
  }

  // For tests.
  flush(): Promise<void> {
    return this.sync?.flush() ?? Promise.resolve();
  }

  private publish(data: Prefs): void {
    this.snapshot = data;
    this.listeners.forEach((l) => l());
  }
}

export function createPreferencesStore(remote: Remote<Prefs>): PreferencesStore {
  return new PreferencesStore(() => remote);
}

export const preferences = new PreferencesStore(() => (fixturesEnabled() ? localOnlyRemote : serverRemote));

if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => preferences.flushKeepalive());
  window.addEventListener("online", () => preferences.nudge());
}

export function usePreferences(): Prefs {
  return useSyncExternalStore(preferences.subscribe, () => preferences.get());
}
