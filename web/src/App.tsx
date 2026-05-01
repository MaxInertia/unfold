import { useEffect, useState } from "react";

type Health = { status: string; target?: string };

export function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/health")
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`${r.status}`))))
      .then(setHealth)
      .catch((e) => setError(String(e)));
  }, []);

  return (
    <main>
      <h1>unfold</h1>
      <p>follow execution paths through code by expanding calls inline</p>
      <section className="status">
        {error && <p className="error">api error: {error}</p>}
        {health && (
          <p>
            api: <code>{health.status}</code>
            {health.target && (
              <>
                {" — target: "}
                <code>{health.target}</code>
              </>
            )}
          </p>
        )}
        {!health && !error && <p>connecting…</p>}
      </section>
    </main>
  );
}
