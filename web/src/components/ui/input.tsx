import { forwardRef, type InputHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

const input = cva(
  "glass focus-ring w-full rounded-lg px-3.5 py-2.5 text-sm text-fg placeholder:text-fg-subtle transition-colors disabled:pointer-events-none disabled:opacity-40",
  {
    variants: {
      // prov-u (an existing provenance token, not a new colour -- see
      // the Part A report) marks a field the server rejected.
      invalid: {
        true: "border-prov-u/60",
        false: "",
      },
    },
    defaultVariants: { invalid: false },
  },
);

export interface InputProps
  extends InputHTMLAttributes<HTMLInputElement>, VariantProps<typeof input> {}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, invalid, ...props }, ref) => (
    <input ref={ref} className={cn(input({ invalid }), className)} {...props} />
  ),
);
Input.displayName = "Input";
