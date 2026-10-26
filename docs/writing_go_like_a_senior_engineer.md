# Writing Go Like a Senior Engineer

Use this guide when writing, changing, or reviewing Go code. The target is code a senior engineer would be glad to inherit: boring, explicit, strongly typed, easy to trace, and structured where the problem has structure. It is not about writing the least code. It is about writing the right amount.

These rules are defaults, not dogma. A consistent existing codebase convention wins, and exceptions are reasonable when the problem genuinely calls for them.

## 1. The One Rule

Validate untrusted data at the boundary. Use concrete types everywhere else.

Boundaries are HTTP requests, queue messages, webhooks, config and environment variables, external APIs, and schemaless documents. At a boundary, decode once into a struct, validate once, and convert to the domain type if the shapes differ. After that, the data is trusted and typed. No `map[string]any` travelling inward, no type assertions rediscovering what the data is, and no rechecking the same fields in every layer.

```go
type DomainMapEvent struct {
    ProjectID string `json:"project_id"`
    Domain    string `json:"domain"`
}

func (s *Service) MapDomain(ctx context.Context, e DomainMapEvent) error {
    domain := strings.ToLower(strings.TrimSpace(e.Domain))
    return s.domains.Map(ctx, e.ProjectID, domain)
}
```

A boundary type may differ from the internal type when the contract differs. A PATCH body needs pointer fields when absent and zero must be distinguished. That is a real reason. Copying the same struct into five layers is not.

## 2. Types

- Structs over maps. If the shape is known, it has a struct.
- Required fields are plain values. Pointers mean real optionality, or shared identity and mutation.
- Make the zero value useful where possible.
- Named types for values that get mixed up (`type ProjectID string`), not for every field.
- `any` only where the type is truly open. `json.RawMessage` is usually the honest choice for deferred JSON decoding.
- Keep units and semantics visible in names and types. `Timeout time.Duration` is clearer than `Timeout int`; `EventTime` is clearer than an ambiguous `Time`.
- A type alone does not validate its value. Enforce invariants at the boundary or in domain constructors when invalid states must not be representable.

## 3. Errors

- Return errors. Never turn a failure into a zero value, `nil`, or an empty result.
- Wrap when context helps a reader: the operation and identifier. `load project 42: %w`, not four layers of "failed to".
- Use sentinel errors with `errors.Is`. Use `errors.As` for typed errors. Never match on `err.Error()`.
- A custom error type is justified when a caller branches on its fields.
- Log once, at the layer that handles the error. Lower layers return it.
- Ignoring an error is a decision. If you do it, explain why in a comment.
- Queue consumers need a policy: invalid message (drop or dead-letter) versus transient failure (retry). Returning an error alone is not a complete policy.
- Preserve the original error when adding context. Avoid repeated wrapping that adds no useful information.
- Avoid panics for ordinary, recoverable failures.

## 4. Structure

- Organise packages by capability, not by kind. No `utils`, `common`, or `helpers`.
- A handler parses input and writes the response. A service holds business rules. A store talks to the database. A layer exists only if it does something; a layer that only forwards does not exist.
- Concrete types by default. An interface is defined by the consumer, kept small, and earns its place with a real boundary to fake in tests or a second implementation. Accept interfaces, return structs.
- Constructors take required dependencies as arguments. Functional options are for genuinely optional config. No builders, factories, or DI containers without a concrete need. Wiring is explicit in `main`.
- No mutable package-level state.
- Business rules stay visible in code (`if p.Status != StatusActive { return ErrInactive }`), not hidden behind vague calls such as `validator.IsValid(p)`.
- Repeat once, extract on the third time or as soon as the logic has a meaningful name. A helper should express a real concept, not hide one line.
- Split long functions into named steps when those steps represent meaningful phases.

## 5. Too Little Structure Is Also a Failure

Boring does not mean flat. These are as wrong as overengineering:

