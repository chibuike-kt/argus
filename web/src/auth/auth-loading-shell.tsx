import { CircleNotch } from "@phosphor-icons/react";

// Shown while auth status is still "loading" (the boot GET /auth/me
// has not resolved yet) -- a route guard renders this instead of
// flashing the login page or the protected shell first and then
// swapping (web auth UI brief, Part A2).
export function AuthLoadingShell() {
  return (
    <div className="grid h-full place-items-center">
      <CircleNotch className="size-5 animate-spin text-fg-subtle" aria-label="Loading" />
    </div>
  );
}
