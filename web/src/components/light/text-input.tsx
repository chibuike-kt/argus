import { forwardRef, type InputHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { Field } from "./field";

// Scaled down from the reference screenshot's own 60px/18px-padding
// (see auth-shell.tsx's own comment) to a more conventional compact
// form-field size; radius and border colour otherwise follow the
// reference.
const baseInputClass =
  "focus-ring-light h-10 w-full rounded-[10px] border border-light-line bg-light-surface px-3 text-sm text-light-text placeholder:text-light-muted transition-colors disabled:pointer-events-none disabled:opacity-50";

export interface TextInputProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  hint?: ReactNode;
  error?: string;
  labelAddon?: ReactNode;
}

export const TextInput = forwardRef<HTMLInputElement, TextInputProps>(
  ({ label, id, required, hint, error, labelAddon, className, ...props }, ref) => (
    <Field label={label} id={id!} required={required} hint={hint} error={error} labelAddon={labelAddon}>
      <input
        ref={ref}
        id={id}
        required={required}
        aria-describedby={error ? `${id}-error` : hint ? `${id}-hint` : undefined}
        aria-invalid={error ? true : undefined}
        className={cn(baseInputClass, error && "border-light-error", className)}
        {...props}
      />
    </Field>
  ),
);
TextInput.displayName = "TextInput";
