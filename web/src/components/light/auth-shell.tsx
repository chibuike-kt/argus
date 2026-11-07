import type { ReactNode } from "react";
import { BackButton } from "./back-button";
import { SupportButton } from "./support-button";
import { StepProgress } from "./step-progress";
import { Wordmark } from "./wordmark";

// Shared page chrome for every pre-sign-in page (web light auth
// redesign brief, Parts 1c/3): the .theme-light scope wrapper, the
// ARGUS wordmark in the page margin (outside the centred column on
// wide screens, above it on narrow ones), a compact centred column
// (own 620px scale -- the reference screenshot's own 960px/816px
// numbers read as oversized at the browser widths this app is
// actually used at, flagged and reduced on direct product feedback,
// same as the vertical sizing -- input/button height, heading size,
// spacing -- scaled down in each component below), the top row (back
// button, optional step progress, support button) pushed down below
// the fixed wordmark's own row on wide screens so the two never read
// as one line, and a footer area that stays pinned at the bottom of
// the viewport so a short step (verify) does not look sparse while a
// long one (sign-in) is unaffected.
export function AuthShell({
  onBack,
  step,
  totalSteps,
  footer,
  children,
}: {
  onBack?: () => void;
  step?: number;
  totalSteps?: number;
  footer?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="theme-light flex min-h-dvh flex-col">
      {/* Narrow screens: wordmark sits above the column. */}
      <div className="px-5 pt-6 sm:hidden">
        <Wordmark />
      </div>

      {/* Wide screens: wordmark sits in the page margin, outside the
          column, fixed so it stays put while the column's own content
          scrolls. */}
      <div className="fixed left-8 top-8 hidden sm:block">
        <Wordmark />
      </div>

      <div className="mx-auto flex w-full max-w-[620px] flex-1 flex-col px-5 pt-2 sm:pt-16">
        <div className="flex items-center justify-between py-3">
          {onBack ? <BackButton onClick={onBack} /> : <span className="size-8" />}
          {step !== undefined && totalSteps !== undefined && (
            <StepProgress current={step} total={totalSteps} />
          )}
          <SupportButton />
        </div>

        <div className="light-step-in flex-1">{children}</div>

        {footer && <div className="pb-5 pt-4">{footer}</div>}
      </div>
    </div>
  );
}
