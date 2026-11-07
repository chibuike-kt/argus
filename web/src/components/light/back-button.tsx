import { CaretLeft } from "@phosphor-icons/react";

// Small circular back button, fill light-fill, text-coloured chevron
// (scaled down from the reference screenshot's own 42px -- see
// auth-shell.tsx's own comment).
export function BackButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label="Back"
      className="focus-ring-light grid size-8 shrink-0 place-items-center rounded-full bg-light-fill text-light-text transition-colors hover:bg-light-line"
    >
      <CaretLeft weight="bold" className="size-4" />
    </button>
  );
}
