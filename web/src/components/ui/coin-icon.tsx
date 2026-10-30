import { useState } from "react";
import { cn } from "@/lib/cn";

// Resolve an instrument symbol to its coin ticker.
// BTCUSDT.P -> btc, ETHUSDT.P -> eth, SOLUSD -> sol. Forex/unknown -> "".
export function baseTicker(symbol: string): string {
  let s = symbol.toUpperCase();
  s = s.replace(/\.P$/, "").replace(/PERP$/, "");
  s = s.replace(/[^A-Z]/g, "");
  for (const q of ["USDT", "USDC", "USD"]) {
    if (s.length > q.length && s.endsWith(q)) {
      s = s.slice(0, -q.length);
      break;
    }
  }
  return s.toLowerCase();
}

// Renders the coin logo from public/coins/<ticker>.svg (cryptocurrency-icons,
// CC0). Falls back to a lettered token for forex pairs or any missing icon.
export function CoinIcon({
  symbol,
  size = 24,
  className,
}: {
  symbol: string;
  size?: number;
  className?: string;
}) {
  const [failed, setFailed] = useState(false);
  const ticker = baseTicker(symbol);

  if (failed || !ticker) {
    const letters = (ticker || symbol).slice(0, 2).toUpperCase();
    return (
      <span
        className={cn(
          "inline-grid shrink-0 place-items-center rounded-full border border-line bg-surface-2 font-semibold text-fg-muted",
          className,
        )}
        style={{ width: size, height: size, fontSize: Math.round(size * 0.38) }}
      >
        {letters}
      </span>
    );
  }

  return (
    <img
      src={`/coins/${ticker}.svg`}
      width={size}
      height={size}
      alt={ticker.toUpperCase()}
      loading="lazy"
      className={cn("shrink-0 rounded-full", className)}
      onError={() => setFailed(true)}
    />
  );
}
