import { forwardRef, type HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

const panel = cva("rounded-xl", {
  variants: {
    variant: {
      glass: "glass",
      strong: "glass-strong",
      accent: "glass-accent",
      solid: "border border-line bg-surface",
      lime: "bg-accent text-accent-foreground",
    },
    pad: {
      none: "",
      sm: "p-3",
      md: "p-4",
      lg: "p-6",
    },
  },
  defaultVariants: { variant: "glass", pad: "md" },
});

export interface PanelProps extends HTMLAttributes<HTMLDivElement>, VariantProps<typeof panel> {}

export const Panel = forwardRef<HTMLDivElement, PanelProps>(
  ({ className, variant, pad, ...props }, ref) => (
    <div ref={ref} className={cn(panel({ variant, pad }), className)} {...props} />
  ),
);
Panel.displayName = "Panel";
