import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api";
import type { Ticket } from "@/lib/tickets";

import { TicketList } from "./ticket-list";

const { apiFetch, searchParams } = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  searchParams: { current: new URLSearchParams() },
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => searchParams.current,
}));

vi.mock("@/lib/use-api", () => ({
  useApiFetch: () => apiFetch,
}));

function ticket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: "6f1b5f2a-0000-4000-8000-000000000001",
    title: "Cannot download my invoice",
    description: "body",
    category: "billing",
    priority: "normal",
    status: "open",
    sla_due_at: "2099-01-01T00:00:00Z",
    sla_breached: false,
    created_at: "2026-08-03T12:00:00Z",
    updated_at: "2026-08-03T12:00:00Z",
    ...overrides,
  };
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function renderList(query = "", client = newClient()) {
  searchParams.current = new URLSearchParams(query);

  render(
    <QueryClientProvider client={client}>
      <TicketList />
    </QueryClientProvider>,
  );

  return client;
}

beforeEach(() => {
  apiFetch.mockReset();
  searchParams.current = new URLSearchParams();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("TicketList", () => {
  it("says it is loading before the first response", () => {
    apiFetch.mockImplementation(() => new Promise(() => {}));
    renderList();

    expect(screen.getByText(/loading/i)).toBeTruthy();
  });

  it("reports a failure with its status", async () => {
    apiFetch.mockRejectedValue(new ApiError(503, "/api/tickets"));
    renderList();

    expect((await screen.findByRole("alert")).textContent).toContain("503");
  });

  it("shows the tickets with their SLA state", async () => {
    apiFetch.mockResolvedValue({
      tickets: [ticket({ title: "Invoice download fails" })],
      next_cursor: null,
    });
    renderList();

    expect(await screen.findByText("Invoice download fails")).toBeTruthy();
    expect(screen.getByText(/left|due now|paused/i)).toBeTruthy();
  });

  // A paused ticket in the list has to read as paused. Rendering the absence of
  // a deadline as blank hides that the ticket is waiting on the customer.
  it("renders a paused clock as paused", async () => {
    apiFetch.mockResolvedValue({
      tickets: [ticket({ sla_due_at: null })],
      next_cursor: null,
    });
    renderList();

    expect(await screen.findByText(/paused/i)).toBeTruthy();
  });

  // Two empty states, because they mean opposite things. Telling a customer
  // with nine tickets that they have none — because they filtered to closed —
  // reads as data loss, and hides the one action that would help.
  it("offers to create a ticket when there are none at all", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });
    renderList();

    expect(await screen.findByText(/no tickets yet/i)).toBeTruthy();
    expect(screen.getByRole("link", { name: /raise one/i })).toBeTruthy();
  });

  it("offers to clear the filters when they are what emptied the list", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });
    renderList("status=closed");

    expect(await screen.findByText(/no tickets match/i)).toBeTruthy();
    expect(screen.getByRole("link", { name: /clear them/i })).toBeTruthy();
    expect(screen.queryByText(/no tickets yet/i)).toBeNull();
  });

  // Filtering happens in SQL. A request that dropped the filters would return
  // everything, and the page would render it under a heading claiming to be
  // filtered — the failure looks like the filter being ignored, which is
  // exactly what it is.
  it("sends the filters from the URL to the API", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });
    renderList("status=pending&priority=urgent");

    await waitFor(() => expect(apiFetch).toHaveBeenCalled());

    const path = apiFetch.mock.calls[0][0] as string;
    expect(path).toContain("status=pending");
    expect(path).toContain("priority=urgent");
  });

  // An unset filter must not travel as an empty parameter. The API treats the
  // two the same, but a URL carrying `?status=` is noise in a log and makes two
  // identical requests look different.
  it("omits a filter that is not set", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });
    renderList();

    await waitFor(() => expect(apiFetch).toHaveBeenCalled());
    expect(apiFetch.mock.calls[0][0]).toBe("/api/tickets");
  });

  // The filters are part of the cache key, and this is what goes wrong when
  // they are not: the request is right, so every assertion about the API still
  // passes, but each view reads the previous one's rows out of the cache and
  // shows them until the refetch lands. A customer switching to `closed` sees
  // their open tickets under a heading that says closed.
  //
  // Two renders against one client, the second never resolving, so the only
  // thing that could put a row on screen is the cache.
  it("never shows another filter's rows while its own load", async () => {
    const client = newClient();

    apiFetch.mockResolvedValue({
      tickets: [ticket({ title: "An open one" })],
      next_cursor: null,
    });
    renderList("status=open", client);
    await screen.findByText("An open one");

    cleanup();
    apiFetch.mockImplementation(() => new Promise(() => {}));
    renderList("status=closed", client);

    expect(screen.queryByText("An open one")).toBeNull();
    expect(screen.getByText(/loading/i)).toBeTruthy();
  });
});
