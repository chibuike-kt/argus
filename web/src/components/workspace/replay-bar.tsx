import * as Slider from "@radix-ui/react-slider";
import { Clock, SkipBack, Play, SkipForward, Radio } from "lucide-react";
import { IconButton } from "@/components/ui/icon-button";

// The as-of time machine, framed as TradingView's Replay: step through history
// and read exactly what was known at each point in time.
export function ReplayBar() {
  return (
    <div className="glass flex items-center gap-4 rounded-xl px-4 py-2">
      <div className="flex items-center gap-2">
        <Clock className="size-4 text-accent" />
        <div className="flex flex-col leading-none">
          <span className="text-[9px] font-medium uppercase tracking-wider text-fg-subtle">
            As of / Replay
          </span>
          <span className="tnum mt-0.5 text-xs font-medium">29 Oct 2026, 14:40 UTC</span>
        </div>
      </div>

      <div className="flex items-center gap-1">
        <IconButton variant="ghost" size="sm" aria-label="Step back">
          <SkipBack />
        </IconButton>
        <IconButton variant="glass" size="sm" aria-label="Play">
          <Play />
        </IconButton>
        <IconButton variant="ghost" size="sm" aria-label="Step forward">
          <SkipForward />
        </IconButton>
      </div>

      <Slider.Root
        className="relative flex h-5 flex-1 touch-none select-none items-center"
        defaultValue={[88]}
        max={100}
        step={1}
        aria-label="As-of time"
      >
        <Slider.Track className="relative h-1 w-full grow rounded-full bg-white/10">
          <Slider.Range className="absolute h-full rounded-full bg-accent" />
        </Slider.Track>
        <Slider.Thumb className="focus-ring block size-3.5 rounded-full bg-accent shadow-[0_0_0_4px_rgba(215,254,98,0.2)]" />
      </Slider.Root>

      <button className="focus-ring inline-flex items-center gap-1.5 rounded-full bg-up/15 px-3 py-1 text-[12px] font-medium text-up">
        <Radio className="size-3.5" /> Live
      </button>
    </div>
  );
}
