/**
 * Client for the SLA Desk API.
 *
 * The API runs on a different origin from this app — Cloud Run rather than
 * Vercel — so its location is configuration, never a constant. Nothing in this
 * codebase may hardcode a host.
 *
 * NEXT_PUBLIC_ values are inlined into the bundle at build time, not read at
 * runtime. Changing NEXT_PUBLIC_API_URL therefore requires a rebuild: set it in
 * the deployment environment *before* building, not after.
 */

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL;

export class ApiConfigurationError extends Error {
  constructor() {
    super(
      "NEXT_PUBLIC_API_URL is not set. Create web/.env.local for local " +
        "development, or set it in the deployment environment before building.",
    );
    this.name = "ApiConfigurationError";
  }
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly path: string,
  ) {
    super(`API request to ${path} failed with status ${status}`);
    this.name = "ApiError";
  }
}

/** Resolves a path against the configured API origin. */
export function apiUrl(path: string): string {
  if (!API_BASE_URL) {
    throw new ApiConfigurationError();
  }
  return new URL(path, API_BASE_URL).toString();
}

/**
 * Performs a JSON request against the API.
 *
 * It deliberately does not swallow failures: a non-2xx response throws, so
 * callers cannot mistake an error page for data. Authentication is attached in
 * T12, in one place rather than per call site.
 */
export async function apiFetch<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const response = await fetch(apiUrl(path), {
    ...init,
    headers: {
      Accept: "application/json",
      ...init?.headers,
    },
  });

  if (!response.ok) {
    throw new ApiError(response.status, path);
  }

  return (await response.json()) as T;
}
