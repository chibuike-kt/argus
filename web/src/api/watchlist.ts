import { getJSON, postJSON, putJSON, type GetJSONOptions, type RequestOptions } from "./client";
import type { ApiInstrument, ApiWatchlistItem, ApiWatchlistTracking } from "./types";

// POST /watchlist/items (Phase 4.1a): idempotent; may enable a disabled
// Hyperliquid perpetual outside production (D15.1).
export function addWatchlistItem(instrumentId: number, options?: RequestOptions): Promise<ApiWatchlistItem[]> {
  return postJSON<ApiWatchlistItem[]>("/watchlist/items", { instrument_id: instrumentId }, options);
}

// PUT /watchlist/order: the ids must be exactly the current list (409
// watchlist_changed otherwise).
export function reorderWatchlist(instrumentIds: number[], options?: RequestOptions): Promise<ApiWatchlistItem[]> {
  return putJSON<ApiWatchlistItem[]>("/watchlist/order", { instrument_ids: instrumentIds }, options);
}

export function getWatchlistTracking(options?: GetJSONOptions): Promise<ApiWatchlistTracking> {
  return getJSON<ApiWatchlistTracking>("/watchlist/tracking", undefined, options);
}

// CATALOG_PAGE is the page size of the catalog read (the API's maximum).
const CATALOG_PAGE = 500;
// [U] display value: a guard against an endless page loop.
const CATALOG_MAX_PAGES = 40;

// listHyperliquidCatalog reads every registered Hyperliquid perpetual,
// enabled or not (the worker's catalog refresh registers them disabled),
// paging by id. The search dialog filters it client side.
export async function listHyperliquidCatalog(options?: GetJSONOptions): Promise<ApiInstrument[]> {
  const out: ApiInstrument[] = [];
  let after: number | undefined;
  for (let page = 0; page < CATALOG_MAX_PAGES; page++) {
    const batch = await getJSON<ApiInstrument[]>(
      "/instruments",
      { enabled: "all", limit: CATALOG_PAGE, after_id: after },
      options,
    );
    for (const inst of batch) if (inst.provider === "hyperliquid") out.push(inst);
    if (batch.length < CATALOG_PAGE) break;
    after = batch[batch.length - 1].id;
  }
  return out.sort((a, b) => a.native_symbol.localeCompare(b.native_symbol));
}