- SQL, business rules, and response formatting all inside one handler.
- Functions of 200 lines with no names for the steps in them.
- The same logic pasted in three places.
- Package-level globals used as shortcuts for dependencies.
- Interfaces and layers that merely forward calls without adding a boundary or policy.

The rule: repeat once, extract on the third time or as soon as the logic has a name worth giving it.

## 6. Functions and Control Flow

- Prefer early returns. Keep the happy path at the left edge.
- Pass what the function needs, not an entire config object.
- Prefer `switch` over dispatch maps unless registration is genuinely dynamic.
- Use short, specific names. Comments explain why, not what.
- Named returns only when a `defer` needs them or they materially clarify the function.
- Keep functions cohesive and control flow explicit.

## 7. Context and Concurrency

- `ctx` is the first parameter, never nil, never stored in a struct, and passed through every I/O call. No `context.Background()` in the middle of a request. It is appropriate at application entry points and tests when no parent context exists.
- Timeouts live at meaningful boundaries, not inside every function.
- A goroutine needs a reason and an owner who handles cancellation, errors, and shutdown.
- Use `errgroup` for fan-out when its cancellation and error semantics fit.
- Channels coordinate work. They are not a way to call a function.
- Real shared state gets a mutex or a clear single-owner model.
- Run tests with `-race`. A passing race test is useful evidence, not proof of concurrency correctness.
- Define channel ownership and closure rules. Never close a channel while senders may still be active.
- Bound concurrency and in-flight work when input can grow without limit.
- Define backpressure behaviour. Do not silently discard work unless dropping is explicit in the contract.
- Every blocking send, receive, and wait needs a plausible completion or cancellation path.
- `net/http` recovers panics per request. Do not use `recover()` in business code as a substitute for correct error handling. Recovery at a process or worker boundary needs an explicit policy.

## 8. Data Access

- Plain SQL in `const` strings. Decode rows into structs.
- Use parameterized queries. Never concatenate untrusted values into SQL.
- Transactions: `BeginTx`, `defer tx.Rollback()`, `Commit`. If the pattern repeats, one small `withTx` helper may be justified.
- `defer rows.Close()` and check `rows.Err()`. Close response bodies.
- One repository/store layer, not a stack of repositories.
- Define transaction boundaries around business invariants.
- Use database constraints for invariants that must survive concurrent requests.
- Set meaningful query and transaction deadlines. Avoid holding transactions open during unrelated network calls.
- Make pagination, ordering, isolation, and locking explicit when they affect correctness.
- Treat migrations as production code and test their application.

## 9. Performance

Write the simple version, measure, then optimise the measured problem.

- Benchmark representative workloads before optimising a hot path.
- Preallocate when size is known and it improves clarity or measured performance.
- No `sync.Pool`, custom allocators, goroutine pools, or clever lock-free designs without evidence from profiling or benchmarks.
- Copy a slice or map when ownership requires it, such as returning internal mutable state from an exported method.
- Measure allocations, throughput, and latency. Inspect tail latency for latency-sensitive paths.
- Use CPU, heap, block, mutex, and execution-trace profiles to find bottlenecks.
- Benchmark realistic data sizes and concurrency levels.
- Define performance budgets for important paths and investigate material regressions.
- Do not sacrifice correctness or maintainability for unmeasured micro-optimisation.

### JSON Detail

`var items []T` encodes as `null`; `items := []T{}` encodes as `[]`. Choose intentionally for API response contracts.

## 10. Tests

- Table-driven tests. Names describe behaviour.
- Test real behaviour first. Handwritten fakes at external boundaries. No mock frameworks for pure logic.
- A bug fix starts with a failing test when practical.
- `go test -race ./...` is a default validation command.
- Test failure paths, cancellation, timeouts, retries, malformed inputs, duplicate events, and recovery.
- Use integration tests for database constraints and transaction semantics that fakes cannot faithfully verify.
- Fuzz parsers and boundary validation where malformed input can reveal bugs.
- Benchmark performance-critical code.
- Control time, randomness, and external inputs when deterministic results are required. Do not use sleeps when synchronization or a controllable clock is available.
- Test invariants and observable behaviour, not private implementation details.
- CI should test supported Go versions and platforms where relevant.

