import { describe, expect, it } from "vitest";
import {
  checkSnapshotGap,
  decideApplyAction,
  INTERVAL_MS,
  isNewerCandleKey,
  type CandleKey,
} from "../live-candles-reducer";

describe("isNewerCandleKey (rule 5: compare open_time, revision, seq)", () => {
  const applied: CandleKey = { openTime: "2026-01-01T00:05:00Z", revision: 2, seq: 10 };

  it("a strictly later open_time is newer, regardless of revision/seq", () => {
    expect(isNewerCandleKey({ openTime: "2026-01-01T00:06:00Z", revision: 1, seq: 1 }, applied)).toBe(true);
  });

  it("the same open_time, same revision, but a lower seq is not newer (duplicate/out of order)", () => {
    expect(isNewerCandleKey({ openTime: applied.openTime, revision: 2, seq: 5 }, applied)).toBe(false);
  });

  it("the same open_time, same revision, a higher seq is newer (a forming-candle progression)", () => {
    expect(isNewerCandleKey({ openTime: applied.openTime, revision: 2, seq: 11 }, applied)).toBe(true);
  });

  it("the same open_time with a higher revision is newer regardless of seq (a correction)", () => {
    expect(isNewerCandleKey({ openTime: applied.openTime, revision: 3, seq: 1 }, applied)).toBe(true);
  });

  it("an earlier open_time is never newer even with a higher revision/seq", () => {
    expect(isNewerCandleKey({ openTime: "2026-01-01T00:04:00Z", revision: 99, seq: 99 }, applied)).toBe(false);
  });

  it("anything is newer than nothing applied yet", () => {
    expect(isNewerCandleKey({ openTime: "2026-01-01T00:00:00Z", revision: 1, seq: 1 }, null)).toBe(true);
  });
});

describe("checkSnapshotGap (review item 6)", () => {
  it("no gap when the snapshot's join point equals the newest held bar", () => {
    const result = checkSnapshotGap(
      { last_closed_open_time: "2026-01-01T00:05:00Z", last_closed_revision: 1 },
      "2026-01-01T00:05:00Z",
      INTERVAL_MS["1m"],
    );
    expect(result.hasGap).toBe(false);
  });

  it("a gap when the snapshot's join point is later than the newest held bar -- a candle closed between the REST fetch and the subscribe", () => {
    const result = checkSnapshotGap(
      { last_closed_open_time: "2026-01-01T00:08:00Z", last_closed_revision: 1 },
      "2026-01-01T00:05:00Z",
      INTERVAL_MS["1m"],
    );
    expect(result.hasGap).toBe(true);
    expect(result.refetchFrom).toBe("2026-01-01T00:06:00.000Z");
    expect(result.refetchTo).toBe("2026-01-01T00:09:00.000Z"); // exclusive, so the 00:08 bar is included
  });

  it("no gap when the snapshot's own join point is null (the instrument has no closed candle yet)", () => {
    const result = checkSnapshotGap({ last_closed_open_time: null, last_closed_revision: null }, "2026-01-01T00:05:00Z", INTERVAL_MS["1m"]);
    expect(result.hasGap).toBe(false);
  });

  it("no gap when nothing was held yet (an empty-history instrument) -- there is nothing to compare the join point against", () => {
    const result = checkSnapshotGap({ last_closed_open_time: "2026-01-01T00:05:00Z", last_closed_revision: 1 }, null, INTERVAL_MS["1m"]);
    expect(result.hasGap).toBe(false);
  });

  it("scales the refetch window to the interval, not always one minute", () => {
    const result = checkSnapshotGap(
      { last_closed_open_time: "2026-01-01T02:00:00Z", last_closed_revision: 1 },
      "2026-01-01T01:00:00Z",
      INTERVAL_MS["1h"],
    );
    expect(result.hasGap).toBe(true);
    expect(result.refetchFrom).toBe("2026-01-01T02:00:00.000Z");
    expect(result.refetchTo).toBe("2026-01-01T03:00:00.000Z");
  });
});

describe("decideApplyAction (lightweight-charts 5.2.1's own update() contract)", () => {
  it("the same open_time as the current last bar: replace", () => {
    expect(decideApplyAction({ open_time: "t5" } as never, "t5")).toBe("replace");
  });

  it("a strictly newer open_time: append", () => {
    expect(decideApplyAction({ open_time: "t6" } as never, "t5")).toBe("append");
  });

  it("an older open_time: drop-stale, never passed to series.update() (which would throw per the contract test)", () => {
    expect(decideApplyAction({ open_time: "t4" } as never, "t5")).toBe("drop-stale");
  });

  it("nothing applied yet: append", () => {
    expect(decideApplyAction({ open_time: "t1" } as never, null)).toBe("append");
  });
});
