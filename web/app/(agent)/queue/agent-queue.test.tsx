import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { QueueEntry } from "@/lib/agent";
import { ApiError } from "@/lib/api";

import { AgentQueue } from "./agent-queue";

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

function entry(overrides: Partial<QueueEntry> = {}): QueueEntry {
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
    requester_name: "Ada Lovelace",
    ...overrides,
  };
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function renderQueue(query = "", client = newClient()) {
  searchParams.current = new URLSearchParams(query);
  return render(
    <QueryClientProvider client={client}>
      <AgentQueue />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  apiFetch.mockReset();
  // Returns the mock, and vitest awaits whatever a hook returns — so a bare
  // `mockReset()` here would hang the hook for ten seconds and take every test
  // in the file with it. Recorded in docs/local-development.md after T14b lost
  // an afternoon to exactly this.
});

describe("the agent queue", () => {
  it("shows the requester, because a queue of titles alone is not workable", async () => {
    apiFetch.mockResolvedValue({
      tickets: [entry({ requester_name: "Ada Lovelace" })],
      next_cursor: null,
    });

    renderQueue();

    expect(await screen.findByText("Ada Lovelace")).toBeTruthy();
  });

  it("renders rows in the order the API sent them", async () => {
    apiFetch.mockResolvedValue({
      tickets: [
        entry({ id: "a", title: "breaches first" }),
        entry({ id: "b", title: "breaches later" }),
        entry({ id: "c", title: "paused", sla_due_at: null }),
      ],
      next_cursor: null,
    });

    renderQueue();

    // Read off the DOM rather than asserting each is present. Presence would
    // pass with the order reversed, and order is the entire product decision
    // behind this endpoint: the top row is the next breach.
    const items = await screen.findAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual([
      expect.stringContaining("breaches first"),
      expect.stringContaining("breaches later"),
      expect.stringContaining("paused"),
    ]);
  });

  it("sends every filter from the URL to the API", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });

    renderQueue("status=open&priority=urgent&assignee=me");

    await waitFor(() => expect(apiFetch).toHaveBeenCalled());
    const path = apiFetch.mock.calls[0][0] as string;
    expect(path).toContain("status=open");
    expect(path).toContain("priority=urgent");
    expect(path).toContain("assignee=me");
  });

  it("does not send a filter that is absent from the URL", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });

    renderQueue("status=open");

    await waitFor(() => expect(apiFetch).toHaveBeenCalled());
    const path = apiFetch.mock.calls[0][0] as string;
    // `?priority=` and no parameter mean the same thing to the API, but only
    // one of them is a URL worth sharing.
    expect(path).not.toContain("priority=");
    expect(path).not.toContain("assignee=");
  });

  it("caches each filter combination separately", async () => {
    const client = newClient();
    apiFetch.mockResolvedValue({
      tickets: [entry({ title: "from the unfiltered view" })],
      next_cursor: null,
    });

    const { unmount } = renderQueue("", client);
    await screen.findByText("from the unfiltered view");
    unmount();

    // The second render never resolves. Anything that appears on screen can
    // only have come out of the cache — which is what the filters being part
    // of the query key is there to prevent. T14a's mutation testing found this
    // gap on the customer list: dropping the filters from the key left every
    // assertion about the request passing while the wrong rows showed.
    apiFetch.mockReturnValue(new Promise(() => {}));
    renderQueue("status=closed", client);

    expect(screen.queryByText("from the unfiltered view")).toBeNull();
  });

  it("tells a customer they are the wrong account, rather than showing a status code", async () => {
    apiFetch.mockRejectedValue(new ApiError(403, "/api/agent/tickets"));

    renderQueue();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("agents");
    // The status code is not the useful part. What a person can act on is that
    // they are signed in as the wrong account.
    expect(alert.textContent).not.toMatch(/^The API answered/);
  });

  it("distinguishes an empty queue from an empty filter", async () => {
    apiFetch.mockResolvedValue({ tickets: [], next_cursor: null });

    const { unmount } = renderQueue();
    expect(await screen.findByText(/queue is empty/i)).toBeTruthy();
    unmount();

    renderQueue("status=closed", newClient());
    // "The queue is empty" in front of an agent who filtered to closed reads
    // as an outage rather than as a filter they applied.
    expect(await screen.findByText(/match these filters/i)).toBeTruthy();
  });
});
