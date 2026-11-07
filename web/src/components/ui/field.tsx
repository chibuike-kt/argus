import type { ReactNode } from "react";

// A labelled form field with an optional hint (helper text, shown when
// there is no error) or error line underneath. error uses the prov-u
// token -- see the Part A report for why, since red elsewhere in this
// app means "the market went down", never a generic UI state.
export function Field({
  label,
  id,
  hint,
  error,
  children,
}: {
  label: string;
  id: string;
  hint?: ReactNode;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="block text-[12px] font-medium text-fg-muted">
        {label}
      </label>
      {children}
      {error ? (
        <p className="text-[12px] text-prov-u" role="alert">
          {error}
        </p>
      ) : hint ? (
        <div className="text-[12px] text-fg-subtle">{hint}</div>
      ) : null}
    </div>
  );
}
