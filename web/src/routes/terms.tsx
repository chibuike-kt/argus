import { Wordmark } from "@/components/light/wordmark";

// A placeholder, not a drafted legal document (web light auth redesign
// brief, Part 3) -- signup links here so the link is never dead, but
// the real text is out of scope for this brief and needs legal review
// before it is anything more than this.
export function Terms() {
  return (
    <div className="theme-light min-h-dvh">
      <div className="mx-auto max-w-[620px] space-y-6 px-5 py-10">
        <Wordmark />
        <h1 className="font-heading text-2xl font-bold text-light-text">Terms of Service</h1>
        <p className="text-sm text-light-muted">
          This is a placeholder page. ARGUS's actual Terms of Service have not been drafted yet --
          this page exists so the link from signup is never dead, not as a substitute for the real
          document.
        </p>
      </div>
    </div>
  );
}
