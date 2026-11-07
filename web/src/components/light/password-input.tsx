import { forwardRef, useState, type InputHTMLAttributes, type ReactNode } from "react";
import { Eye, EyeSlash } from "@phosphor-icons/react";
import { cn } from "@/lib/cn";
import { Field } from "./field";

const baseInputClass =
  "focus-ring-light h-10 w-full rounded-[10px] border border-light-line bg-light-surface pl-3.5 pr-10 text-sm text-light-text placeholder:text-light-muted transition-colors disabled:pointer-events-none disabled:opacity-50";

export interface PasswordInputProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  hint?: ReactNode;
  error?: string;
  labelAddon?: ReactNode;
}

// Same compact input as TextInput, with a show/hide eye toggle at the
// right edge (the reference screenshot's own layout).
export const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps>(
  ({ label, id, required, hint, error, labelAddon, className, ...props }, ref) => {
    const [visible, setVisible] = useState(false);
    return (
      <Field label={label} id={id!} required={required} hint={hint} error={error} labelAddon={labelAddon}>
        <div className="relative">
          <input
            ref={ref}
            id={id}
            type={visible ? "text" : "password"}
            required={required}
            aria-describedby={error ? `${id}-error` : hint ? `${id}-hint` : undefined}
            aria-invalid={error ? true : undefined}
            className={cn(baseInputClass, error && "border-light-error", className)}
            {...props}
          />
          <button
            type="button"
            onClick={() => setVisible((v) => !v)}
            aria-label={visible ? "Hide password" : "Show password"}
            className="focus-ring-light absolute inset-y-0 right-0 grid w-[52px] place-items-center text-light-muted transition-colors hover:text-light-text"
          >
            {visible ? <EyeSlash className="size-[20px]" /> : <Eye className="size-[20px]" />}
          </button>
        </div>
      </Field>
    );
  },
);
PasswordInput.displayName = "PasswordInput";
