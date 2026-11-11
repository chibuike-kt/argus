import { forwardRef, type InputHTMLAttributes, type ReactNode } from "react";
import { Check } from "@phosphor-icons/react";
import { cn } from "@/lib/cn";

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type"> {
  label: ReactNode;
}

// A square box (border light-line, white fill, checked = light-primary
// fill with a white check) plus a label that can carry inline links
// (the signup terms checkbox: "...agree to ARGUS's Terms and Privacy
// Policy" -- docs/design-refs/signup-email.png).
export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(
  ({ label, id, className, checked, ...props }, ref) => (
    <label htmlFor={id} className="flex cursor-pointer items-start gap-3 text-[14px] text-light-text">
      <span className="relative mt-0.5 inline-flex shrink-0">
        <input
          ref={ref}
          id={id}
          type="checkbox"
          checked={checked}
          className={cn("peer sr-only", className)}
          {...props}
        />
        <span
          aria-hidden
          className={cn(
            "focus-ring-light grid size-4 place-items-center rounded-[5px] border border-light-line bg-light-surface transition-colors",
            "peer-checked:border-light-primary peer-checked:bg-light-primary",
            "peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-light-focus peer-focus-visible:outline-offset-2",
          )}
        >
          {checked && <Check weight="bold" className="size-3 text-white" />}
        </span>
      </span>
      <span>{label}</span>
    </label>
  ),
);
Checkbox.displayName = "Checkbox";
