import { useEffect, useState } from "react";

// Minimal shell that reports backend readiness. Replace with the real
// dashboard as the chart and setup views land (ADR-008).
export function App() {
  const [status, setStatus] = useState<string>("checking...");

  useEffect(() => {
    fetch("/readyz")
      .then((r) => (r.ok ? "ready" : "degraded"))
      .then(setStatus)
      .catch(() => setStatus("unreachable"));
  }, []);

  return (
    <main style={{ fontFamily: "system-ui, sans-serif", padding: "2rem" }}>
      <h1>ARGUS</h1>
      <p>Autonomous Market Intelligence</p>
      <p>
        Backend status: <strong>{status}</strong>
      </p>
    </main>
  );
}
