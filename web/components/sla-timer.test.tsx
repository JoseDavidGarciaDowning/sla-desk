import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { SlaTimer } from "@/components/sla-timer";

const NOW = new Date("2026-08-03T12:00:00Z");

function at(offsetMs: number): string {
  return new Date(NOW.getTime() + offsetMs).toISOString();
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function renderTimer(props: {
  sla_due_at: string | null;
  sla_breached?: boolean;
}) {
  vi.setSystemTime(NOW);
  render(
    <SlaTimer
      dueAt={props.sla_due_at}
      breached={props.sla_breached ?? false}
    />,
  );
}

afterEach(() => {
  vi.useRealTimers();
});

describe("SlaTimer", () => {
  // docs/spec.md §4.2: a paused clock cannot breach. Rendering nothing, or
  // rendering it as expired, are both wrong in a way the customer would act on
  // — one hides that the ticket is waiting on them, the other says they missed
  // a deadline that is not running.
  it("says a paused clock is paused", () => {
    vi.useFakeTimers();
    renderTimer({ sla_due_at: null });

    expect(screen.getByText(/paused/i)).toBeTruthy();
    expect(screen.queryByText(/breached|overdue|left/i)).toBeNull();
  });

  // A paused clock stays paused even on a ticket that breached before it was
  // paused. The breach is a fact about the past; the clock is a fact about now.
  it("says paused even when the ticket has breached", () => {
    vi.useFakeTimers();
    renderTimer({ sla_due_at: null, sla_breached: true });

    expect(screen.getByText(/breached/i)).toBeTruthy();
    expect(screen.getByText(/paused/i)).toBeTruthy();
  });

  it.each([
    [2 * DAY + 4 * HOUR, "2d 4h"],
    [3 * HOUR + 20 * MINUTE, "3h 20m"],
    [45 * MINUTE, "45m"],
    [30_000, "< 1m"],
  ])("renders %d ms away as %s", (offset, want) => {
    vi.useFakeTimers();
    renderTimer({ sla_due_at: at(offset) });

    expect(screen.getByText(new RegExp(want.replace(/[<]/g, "\\$&")))).toBeTruthy();
  });

  // The server decides whether a ticket has breached; this subtraction does
  // not. A browser clock minutes out of step would otherwise invent a breach,
  // or hide one — and the SLA report would disagree with what the customer was
  // shown.
  it("defers to the server's verdict, not to the subtraction", () => {
    vi.useFakeTimers();
    renderTimer({ sla_due_at: at(-2 * HOUR), sla_breached: false });

    expect(screen.queryByText(/breached/i)).toBeNull();
    expect(screen.getByText(/due now/i)).toBeTruthy();
  });

  it("shows a breach the server reported even with time apparently left", () => {
    vi.useFakeTimers();
    renderTimer({ sla_due_at: at(3 * HOUR), sla_breached: true });

    expect(screen.getByText(/breached/i)).toBeTruthy();
  });

  // A deadline display that renders once and never again leaves "2h left" on
  // screen for two hours. The current time is state, updated on an interval,
  // which is also what makes the component pure — reading Date.now() during
  // render made its output change on re-renders caused by anything at all.
  it("counts down as time passes", async () => {
    vi.useFakeTimers();
    renderTimer({ sla_due_at: at(2 * HOUR + MINUTE) });

    expect(screen.getByText(/2h 1m left/)).toBeTruthy();

    await act(async () => {
      vi.advanceTimersByTime(2 * MINUTE);
    });

    expect(screen.getByText(/1h 59m left/)).toBeTruthy();
  });

  // Nothing to count, so nothing should wake the component up. An interval on
  // a paused clock re-renders forever to produce the same word.
  it("sets no interval on a paused clock", () => {
    vi.useFakeTimers();
    const setInterval = vi.spyOn(globalThis, "setInterval");

    renderTimer({ sla_due_at: null });

    expect(setInterval).not.toHaveBeenCalled();
  });
});
