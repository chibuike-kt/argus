import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

const button = cva(
  "focus-ring inline-flex select-none items-center justify-center gap-2 whitespace-nowrap rounded-full font-medium transition-colors disabled:pointer-events-none disabled:opacity-40",
  {
    variants: {
      variant: {
        primary:
          "bg-accent text-accent-foreground hover:bg-accent-strong active:bg-accent-strong",
        glass: "glass text-fg hover:border-line-strong",
        outline: "border border-line text-fg hover:bg-white/5",
        ghost: "text-fg-muted hover:bg-white/5 hover:text-fg",
      },
      size: {
        sm: "h-8 px-3 text-[13px]",
        md: "h-10 px-4 text-sm",
        lg: "h-12 px-6 text-base",
      },
    },
    defaultVariants: { variant: "primary", size: "md" },
  },
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof button> {}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(button({ variant, size }), className)} {...props} />
  ),
);
Button.displayName = "Button";
