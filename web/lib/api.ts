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

/**
 * The media type RFC 9457 assigns to problem documents.
 *
 * It is the only reliable signal that a body is one. A 400 could just as well
 * carry an HTML page from a proxy, or JSON of some other shape, and reading
 * either as a problem document would invent field errors that nobody sent.
 */
const PROBLEM_CONTENT_TYPE = "application/problem+json";

/**
 * An RFC 9457 problem document, mirroring Problem in internal/api/problem.go.
 *
 * `errors` is the extension member the API uses for per-field validation
 * messages, and it is the reason the frontend can show which field the server
 * objected to without knowing any of the server's rules.
 */
export type Problem = {
  type: string;
  title: string;
  status: number;
  detail?: string;
  errors?: Record<string, string>;
};

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly path: string,
    /** Absent when the response carried no readable problem document. */
    readonly problem?: Problem,
  ) {
    super(
      problem?.detail ?? `API request to ${path} failed with status ${status}`,
    );
    this.name = "ApiError";
  }

  /**
   * Per-field messages from the server, keyed by the field's JSON name.
   *
   * Empty when the failure was not a validation one — or when the response was
   * not something this client could read. A form renders whatever is here and
   * decides nothing: the rules, the limits and the wording all belong to the
   * API, which is the only place they can be enforced anyway.
   */
  get fieldErrors(): Record<string, string> {
    return this.problem?.errors ?? {};
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
 * Reads a problem document out of a failed response.
 *
 * Returns undefined rather than throwing for anything it cannot read. A caller
 * facing a 502 needs to be told about the 502, and an error thrown while
 * parsing the gateway's HTML would replace that with a parse failure — the one
 * piece of information the caller could have acted on, lost to the reporting of
 * a second problem.
 */
async function readProblem(response: Response): Promise<Problem | undefined> {
  if (!response.headers.get("Content-Type")?.includes(PROBLEM_CONTENT_TYPE)) {
    return undefined;
  }

  try {
    return (await response.json()) as Problem;
  } catch {
    return undefined;
  }
}

/**
 * Performs a JSON request against the API.
 *
 * It deliberately does not swallow failures: a non-2xx response throws, so
 * callers cannot mistake an error page for data. What it does carry across is
 * the problem document, so a caller can render the server's own field errors
 * instead of deducing them.
 *
 * Authentication is attached in use-api.ts, in one place rather than per call
 * site.
 */
export async function apiFetch<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const response = await fetch(apiUrl(path), {
    ...init,
    headers: {
      Accept: "application/json",
      // Only when there is something to describe. Setting it unconditionally
      // would make every GET a preflighted request for no reason.
      //
      // Every default is spread before the caller's headers, so a caller that
      // states one means it — including Authorization, which arrives this way
      // from use-api.ts.
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });

  if (!response.ok) {
    throw new ApiError(response.status, path, await readProblem(response));
  }

  return (await response.json()) as T;
}
