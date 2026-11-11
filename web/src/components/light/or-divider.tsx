// Hairline either side (light-fill, matching the OR divider's own
// sampled colour off signup-email.png), centred small-caps "OR".
export function OrDivider() {
  return (
    <div className="flex items-center gap-4" role="separator">
      <span className="h-px flex-1 bg-light-fill" />
      <span className="text-[12px] font-medium uppercase tracking-[0.12em] text-light-muted">
        Or
      </span>
      <span className="h-px flex-1 bg-light-fill" />
    </div>
  );
}
