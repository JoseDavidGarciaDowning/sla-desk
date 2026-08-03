import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, apiFetch } from "@/lib/api";

function respondWith(body: string, init: ResponseInit): void {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(body, init)));
}

function lastRequest(): [string, RequestInit] {
  const fetchMock = vi.mocked(globalThis.fetch);
  return fetchMock.mock.calls[0] as unknown as [string, RequestInit];
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("apiFetch", () => {
  it("returns the decoded body of a successful response", async () => {
    respondWith(JSON.stringify({ tickets: [], next_cursor: null }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });

    await expect(apiFetch("/api/tickets")).resolves.toEqual({
      tickets: [],
      next_cursor: null,
    });
  });

  it("resolves the path against the configured origin", async () => {
    respondWith("{}", { status: 200 });

    await apiFetch("/api/tickets");

    expect(lastRequest()[0]).toBe("http://api.test/api/tickets");
  });

  // The whole reason this file changed in T13. Without the problem document the
  // form knows only "400" and has to guess which field the server objected to —
  // which means reimplementing the server's rules in TypeScript to make the
  // guess, and that copy is what drifts.
  it("carries the per-field errors of a problem document", async () => {
    respondWith(
      JSON.stringify({
        type: "about:blank",
        title: "Bad Request",
        status: 400,
        detail: "the request body failed validation",
        errors: {
          title: "must not be empty",
          priority: "must be one of urgent, high, normal, low",
        },
      }),
      {
        status: 400,
        headers: { "Content-Type": "application/problem+json" },
      },
    );

    const error = await apiFetch("/api/tickets").catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(400);
    expect((error as ApiError).fieldErrors).toEqual({
      title: "must not be empty",
      priority: "must be one of urgent, high, normal, low",
    });
  });

  // Not every failure comes from our API. A gateway timeout on Cloud Run is an
  // HTML page, and a request that never reaches the container has no body at
  // all. Both have to arrive as an ApiError carrying the status — the one thing
  // the caller can act on — rather than as a parse error that hides it.
  it.each([
    ["an HTML error page", "<html>502 Bad Gateway</html>", "text/html", 502],
    ["an empty body", "", "application/problem+json", 503],
    ["a truncated document", '{"status":500,', "application/problem+json", 500],
  ])("survives %s", async (_name, body, contentType, status) => {
    respondWith(body, { status, headers: { "Content-Type": contentType } });

    const error = await apiFetch("/api/tickets").catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(status);
    expect((error as ApiError).fieldErrors).toEqual({});
  });

  // A JSON body that is not a problem document must not be mistaken for one.
  // Content negotiation is the only signal available, and RFC 9457 exists so
  // that it is a reliable one.
  it("does not read field errors out of a plain JSON error body", async () => {
    respondWith(JSON.stringify({ errors: { title: "not ours" } }), {
      status: 400,
      headers: { "Content-Type": "application/json" },
    });

    const error = await apiFetch("/api/tickets").catch((e: unknown) => e);

    expect((error as ApiError).fieldErrors).toEqual({});
  });

  it("sends a body as JSON, so the API can decode it", async () => {
    respondWith("{}", { status: 201 });

    await apiFetch("/api/tickets", {
      method: "POST",
      body: JSON.stringify({ title: "Hello" }),
    });

    const headers = lastRequest()[1].headers as Record<string, string>;
    expect(headers["Content-Type"]).toBe("application/json");
  });

  // A request with nothing to describe must not describe it anyway.
  // Content-Type is not on the CORS safelist for this value, so setting it on
  // every GET would turn each one into a preflight round trip it does not need.
  it("does not announce a content type on a request with no body", async () => {
    respondWith("{}", { status: 200 });

    await apiFetch("/api/tickets");

    const headers = lastRequest()[1].headers as Record<string, string>;
    expect(headers["Content-Type"]).toBeUndefined();
  });

  // Every default here is a default, not a policy: a caller that states a
  // header means it. Authorization arrives this way from use-api.ts, and both
  // Accept and Content-Type have to be overridable for the day something is
  // uploaded that is not JSON.
  it("lets a caller's headers win over every default", async () => {
    respondWith("{}", { status: 200 });

    await apiFetch("/api/tickets", {
      method: "POST",
      body: "title=Hello",
      headers: {
        Authorization: "Bearer token",
        Accept: "text/plain",
        "Content-Type": "application/x-www-form-urlencoded",
      },
    });

    const headers = lastRequest()[1].headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer token");
    expect(headers.Accept).toBe("text/plain");
    expect(headers["Content-Type"]).toBe("application/x-www-form-urlencoded");
  });
});
