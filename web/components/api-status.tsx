"use client";

import { useEffect, useState } from "react";

import { ApiConfigurationError, apiFetch } from "@/lib/api";

type HealthResponse = {
  status: string;
  checks?: Record<string, string>;
};

type State =
  | { kind: "loading" }
  | { kind: "reachable"; health: HealthResponse }
  | { kind: "unconfigured" }
  | { kind: "unreachable"; detail: string };

/**
 * Live status of the SLA Desk API.
 *
 * This is a client component on purpose. The whole point is to prove that a
 * *browser* on the Vercel origin can call the API on Cloud Run — which means
 * exercising CORS. A server component would fetch server-to-server and never
 * touch CORS at all, so it would pass while the real thing was broken.
 */
export function ApiStatus() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    const controller = new AbortController();

    apiFetch<HealthResponse>("/health", { signal: controller.signal })
      .then((health) => setState({ kind: "reachable", health }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;

        if (error instanceof ApiConfigurationError) {
          setState({ kind: "unconfigured" });
          return;
        }
        setState({
          kind: "unreachable",
          detail: error instanceof Error ? error.message : "Unknown error",
        });
      });

    return () => controller.abort();
  }, []);

  return (
    <div className="rounded-lg border border-border bg-muted/30 p-4 text-sm">
      <p className="mb-2 font-medium">API status</p>
      <Body state={state} />
    </div>
  );
}

function Body({ state }: { state: State }) {
  switch (state.kind) {
    case "loading":
      return (
        <p className="text-muted-foreground" aria-live="polite">
          Checking…
        </p>
      );

    case "unconfigured":
      return (
        <p className="text-muted-foreground" aria-live="polite">
          <Dot className="bg-muted-foreground" />
          NEXT_PUBLIC_API_URL is not set for this build. It is inlined at build
          time, so setting it now requires a redeploy.
        </p>
      );

    case "unreachable":
      return (
        <p className="text-muted-foreground" aria-live="polite">
          <Dot className="bg-destructive" />
          Unreachable — {state.detail}
        </p>
      );

    case "reachable":
      return (
        <div aria-live="polite">
          <p>
            <Dot
              className={
                state.health.status === "ok" ? "bg-emerald-500" : "bg-amber-500"
              }
            />
            {state.health.status}
          </p>
          {state.health.checks && (
            <ul className="mt-2 space-y-1 text-muted-foreground">
              {Object.entries(state.health.checks).map(([name, result]) => (
                <li key={name}>
                  {name}: {result}
                </li>
              ))}
            </ul>
          )}
        </div>
      );
  }
}

function Dot({ className }: { className: string }) {
  return (
    <span
      aria-hidden
      className={`mr-2 inline-block size-2 rounded-full align-middle ${className}`}
    />
  );
}
