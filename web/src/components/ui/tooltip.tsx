import type { ReactNode } from "react";
import * as T from "@radix-ui/react-tooltip";

export const TooltipProvider = T.Provider;

export function Tooltip({
  content,
  children,
  side = "top",
}: {
  content: ReactNode;
  children: ReactNode;
  side?: "top" | "right" | "bottom" | "left";
}) {
  return (
    <T.Root>
      <T.Trigger asChild>{children}</T.Trigger>
      <T.Portal>
        <T.Content
          side={side}
          sideOffset={6}
          className="glass-strong z-50 max-w-xs rounded-md px-2.5 py-1.5 text-xs leading-snug text-fg"
        >
          {content}
          <T.Arrow className="fill-white/15" />
        </T.Content>
      </T.Portal>
    </T.Root>
  );
}
