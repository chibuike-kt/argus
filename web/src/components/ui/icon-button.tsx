import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

const iconButton = cva(
  "focus-ring inline-flex shrink-0 items-center justify-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-40",
  {
    variants: {
      variant: {
        glass: "glass text-fg hover:border-line-strong",
        solid: "bg-accent text-accent-foreground hover:bg-accent-strong",
        ghost: "text-fg-muted hover:bg-white/5 hover:text-fg",
      },
      size: {
        sm: "size-8 [&_svg]:size-4",
        md: "size-10 [&_svg]:size-[18px]",
        lg: "size-12 [&_svg]:size-5",
      },
    },
    defaultVariants: { variant: "glass", size: "md" },
  },
);

export interface IconButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof iconButton> {}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(iconButton({ variant, size }), className)} {...props} />
  ),
);
IconButton.displayName = "IconButton";
