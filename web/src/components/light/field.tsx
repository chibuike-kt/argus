import type { ReactNode } from "react";

// Label (14px medium, optional required asterisk in the primary
// colour), then whatever control is passed as children, then an
// optional hint or error line. Scaled down from the reference
// screenshot's own 18px label (see auth-shell.tsx's own comment on
// why only the vertical sizing changed). A low-level layout
// primitive: it does not wire
// aria-describedby onto children itself (it cannot know what children
// is), so TextInput/PasswordInput below -- the common case -- wire it
// themselves onto the <input> they render; a caller using Field
// directly with some other control does the same.
export function Field({
  label,
  id,
  required,
  hint,
  error,
  labelAddon,
  children,
}: {
  label: string;
  id: string;
  required?: boolean;
  hint?: ReactNode;
  error?: string;
  /** Rendered at the row's right edge, alongside the label (e.g. "Forgot password?"). */
  labelAddon?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id} className="text-sm font-medium text-light-text">
          {label}
          {required && (
            <span className="text-light-primary" aria-hidden>
              {" "}
              *
            </span>
          )}
        </label>
        {labelAddon}
      </div>
      {children}
      {error ? (
        <p id={`${id}-error`} className="text-xs text-light-error" role="alert">
          {error}
        </p>
      ) : hint ? (
        <div id={`${id}-hint`} className="text-xs text-light-muted">
          {hint}
        </div>
      ) : null}
    </div>
  );
}