## 11. Standard Library and Dependencies

- Check the Go version in `go.mod`, then use standard-library features it provides: `slices`, `maps`, `cmp`, `errors`, `strings`, and `strconv`, where appropriate.
- Do not rewrite standard-library functionality without a concrete reason.
- Few dependencies, each with a reason.
- Pin or otherwise control CI tool versions, including linters.
- Review dependency updates for compatibility, security, and behaviour changes.

## 12. Boundaries, Configuration, and Security

- Decode untrusted input into a concrete boundary type, validate it, then convert to the domain type when contracts differ.
- Parse and validate configuration once at startup. Fail fast on invalid required configuration.
- Do not log secrets, authentication tokens, private keys, or unnecessary personal data. Redact sensitive values at the logging boundary.
- Use least-privilege credentials and explicit authorization checks. Authentication does not imply authorization.
- Treat external-service data as untrusted, even when the provider is reputable.
- Use parameterized SQL and safe file and outbound-request handling.
- Never silently disable verification or fall back to permissive behaviour after an error.
- Version external contracts deliberately. JSON shapes, enum values, error responses, and event schemas can be compatibility contracts.
- Do not treat a client-supplied tenant or owner ID as proof of authorization.

## 13. Financial and Numerical Correctness

Financial software can be cleanly written and still be wrong because of precision, rounding, units, or time semantics.

- Do not use `float64` for monetary amounts or exact financial accounting. Use integer units or a suitable decimal representation where required. Floating point may be appropriate for statistical calculations when its limitations are understood.
- Define price precision, quantity precision, monetary precision, tick size, and quantity step size separately.
- Specify rounding rules for fees, risk, position sizing, and order quantities.
- Validate numeric boundary inputs for invalid ranges, overflow, NaN, and positive or negative infinity where applicable.
- Make units visible in names and types. Do not mix percentages, basis points, prices, quantities, and monetary values as interchangeable bare numbers.
- Distinguish exchange event time, ingestion time, processing time, and evaluation time. Use UTC internally and define serialization rules.
- Inject a clock or pass explicit time when time affects business logic or tests.
- Make calculations reproducible when required. Avoid hidden dependence on wall-clock time, mutable global state, or uncontrolled randomness.
- Do not treat floating-point equality as exact equality unless the domain and calculation make that valid.
- An `int64` alias alone does not define price precision. Document scale, tick rules, and serialization.

Illustrative example only; define the scale and tick-size rules before adopting this representation:

```go
type Price int64

type Candle struct {
    Symbol    string
    Open      Price
    High      Price
    Low       Price
    Close     Price
    OpenTime  time.Time
    CloseTime time.Time
    IsClosed  bool
}
```

Different instruments may require different precision. Statistical calculations may legitimately use floating point.

## 14. Reliability, Retries, and Recovery

- Give every external operation a timeout, cancellation, or deadline policy.
- Classify errors before retrying. Do not retry permanent validation or authorization failures as transient errors.
- Bound retries by attempts or deadline. Use exponential backoff with jitter where appropriate.
- Do not blindly retry non-idempotent operations. Define idempotency keys or deduplication where repeated delivery is possible.
- Queue consumers distinguish permanent bad messages from transient failures and define drop, quarantine, or dead-letter behaviour.
- Define behaviour for provider outages, rate limiting, partial failures, reconnects, and stale data.
- Reconnecting does not prove missed events have been recovered. Define and test backfill and reconciliation.
- Define graceful shutdown: stop accepting work, cancel or drain workers according to policy, persist required state, and exit within a deadline.
- Bound queues and define overflow behaviour. An unbounded queue turns overload into delayed failure and memory exhaustion.
- For external side effects, distinguish “request sent,” “result confirmed,” and “result unknown.” A timeout does not prove the remote operation failed.

