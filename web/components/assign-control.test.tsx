import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AssignControl } from "@/components/assign-control";
import type { AgentTicket } from "@/lib/agent";
import { ApiError } from "@/lib/api";

const { apiFetch } = vi.hoisted(() => ({ apiFetch: vi.fn() }));

vi.mock("@/lib/use-api", () => ({
  useApiFetch: () => apiFetch,
}));

const ME = "11111111-1111-4111-8111-111111111111";
const COLLEAGUE = "22222222-2222-4222-8222-222222222222";

function ticket(assignee: string | null): AgentTicket {
  return {
    id: "6f1b5f2a-0000-4000-8000-000000000001",
    title: "Cannot download my invoice",
    description: "body",
    category: "billing",
    priority: "normal",
    status: "open",
    sla_due_at: "2099-01-01T00:00:00Z",
    sla_breached: false,
    assignee_id: assignee,
    created_at: "2026-08-03T12:00:00Z",
    updated_at: "2026-08-03T12:00:00Z",
  };
}

const roster = {
  users: [
    { id: COLLEAGUE, name: "Ada Lovelace", role: "agent" },
    { id: ME, name: "me@example.test", role: "admin" },
  ],
};

// Routes by path so the roster and the assignment can succeed or fail
// independently, which is behaviour under test.
function respond({
  onRoster,
  onAssign,
}: {
  onRoster?: () => unknown;
  onAssign?: () => unknown;
} = {}) {
  apiFetch.mockImplementation((path: string) => {
    if (path.endsWith("/assignable")) {
      return onRoster
        ? Promise.resolve(onRoster())
        : Promise.reject(new Error("no roster"));
    }
    return onAssign
      ? Promise.resolve(onAssign())
      : Promise.reject(new Error("no assign"));
  });
}

function renderControl(assignee: string | null) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <AssignControl ticket={ticket(assignee)} me={{ id: ME }} />
      </QueryClientProvider>,
    ),
  };
}

beforeEach(() => {
  apiFetch.mockReset();
  // Returns the mock, and vitest awaits what a hook returns (T14b).
});

describe("the assign control", () => {
  it("takes the ticket in one click", async () => {
    respond({ onRoster: () => roster, onAssign: () => ticket(ME) });

    renderControl(null);
    await userEvent.click(screen.getByRole("button", { name: /assign to me/i }));

    await waitFor(() =>
      expect(
        apiFetch.mock.calls.some(([p]) => (p as string).endsWith("/assignee")),
      ).toBe(true),
    );

    const call = apiFetch.mock.calls.find(([p]) =>
      (p as string).endsWith("/assignee"),
    )!;
    const init = call[1] as RequestInit;
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(init.body as string).assignee_id).toBe(ME);
  });

  it("does not offer to take a ticket that is already mine", async () => {
    respond({ onRoster: () => roster });

    renderControl(ME);

    expect(screen.queryByRole("button", { name: /assign to me/i })).toBeNull();
    // Dropping it is still on the table.
    expect(screen.getByRole("button", { name: /unassign/i })).toBeTruthy();
  });

  it("sends an explicit null to unassign", async () => {
    respond({ onRoster: () => roster, onAssign: () => ticket(null) });

    renderControl(ME);
    await userEvent.click(screen.getByRole("button", { name: /unassign/i }));

    await waitFor(() =>
      expect(
        apiFetch.mock.calls.some(([p]) => (p as string).endsWith("/assignee")),
      ).toBe(true),
    );

    const call = apiFetch.mock.calls.find(([p]) =>
      (p as string).endsWith("/assignee"),
    )!;
    const body = JSON.parse((call[1] as RequestInit).body as string);
    // Explicit null, and the key has to be present: the API refuses an absent
    // field rather than reading it as an unassignment, so that a client
    // sending {} by accident cannot clear somebody's work.
    expect("assignee_id" in body).toBe(true);
    expect(body.assignee_id).toBeNull();
  });

  it("offers no button to unassign a ticket nobody is on", () => {
    respond({ onRoster: () => roster });

    renderControl(null);

    expect(screen.queryByRole("button", { name: /unassign/i })).toBeNull();
  });

  it("hands the ticket to a colleague chosen from the roster", async () => {
    respond({ onRoster: () => roster, onAssign: () => ticket(COLLEAGUE) });

    renderControl(null);
    await screen.findByRole("option", { name: /ada lovelace/i });

    await userEvent.selectOptions(
      screen.getByLabelText(/hand it to/i),
      COLLEAGUE,
    );

    await waitFor(() =>
      expect(
        apiFetch.mock.calls.some(([p]) => (p as string).endsWith("/assignee")),
      ).toBe(true),
    );

    const call = apiFetch.mock.calls.find(([p]) =>
      (p as string).endsWith("/assignee"),
    )!;
    const body = JSON.parse((call[1] as RequestInit).body as string);
    expect(body.assignee_id).toBe(COLLEAGUE);
  });

  it("shows each colleague's role, because they are different colleagues", async () => {
    respond({ onRoster: () => roster });

    renderControl(null);

    expect(await screen.findByRole("option", { name: /ada lovelace \(agent\)/i })).toBeTruthy();
    expect(screen.getByRole("option", { name: /me@example.test \(admin\)/i })).toBeTruthy();
  });

  it("keeps the buttons usable when the roster will not load", async () => {
    respond({ onAssign: () => ticket(ME) });

    renderControl(null);

    // The roster failing is not a reason to stop somebody taking a ticket.
    expect(await screen.findByText(/could not be loaded/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /assign to me/i })).toBeTruthy();
  });

  it("renders the API's own sentence when the assignee is refused", async () => {
    respond({
      onRoster: () => roster,
      onAssign: () => {
        throw new ApiError(400, "/api/agent/tickets/x/assignee", {
          type: "about:blank",
          title: "Bad Request",
          status: 400,
          detail: "the request body failed validation",
          errors: { assignee_id: "that user may not hold tickets" },
        });
      },
    });

    renderControl(null);
    await userEvent.click(screen.getByRole("button", { name: /assign to me/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("may not hold tickets");
  });

  it("invalidates the queue but not the timeline", async () => {
    respond({ onRoster: () => roster, onAssign: () => ticket(ME) });

    const { client } = renderControl(null);
    const invalidate = vi.spyOn(client, "invalidateQueries");

    await userEvent.click(screen.getByRole("button", { name: /assign to me/i }));
    await waitFor(() => expect(invalidate).toHaveBeenCalled());

    const keys = invalidate.mock.calls.map(([arg]) =>
      JSON.stringify((arg as { queryKey: unknown }).queryKey),
    );

    // The queue can be filtered by assignee, so a row may have just left or
    // joined the view.
    expect(keys).toContain(JSON.stringify(["agent", "queue"]));
    // No history row is written by an assignment (plan §E), so refetching the
    // timeline would be a request for an answer that did not change.
    expect(keys.some((k) => k.includes("history"))).toBe(false);
  });
});
