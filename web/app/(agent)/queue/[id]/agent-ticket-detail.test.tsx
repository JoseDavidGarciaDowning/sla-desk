import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api";

import { AgentTicketDetail } from "./agent-ticket-detail";

const { apiFetch } = vi.hoisted(() => ({ apiFetch: vi.fn() }));

vi.mock("@/lib/use-api", () => ({
  useApiFetch: () => apiFetch,
}));

const ticket = {
  id: "6f1b5f2a-0000-4000-8000-000000000001",
  title: "Cannot download my invoice",
  description: "The download button returns a 500.",
  category: "billing",
  priority: "urgent",
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
      created_at: "2026-08-03T12:00:00Z",
    },
  ],
};

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function renderDetail() {
  return render(
    <QueryClientProvider client={newClient()}>
      <AgentTicketDetail id={ticket.id} />
    </QueryClientProvider>,
  );
}

// Routes the mock by path, so the ticket and the history can succeed and fail
// independently — which is the behaviour under test.
function respond({
  onTicket,
  onHistory,
}: {
  onTicket?: () => unknown;
  onHistory?: () => unknown;
}) {
  apiFetch.mockImplementation((path: string) => {
    if (path.endsWith("/history")) {
      return onHistory ? Promise.resolve(onHistory()) : Promise.reject(new Error("no"));
    }
    return onTicket ? Promise.resolve(onTicket()) : Promise.reject(new Error("no"));
  });
}

beforeEach(() => {
  apiFetch.mockReset();
  // Returns the mock, and vitest awaits whatever a hook returns — a bare
  // mockReset() here hangs the hook and takes the whole file with it. T14b
  // lost an afternoon to exactly this; it is recorded in
  // docs/local-development.md.
});

describe("the agent ticket detail", () => {
  it("reads a ticket that belongs to somebody else", async () => {
    respond({ onTicket: () => ticket, onHistory: () => history });

    renderDetail();

    expect(await screen.findByText(ticket.title)).toBeTruthy();
    // The endpoints it reads are the agent's, not the customer's. The customer
    // ones would answer 404 for a ticket that is not the caller's, so pointing
    // at them would be invisible in a mock and fatal in production.
    const paths = apiFetch.mock.calls.map((c) => c[0] as string);
    expect(paths.every((p) => p.startsWith("/api/agent/tickets/"))).toBe(true);
  });

  it("keeps the ticket on screen when the history fails", async () => {
    respond({ onTicket: () => ticket });

    renderDetail();

    // The history is context, not the content. A timeline that will not load
    // must not blank out the ticket an agent is trying to work.
    expect(await screen.findByText(ticket.title)).toBeTruthy();
    expect(await screen.findByText(/history could not be loaded/i)).toBeTruthy();
  });

  it("says the ticket does not exist, rather than showing a status code", async () => {
    apiFetch.mockRejectedValue(new ApiError(404, "/api/agent/tickets/x"));

    renderDetail();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/no such ticket/i);
  });

  it("does not ask for the history of a ticket that is not there", async () => {
    apiFetch.mockRejectedValue(new ApiError(404, "/api/agent/tickets/x"));

    renderDetail();
    await screen.findByRole("alert");

    // Two requests would render two copies of the same message, and the second
    // one is pointless: the id names nothing.
    const paths = apiFetch.mock.calls.map((c) => c[0] as string);
    expect(paths.some((p) => p.endsWith("/history"))).toBe(false);
  });
});
