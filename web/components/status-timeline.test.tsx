import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StatusTimeline } from "@/components/status-timeline";
import type { TicketHistoryEntry } from "@/lib/tickets";

function entry(overrides: Partial<TicketHistoryEntry> = {}): TicketHistoryEntry {
  return {
    from_status: "open",
    to_status: "pending",
    actor_role: "agent",
    reason: null,
    created_at: "2026-08-03T12:00:00.000Z",
    ...overrides,
  };
}

describe("StatusTimeline", () => {
  // The first entry moved from nowhere. Rendering it as "from null to open", or
  // leaving the origin blank, describes a transition that never happened —
  // the ticket was created.
  it("reads the creation entry as a creation", () => {
    render(<StatusTimeline entries={[entry({ from_status: null, to_status: "open" })]} />);

    expect(screen.getByText(/opened/i)).toBeTruthy();
    expect(screen.queryByText(/null|undefined/i)).toBeNull();
    expect(screen.queryByText(/→|from/i)).toBeNull();
  });

  it("names both ends of a move and who made it", () => {
    render(
      <StatusTimeline
        entries={[entry({ from_status: "open", to_status: "resolved", actor_role: "agent" })]}
      />,
    );

    const item = screen.getByRole("listitem");
    expect(item.textContent).toContain("open");
    expect(item.textContent).toContain("resolved");
    expect(item.textContent?.toLowerCase()).toContain("agent");
  });

  it("shows a reason when there is one and nothing when there is not", () => {
    const { unmount } = render(
      <StatusTimeline entries={[entry({ reason: "Waiting on the customer" })]} />,
    );
    expect(screen.getByText(/waiting on the customer/i)).toBeTruthy();
    unmount();

    render(<StatusTimeline entries={[entry({ reason: null })]} />);
    expect(screen.queryByText(/null/i)).toBeNull();
  });

  // The timestamp is asserted through the machine-readable attribute, not
  // through the rendered text. The text is formatted for whatever locale the
  // reader has, which is right for them and untestable here.
  it("carries the exact instant in a time element", () => {
    render(<StatusTimeline entries={[entry({ created_at: "2026-08-03T12:00:00.000Z" })]} />);

    const time = screen.getByRole("listitem").querySelector("time");
    expect(time?.getAttribute("dateTime")).toBe("2026-08-03T12:00:00.000Z");
  });

  // Oldest first, as the API sends them. Reversing it would make the SLA story
  // read backwards — the clock starts at the top.
  it("keeps the order it was given", () => {
    render(
      <StatusTimeline
        entries={[
          entry({ from_status: null, to_status: "open", created_at: "2026-08-03T10:00:00.000Z" }),
          entry({ from_status: "open", to_status: "pending", created_at: "2026-08-03T11:00:00.000Z" }),
          entry({ from_status: "pending", to_status: "resolved", created_at: "2026-08-03T12:00:00.000Z" }),
        ]}
      />,
    );

    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(within(items[0]).getByText(/opened/i)).toBeTruthy();
    expect(items[2].textContent).toContain("resolved");
  });
});
