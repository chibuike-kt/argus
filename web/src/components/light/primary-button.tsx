import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export interface PrimaryButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  loading?: boolean;
}

// Full width, 10px radius, fill light-primary, white text -- height
// scaled down from the reference screenshot's own 60px (see
// auth-shell.tsx's own comment). Disabled dims the same solid fill
// (opacity, not a separate lightened colour) -- the terminal's own
// Button component's pattern (components/ui/button.tsx:
// "disabled:opacity-40"), kept consistent here rather than the
// reference screenshot's own pale-fill treatment. loading shows a
// quiet indeterminate sweep instead of a spinner glyph (brief's own
// "Motion and feel" section), purely CSS so prefers-reduced-motion
// (theme.css) turns it off by itself.
export const PrimaryButton = forwardRef<HTMLButtonElement, PrimaryButtonProps>(
  ({ className, loading, disabled, children, ...props }, ref) => (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={cn(
        "focus-ring-light relative h-10 w-full overflow-hidden rounded-[10px] bg-light-primary text-sm font-medium text-white transition-colors disabled:pointer-events-none disabled:opacity-40",
        className,
      )}
      {...props}
    >
      {loading && <span aria-hidden className="light-progress-sweep absolute inset-0" />}
      <span className="relative">{children}</span>
    </button>
  ),
);
PrimaryButton.displayName = "PrimaryButton";
