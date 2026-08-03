import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { PRIORITIES, STATUSES } from "@/lib/contract";

import { TicketFilters } from "./ticket-filters";

const { replace, searchParams } = vi.hoisted(() => ({
  replace: vi.fn(),
  searchParams: { current: new URLSearchParams() },
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => searchParams.current,
  useRouter: () => ({ replace }),
  usePathname: () => "/tickets",
}));

function renderFilters(query = "") {
  searchParams.current = new URLSearchParams(query);
  render(<TicketFilters />);
}

/** The URL the component asked the router to go to. */
function navigatedTo(): string {
  return replace.mock.calls[0][0] as string;
}

beforeEach(() => {
  replace.mockReset();
  searchParams.current = new URLSearchParams();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("TicketFilters", () => {
  // The options come from the generated contract, so a status the API accepts
  // cannot be missing from the control and one it rejects cannot appear in it.
  it("offers every value the contract publishes", () => {
    renderFilters();

    const values = screen
      .getAllByRole("option")
      .map((option) => (option as HTMLOptionElement).value);

    for (const status of STATUSES) {
      expect(values).toContain(status);
    }
    for (const priority of PRIORITIES) {
      expect(values).toContain(priority);
    }
  });

  // The URL is the state. A control that did not read it would reset itself on
  // every reload, and a shared link would arrive showing "Any" over a filtered
  // list — the page and its own controls disagreeing.
  it("shows what the URL says", () => {
    renderFilters("status=resolved&priority=low");

    expect(screen.getByLabelText(/status/i)).toHaveProperty("value", "resolved");
    expect(screen.getByLabelText(/priority/i)).toHaveProperty("value", "low");
  });

  it("puts a chosen filter in the URL", async () => {
    const user = userEvent.setup();
    renderFilters();

    await user.selectOptions(screen.getByLabelText(/status/i), "pending");

    expect(navigatedTo()).toBe("/tickets?status=pending");
  });

  it("keeps the other filter when one changes", async () => {
    const user = userEvent.setup();
    renderFilters("priority=urgent");

    await user.selectOptions(screen.getByLabelText(/status/i), "open");

    expect(navigatedTo()).toContain("priority=urgent");
    expect(navigatedTo()).toContain("status=open");
  });

  // Clearing removes the parameter rather than setting it empty. Both mean the
  // same to the API, but only one of them is a link worth sharing.
  it("removes a filter rather than emptying it", async () => {
    const user = userEvent.setup();
    renderFilters("status=open&priority=urgent");

    await user.selectOptions(screen.getByLabelText(/status/i), "");

    expect(navigatedTo()).toBe("/tickets?priority=urgent");
  });

  it("goes back to a bare path when the last filter is cleared", async () => {
    const user = userEvent.setup();
    renderFilters("status=open");

    await user.selectOptions(screen.getByLabelText(/status/i), "");

    expect(navigatedTo()).toBe("/tickets");
  });

  // The cursor addresses a position in the unfiltered sequence. Carried across
  // a filter change it would resume from a row the new filter may exclude, and
  // the first page of the filtered list would silently start in the middle.
  it("drops the pagination cursor when a filter changes", async () => {
    const user = userEvent.setup();
    renderFilters("cursor=abc123");

    await user.selectOptions(screen.getByLabelText(/priority/i), "high");

    expect(navigatedTo()).not.toContain("cursor");
  });

  // replace, not push. Otherwise every filter change is a history entry and the
  // back button walks the customer through each one instead of returning them
  // where they came from.
  it("replaces the history entry instead of stacking one", async () => {
    const user = userEvent.setup();
    renderFilters();

    await user.selectOptions(screen.getByLabelText(/status/i), "open");

    expect(replace).toHaveBeenCalledTimes(1);
  });
});
