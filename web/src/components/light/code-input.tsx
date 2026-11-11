import { useRef, type ClipboardEvent, type KeyboardEvent } from "react";
import { cn } from "@/lib/cn";

export interface CodeInputProps {
  length?: number;
  value: string;
  onChange: (value: string) => void;
  /** Fires once, the moment the code reaches `length` digits (paste or typed). */
  onComplete?: (value: string) => void;
  error?: boolean;
  disabled?: boolean;
}

// Six square boxes, grouped three-dash-three, dash in light-muted --
// scaled down from the reference screenshot's own ~66px box (see
// auth-shell.tsx's own comment on why only the vertical sizing
// changed). Digits only, numeric keypad, auto-advance,
// backspace moves back, pasting the full code fills every box at
// once, and onComplete fires the instant the last digit lands --
// Enter-to-submit is native <form> behaviour as long as this sits
// inside one, not special-cased here.
export function CodeInput({
  length = 6,
  value,
  onChange,
  onComplete,
  error,
  disabled,
}: CodeInputProps) {
  const refs = useRef<(HTMLInputElement | null)[]>([]);
  const digits = Array.from({ length }, (_, i) => value[i] ?? "");

  function setAt(index: number, char: string) {
    const next = digits.slice();
    next[index] = char;
    const joined = next.join("").slice(0, length);
    onChange(joined);
    // next.includes("") (an empty slot still present in the per-box
    // array) is the right check -- joined.includes("") was a bug: the
    // empty string is a substring of every string, so that check was
    // always true and onComplete could never fire (caught by the web
    // light auth redesign brief's own Part 3 integration test, the
    // first thing to actually exercise auto-submit-on-complete end to
    // end).
    if (joined.length === length && !next.includes("")) {
      onComplete?.(joined);
    }
  }

  function handleChange(index: number, raw: string) {
    const digit = raw.replace(/\D/g, "").slice(-1);
    setAt(index, digit);
    if (digit && index < length - 1) {
      refs.current[index + 1]?.focus();
    }
  }

  function handleKeyDown(index: number, e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Backspace" && !digits[index] && index > 0) {
      refs.current[index - 1]?.focus();
      setAt(index - 1, "");
    } else if (e.key === "ArrowLeft" && index > 0) {
      refs.current[index - 1]?.focus();
    } else if (e.key === "ArrowRight" && index < length - 1) {
      refs.current[index + 1]?.focus();
    }
  }

  function handlePaste(e: ClipboardEvent<HTMLInputElement>) {
    const pasted = e.clipboardData.getData("text").replace(/\D/g, "").slice(0, length);
    if (!pasted) return;
    e.preventDefault();
    onChange(pasted);
    if (pasted.length === length) {
      onComplete?.(pasted);
      refs.current[length - 1]?.focus();
    } else {
      refs.current[pasted.length]?.focus();
    }
  }

  return (
    <div className="flex items-center gap-2" role="group" aria-label="Verification code">
      {digits.map((digit, i) => (
        <span key={i} className="flex items-center gap-2">
          {i === Math.ceil(length / 2) && (
            <span aria-hidden className="px-1 text-light-muted">
              –
            </span>
          )}
          <input
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="text"
            inputMode="numeric"
            autoComplete={i === 0 ? "one-time-code" : "off"}
            maxLength={1}
            disabled={disabled}
            value={digit}
            aria-label={`Digit ${i + 1} of ${length}`}
            aria-invalid={error ? true : undefined}
            onChange={(e) => handleChange(i, e.target.value)}
            onKeyDown={(e) => handleKeyDown(i, e)}
            onPaste={handlePaste}
            className={cn(
              "focus-ring-light size-9 rounded-[10px] border border-light-line bg-light-surface text-center text-lg font-medium text-light-text transition-colors",
              error && "border-light-error",
            )}
          />
        </span>
      ))}
    </div>
  );
}