## 15. Observability and Operations

- Use structured logs with stable, useful fields. The standard library's `log/slog` is a reasonable default.
- Measure throughput, latency, errors, retries, queue depth, resource use, and dependency health.
- Use distributed tracing when work crosses meaningful service or asynchronous boundaries.
- Distinguish liveness from readiness. A process can be alive but not safe to serve traffic or make decisions.
- Separate expected business outcomes, such as a strategy rejecting a setup, from system failures.
- Make background worker health, progress, and shutdown status visible.
- Redact secrets and sensitive values before exporting telemetry.
- Define service-level objectives and alert on user-impacting symptoms.
- Use correlation identifiers consistently where useful, without high-cardinality identifiers as metric labels.
- Document how to diagnose common incidents and verify recovery.

## 16. Autonomous and Market-Data Systems

These rules apply specifically to ARGUS and other systems that continuously evaluate changing external data.

- Every significant strategy evaluation identifies the input data, strategy version, evaluation time, and relevant configuration used.
- Market data has explicit freshness, finality, validity, and provenance semantics. Never silently treat stale, incomplete, or invalid data as current and complete.
- Define duplicate handling, ordering requirements, gap detection, and recovery for event streams.
- The same strategy version and equivalent ordered inputs must produce reproducible results whenever the strategy is specified as deterministic.
- Live evaluation and historical replay share the same core strategy logic. Differences in data availability, timing, fees, and execution assumptions remain explicit.
- Do not use future information in historical evaluation. Test for lookahead bias and account for selection or survivorship bias where relevant.
- Separate strategy eligibility from risk approval and execution permission. A valid setup is not automatically an authorised trade.
- Autonomous actions respect explicit risk limits, permissions, kill switches, and failure policies.
- An upstream data-feed failure must not silently become a valid market signal.
- Record enough provenance to explain and reproduce significant decisions without logging secrets or unnecessarily sensitive information.
- Version strategy definitions and configuration. Test and evaluate changes before allowing them to affect live behaviour.
- Adaptive or learning components have explicit authority. They must not silently modify protected risk constraints or bypass deterministic eligibility rules.
- Define safe behaviour when data is stale, dependencies are unavailable, processing falls behind, or system state cannot be trusted.
- Test interrupted processing and replay to ensure recovery does not create unintended duplicate decisions or actions.
- Distinguish an alert being generated from it being accepted by a provider or delivered to a user.
- Measure data freshness, gap-recovery status, processing lag, evaluation latency, and worker health.

## 17. Before I Call It Done

1. `gofmt`, `go vet`, `staticcheck`, and `go test -race ./...` pass, or exceptions are documented with a reason.
2. I can trace a request or event from entry to result without jumping through forwarding code.
3. Every nil check and fallback guards something that can actually happen.
4. Every interface, constructor, and helper has a reason I can state in one sentence.
5. Behaviour changes, including error returns, JSON shape, ordering, and retry semantics, are called out in the commit message.
6. Failure behaviour is defined for important I/O, concurrency, and background-processing paths.
7. Important assumptions and invariants are enforced by code, tests, or database constraints rather than comments alone.
8. Performance-sensitive paths have representative measurements before complex optimisation.
9. Logs and telemetry make important failures diagnosable without exposing secrets.
10. The change has been tested at the appropriate level: unit, integration, concurrency, fuzz, benchmark, or end-to-end, as applicable.

A passing test suite is necessary but not sufficient. Important assumptions must be explicit, failures must be detectable, and behaviour must be recoverable.

## 18. Limits of This Guide

- A consistent existing codebase convention beats this guide.
- Do not edit generated code (protobuf, sqlc, mocks) or change the exported API of a shared package without asking.
- Do not rewrite working code for taste. Flag it and move on.
- These are defaults, not absolutes. Deviate when the problem requires it, and make the trade-off clear in review.
- Do not optimise by intuition alone. Measure first, then make the smallest change that addresses the measured problem.
