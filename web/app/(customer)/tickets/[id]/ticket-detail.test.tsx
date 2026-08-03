import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api";
import type { Ticket } from "@/lib/tickets";

import { TicketDetail } from "./ticket-detail";

const { apiFetch } = vi.hoisted(() => ({ apiFetch: vi.fn() }));

vi.mock("@/lib/use-api", () => ({ useApiFetch: () => apiFetch }));

const ID = "6f1b5f2a-0000-4000-8000-000000000001";

const ticket: Ticket = {
  id: ID,
  title: "Cannot download my invoice",
  description: "The download button returns a 500.",
  category: "billing",
  priority: "normal",
  status: "open",
  sla_due_at: "2099-01-01T00:00:00Z",
  sla_breached: false,
  created_at: "2026-08-03T12:00:00Z",
  updated_at: "2026-08-03T12:00:00Z",
};

const history = {
  entries: [
    {
      from_status: null,
      to_status: "open",
      actor_role: "customer",
      reason: null,
      created_at: "2026-08-03T12:00:00.000Z",
    },
  ],
};

/**
 * Answers the two requests separately, so they can succeed and fail
 * independently — which is the behaviour half of these tests are about.
 *
 * `undefined` for either means the API answered 404 for it.
 */
function respond(options: { ticket?: unknown | Error; history?: unknown | Error }) {
  apiFetch.mockImplementation(async (path: string) => {
    const answer = path.endsWith("/history") ? options.history : options.ticket;

    if (answer === undefined) throw new ApiError(404, path);
    if (answer instanceof Error) throw answer;
    return answer;
  });
}

function renderDetail() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={client}>
      <TicketDetail id={ID} />
    </QueryClientProvider>,
  );

  return client;
}

// Block bodies, not concise ones. `() => apiFetch.mockReset()` returns the mock
// and Vitest awaits whatever a hook returns, so the hook hangs until it times
// out — 10 seconds later, and every test in the file fails with an error that
// points at the hook rather than at the arrow.
beforeEach(() => {
  apiFetch.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("TicketDetail", () => {
  it("shows the ticket and its timeline", async () => {
    respond({ ticket, history });
    renderDetail();

    expect(await screen.findByText(ticket.title)).toBeTruthy();
    expect(screen.getByText(ticket.description)).toBeTruthy();
    expect(await screen.findByText(/opened/i)).toBeTruthy();
  });

  // The API answers 404 both for a ticket that does not exist and for one
  // belonging to someone else, and cannot tell them apart. The wording must not
  // imply the id names something real.
  it("says no such ticket on a 404, without confirming it exists", async () => {
    respond({});
    renderDetail();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/no such ticket/i);
    expect(alert.textContent).not.toMatch(/permission|forbidden|not yours/i);
  });

  it("distinguishes a failure to load from a missing ticket", async () => {
    respond({ ticket: new ApiError(503, "/api/tickets") });
    renderDetail();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/could not be loaded/i);
    expect(alert.textContent).not.toMatch(/no such ticket/i);
  });

  // The history is context, not the content. Losing it should not look like
  // losing the ticket.
  it("keeps the ticket on screen when only the history fails", async () => {
    respond({ ticket, history: new ApiError(500, "history") });
    renderDetail();

    expect(await screen.findByText(ticket.title)).toBeTruthy();
    expect(await screen.findByText(/history could not be loaded/i)).toBeTruthy();
  });

  // Asking for the history of a ticket that answered 404 produces a second
  // "no such ticket" racing the first, and a request that was never going to
  // succeed.
  it("does not ask for the history of a ticket it could not load", async () => {
    respond({});
    renderDetail();

    await screen.findByRole("alert");
    for (const [path] of apiFetch.mock.calls) {
      expect(path).not.toContain("/history");
    }
  });

  it("renders the SLA state from the API", async () => {
    respond({ ticket: { ...ticket, sla_due_at: null }, history });
    renderDetail();

    expect(await screen.findByText(/paused/i)).toBeTruthy();
  });

  // The create form seeds this key before navigating. If the detail read a
  // different one the seeding would buy nothing and the page would fetch on
  // arrival — which is invisible in production and exactly what this catches.
  it("reads the key the create form seeds", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    client.setQueryData(["tickets", "detail", ID], ticket);

    apiFetch.mockImplementation(() => new Promise(() => {}));
    render(
      <QueryClientProvider client={client}>
        <TicketDetail id={ID} />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(screen.getByText(ticket.title)).toBeTruthy());
  });
});
