# Contributing to ARGUS

## Branches and commits

- Branch off `main`. Use short, descriptive branch names.
- Use Conventional Commits, for example `feat: add instrument registry` or
  `fix(marketdata): handle missing candle gaps`.

## Before you push

Run the full local check:

```bash
make check   # fmt, vet, lint, test
```

For database changes:

- Migrations are expand-contract by default and run as a dedicated step (ADR-020).
- Add both `.up.sql` and `.down.sql`. Prefer a forward fix over an unsafe reverse.
- Regenerate query code with `make generate` after schema or query changes.

## Pull requests

- Keep PRs small and reviewable.
- Link the ADR or PRD requirement the change relates to.
- CI must pass: backend lint/test/build/vuln, frontend lint/typecheck/build,
  migration up and down, CodeQL and dependency review.

## Decisions

The ADRs in `docs/adr` are the source of truth. If a change needs to deviate
from an ADR, say so in the PR and, when the decision is material, update or add
an ADR rather than drifting silently.
