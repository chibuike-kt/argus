// VersionedSync keeps one server-stored, versioned document in step with
// the local copy the UI reads -- the engine behind preferences and chart
// drawings (owner decision D1, brief-argus-product-overhaul.md Phase 1).
//
// The local copy is always what the UI shows and is never lost: every
// change is applied locally first (and cached by the caller), marked dirty
// per key, and pushed to the server on a short debounce.
//
//   - 409 (someone else wrote first, another tab or device): re-read the
//     server copy, put THIS client's dirty keys back on top of it (last
//     intentional change wins, per key), push again.
//   - 500 / offline: retry with backoff; the local value stays.
//   - 400 / 413 (the server will never accept this document): stop
//     retrying it, drop those local edits and take the server copy, so a
//     bad cached value cannot loop forever.
//   - pagehide: flush once with a keepalive request.
//
// Documents are merged per KEY, not wholesale: an adapter says what the
// keys are (preference paths, drawing ids), so two tabs changing
// different keys never overwrite each other.

export interface SyncAdapter<D> {
  keys(doc: D): string[];
  // undefined = the key is absent.
  get(doc: D, key: string): unknown;
  // Returns a new document with key set (undefined deletes it).
  set(doc: D, key: string, value: unknown): D;
}

export interface Remote<D> {
  get(): Promise<{ data: D; version: number }>;
  put(data: D, version: number, opts?: { keepalive?: boolean }): Promise<{ data: D; version: number }>;
}

// Thrown by Remote.put for a 409.
export class ConflictError extends Error {}
// Thrown by Remote.put for a 400/413: the server will not accept this.
export class RejectedError extends Error {}

export interface SyncState<D> {
  data: D;
  baseVersion: number;
  dirty: string[];
}

export interface SyncOptions<D> {
  debounceMs: number;
  maxBackoffMs: number;
  // Called on every local or remote change, with the full state, so the
  // caller can cache it and notify subscribers.
  onChange: (state: SyncState<D>) => void;
}

export class VersionedSync<D> {
  private data: D;
  private baseVersion: number;
  // key -> edit sequence; a key edited again while a write is in flight
  // stays dirty after that write succeeds.
  private dirty = new Map<string, number>();
  private seq = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private inFlight: Promise<void> | null = null;
  private failures = 0;
  private stopped = false;

  constructor(
    private adapter: SyncAdapter<D>,
    private remote: Remote<D>,
    initial: SyncState<D>,
    private opts: SyncOptions<D>,
  ) {
    this.data = initial.data;
    this.baseVersion = initial.baseVersion;
    for (const k of initial.dirty) this.dirty.set(k, ++this.seq);
    if (this.dirty.size > 0) this.schedule(0);
  }

  get state(): SyncState<D> {
    return { data: this.data, baseVersion: this.baseVersion, dirty: [...this.dirty.keys()] };
  }

  // set records a local change to key and schedules a push.
  set(key: string, value: unknown): void {
    this.data = this.adapter.set(this.data, key, value);
    this.dirty.set(key, ++this.seq);
    this.emit();
    this.schedule(this.opts.debounceMs);
  }

  // replace records a whole-document local change: every key that differs
  // from the current document becomes dirty.
  replace(next: D): void {
    const keys = new Set([...this.adapter.keys(this.data), ...this.adapter.keys(next)]);
    let changed = false;
    for (const k of keys) {
      if (JSON.stringify(this.adapter.get(this.data, k)) !== JSON.stringify(this.adapter.get(next, k))) {
        this.dirty.set(k, ++this.seq);
        changed = true;
      }
    }
    this.data = next;
    if (changed) {
      this.emit();
      this.schedule(this.opts.debounceMs);
    }
  }

  // pull reads the server copy and adopts it if it is newer than what this
  // copy is based on, keeping this client's dirty keys on top.
  async pull(): Promise<void> {
    const remote = await this.remote.get();
    this.adopt(remote);
  }

  private adopt(remote: { data: D; version: number }): void {
    if (remote.version < this.baseVersion) return; // older than ours: ignore
    let merged = remote.data;
    for (const k of this.dirty.keys()) merged = this.adapter.set(merged, k, this.adapter.get(this.data, k));
    this.data = merged;
    this.baseVersion = remote.version;
    this.emit();
    if (this.dirty.size > 0) this.schedule(0);
  }

  // flush pushes dirty keys now (one write at a time).
  flush(): Promise<void> {
    if (this.inFlight) return this.inFlight;
    if (this.dirty.size === 0 || this.stopped) return Promise.resolve();
    this.inFlight = this.push().finally(() => {
      this.inFlight = null;
    });
    return this.inFlight;
  }

  private async push(): Promise<void> {
    for (let attempt = 0; attempt < 5; attempt++) {
      const sent = new Map(this.dirty);
      try {
        const result = await this.remote.put(this.data, this.baseVersion);
        // Keys edited again while this write was in flight stay dirty.
        for (const [k, s] of sent) if (this.dirty.get(k) === s) this.dirty.delete(k);
        let merged = result.data;
        for (const k of this.dirty.keys()) merged = this.adapter.set(merged, k, this.adapter.get(this.data, k));
        this.data = merged;
        this.baseVersion = result.version;
        this.failures = 0;
        this.emit();
        if (this.dirty.size > 0) this.schedule(this.opts.debounceMs);
        return;
      } catch (err) {
        if (err instanceof ConflictError) {
          // Someone else wrote first: take theirs, keep our dirty keys on
          // top, and try again straight away.
          try {
            this.adopt(await this.remote.get());
          } catch {
            this.retryLater();
            return;
          }
          continue;
        }
        if (err instanceof RejectedError) {
          // Never acceptable as is: drop these edits and take the server's.
          for (const k of sent.keys()) this.dirty.delete(k);
          this.baseVersion = -1; // force adoption of whatever the server has
          try {
            this.adopt(await this.remote.get());
          } catch {
            /* stays local; next change retries */
          }
          return;
        }
        this.retryLater();
        return;
      }
    }
    this.retryLater();
  }

  private retryLater(): void {
    this.failures++;
    const delay = Math.min(this.opts.maxBackoffMs, 1000 * 2 ** (this.failures - 1));
    this.schedule(delay);
  }

  // flushKeepalive is the pagehide path: one best-effort write that the
  // browser completes even as the page goes away. No response handling --
  // the next load pulls and reconciles anyway.
  flushKeepalive(): void {
    if (this.dirty.size === 0 || this.stopped) return;
    void this.remote.put(this.data, this.baseVersion, { keepalive: true }).catch(() => {});
  }

  // nudge pushes now if anything is waiting (e.g. the browser came online).
  nudge(): void {
    if (this.dirty.size > 0) this.schedule(0);
  }

  stop(): void {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }

  private schedule(ms: number): void {
    if (this.stopped) return;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.flush();
    }, ms);
  }

  private emit(): void {
    this.opts.onChange(this.state);
  }
}
