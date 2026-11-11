import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

// Full width, 10px radius, fill light-fill, no border, icon + label
// centred (the reference screenshot's own layout; height scaled down
// from its 60px, see auth-shell.tsx's own comment). icon is the
// provider's own real logo (its natural colours -- Google's 4-colour
// "G", Apple's glyph), not a monochrome recolour.
export function SocialButton({
  icon,
  children,
  disabled,
  soon,
  ...props
}: {
  icon: ReactNode;
  children: ReactNode;
  disabled?: boolean;
  soon?: boolean;
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      disabled={disabled}
      className={cn(
        "focus-ring-light relative flex h-9 w-full items-center justify-center gap-2.5 rounded-[10px] bg-light-fill text-sm font-medium text-light-text transition-colors disabled:cursor-not-allowed disabled:opacity-60",
      )}
      {...props}
    >
      <span className="[&_svg]:size-4">{icon}</span>
      {children}
      {soon && (
        <span className="absolute right-4 rounded-full bg-light-line px-2 py-0.5 text-[11px] font-medium text-light-muted">
          Soon
        </span>
      )}
    </button>
  );
}
