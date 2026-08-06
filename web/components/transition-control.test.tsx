import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { TransitionControl } from "@/components/transition-control";
import { ApiError } from "@/lib/api";
import type { Ticket } from "@/lib/tickets";

const { apiFetch } = vi.hoisted(() => ({ apiFetch: vi.fn() }));

vi.mock("@/lib/use-api", () => ({
  useApiFetch: () => apiFetch,
}));

function ticket(status: string): Ticket {
  return {
    id: "6f1b5f2a-0000-4000-8000-000000000001",
    title: "Cannot download my invoice",
    description: "body",
    category: "billing",
    priority: "normal",
    status,
    sla_due_at: "2099-01-01T00:00:00Z",
    sla_breached: false,
    created_at: "2026-08-03T12:00:00Z",
    updated_at: "2026-08-03T12:00:00Z",
  };
}

function renderControl(status: string, role: "agent" | "customer" = "agent") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <TransitionControl ticket={ticket(status)} role={role} />
      </QueryClientProvider>,
    ),
  };
}

beforeEach(() => {
  apiFetch.mockReset();
  // Returns the mock, and vitest awaits what a hook returns — a bare
  // mockReset() hangs the hook and takes the file with it (T14b).
});

describe("the transition control", () => {
  it("offers the moves that leave the current status and no others", async () => {
    renderControl("open");

    // From open an agent may set pending or resolve. Closing is only reachable
    // from resolved, and reopening is not a move from open at all.
    expect(screen.getByRole("button", { name: /pending/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /resolved/i })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /closed/i })).toBeNull();
  });

  it("changes what it offers when the ticket is somewhere else", () => {
    const { unmount } = renderControl("resolved");

    // From resolved: reopen or close. Setting pending is not an edge.
    expect(screen.getByRole("button", { name: /open/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /closed/i })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /pending/i })).toBeNull();
    unmount();
  });

  it("offers nothing on a closed ticket, and says why", () => {
    renderControl("closed");

    // Not a disabled row. A greyed-out button invites a click that can never
    // work; closed is terminal and the sentence says so.
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.getByText(/closed/i)).toBeTruthy();
  });

  it("sends the target and omits an empty reason", async () => {
    apiFetch.mockResolvedValue(ticket("pending"));

    renderControl("open");
    await userEvent.click(screen.getByRole("button", { name: /pending/i }));

    await waitFor(() => expect(apiFetch).toHaveBeenCalled());
    const [path, init] = apiFetch.mock.calls[0] as [string, RequestInit];
    expect(path).toContain("/api/agent/tickets/");
    expect(path).toContain("/transitions");

    const body = JSON.parse(init.body as string);
    expect(body.to).toBe("pending");
    // An empty box is no reason. The API stores whitespace as absent anyway;
    // not sending it keeps the two agreeing.
    expect(body.reason).toBeUndefined();
  });

  it("sends a reason when one is typed", async () => {
    apiFetch.mockResolvedValue(ticket("pending"));

    renderControl("open");
    await userEvent.type(screen.getByLabelText(/reason/i), "  waiting on them  ");
    await userEvent.click(screen.getByRole("button", { name: /pending/i }));

    await waitFor(() => expect(apiFetch).toHaveBeenCalled());
    const [, init] = apiFetch.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string).reason).toBe("waiting on them");
  });

  it("settles every cache a move invalidates", async () => {
    const updated = ticket("pending");
    apiFetch.mockResolvedValue(updated);

    const { client } = renderControl("open");
    const invalidate = vi.spyOn(client, "invalidateQueries");
    const seed = vi.spyOn(client, "setQueryData");

    await userEvent.click(screen.getByRole("button", { name: /pending/i }));
    await waitFor(() => expect(invalidate).toHaveBeenCalled());

    const invalidated = invalidate.mock.calls.map(([arg]) =>
      JSON.stringify((arg as { queryKey: unknown }).queryKey),
    );

    // The queue is ordered by deadline and a pause clears one, so the row has
    // moved: every cached page of it is stale. Missing this leaves an agent
    // looking at a queue that still shows the ticket where it used to be.
    expect(invalidated).toContain(JSON.stringify(["agent", "queue"]));

    // The timeline gained a row the API did not send back.
    expect(invalidated).toContain(
      JSON.stringify(["agent", "history", updated.id]),
    );

    // The detail is written rather than invalidated: the response is the real
    // ticket, so refetching it would blank the view to fetch what we hold.
    expect(seed).toHaveBeenCalledWith(["agent", "detail", updated.id], updated);
  });

  it("renders the API's own sentence when a move is refused", async () => {
    apiFetch.mockRejectedValue(
      new ApiError(403, "/api/agent/tickets/x/transitions", {
        type: "about:blank",
        title: "Forbidden",
        status: 403,
        detail: "your role may not make that transition",
      }),
    );

    renderControl("open");
    await userEvent.click(screen.getByRole("button", { name: /pending/i }));

    const alert = await screen.findByRole("alert");
    // Verbatim. A 403 here means the move exists and is not this role's to
    // make, which is worth reading rather than flattening into "an error".
    expect(alert.textContent).toContain("your role may not make that transition");
  });

  it("renders a field error when the move is impossible", async () => {
    apiFetch.mockRejectedValue(
      new ApiError(400, "/api/agent/tickets/x/transitions", {
        type: "about:blank",
        title: "Bad Request",
        status: 400,
        detail: "the request body failed validation",
        errors: { to: "that transition is not possible from this ticket's status" },
      }),
    );

    renderControl("open");
    await userEvent.click(screen.getByRole("button", { name: /pending/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("not possible");
  });
});
