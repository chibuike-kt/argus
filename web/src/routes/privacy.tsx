import { Wordmark } from "@/components/light/wordmark";

// A placeholder, not a drafted privacy document (web light auth
// redesign brief, Part 3) -- same reasoning as terms.tsx.
export function Privacy() {
  return (
    <div className="theme-light min-h-dvh">
      <div className="mx-auto max-w-[620px] space-y-6 px-5 py-10">
        <Wordmark />
        <h1 className="font-heading text-2xl font-bold text-light-text">Privacy Policy</h1>
        <p className="text-sm text-light-muted">
          This is a placeholder page. ARGUS's actual Privacy Policy has not been drafted yet. This
          page exists so the link from signup is never dead, not as a substitute for the real
          document.
        </p>
      </div>
    </div>
  );
}
