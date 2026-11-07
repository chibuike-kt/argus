import { Headset } from "@phosphor-icons/react";

// Renders only when VITE_SUPPORT_URL is set (no support channel exists
// yet -- web light auth redesign brief, Part 1c: "Do not invent a
// support address"); otherwise renders a same-size invisible spacer so
// the top row's layout (back button / progress bar / this) never
// shifts depending on whether it's there.
export function SupportButton() {
  const url = import.meta.env.VITE_SUPPORT_URL;
  if (!url) return <div aria-hidden className="size-8 shrink-0" />;
  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      aria-label="Support"
      className="focus-ring-light grid size-8 shrink-0 place-items-center rounded-full bg-light-fill text-light-text transition-colors hover:bg-light-line"
    >
      <Headset weight="regular" className="size-4" />
    </a>
  );
}
