import type { HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

const pill = cva("inline-flex items-center gap-1.5 rounded-full font-medium leading-none", {
  variants: {
    variant: {
      default: "bg-white/5 text-fg-muted",
      glass: "glass text-fg",
      active: "bg-accent text-accent-foreground",
      outline: "border border-line text-fg-muted",
      up: "bg-up/15 text-up",
      down: "bg-down/15 text-down",
    },
    size: {
      sm: "h-6 px-2.5 text-[11px]",
      md: "h-8 px-3.5 text-[13px]",
    },
  },
  defaultVariants: { variant: "default", size: "sm" },
});

export interface PillProps extends HTMLAttributes<HTMLSpanElement>, VariantProps<typeof pill> {
  dot?: boolean;
}

export function Pill({ className, variant, size, dot, children, ...props }: PillProps) {
  return (
    <span className={cn(pill({ variant, size }), className)} {...props}>
      {dot && <span className="size-1.5 rounded-full bg-current" />}
      {children}
    </span>
  );
}
