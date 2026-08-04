"use client";

import { useEffect, useState } from "react";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/**
 * How often the remaining time is recomputed.
 *
 * The display is never finer than a minute, so anything faster re-renders to
 * produce identical text. Half a minute keeps the worst case — a label a full
 * minute out of date — down to something nobody notices.
 */
const TICK_MS = 30_000;

/**
 * Renders how far off a deadline is, coarsely.
 *
 * Two units at most, and never seconds. An SLA measured in hours does not gain
 * anything from a display that changes every second except a reason for the
 * page to re-render, and "1d 4h 32m 09s" is harder to read at a glance than
 * "1d 4h".
 */
function distance(ms: number): string {
  if (ms < MINUTE) return "< 1m";
  if (ms < HOUR) return `${Math.floor(ms / MINUTE)}m`;
  if (ms < DAY) {
    const hours = Math.floor(ms / HOUR);
    const minutes = Math.floor((ms % HOUR) / MINUTE);
    return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
  }
  const days = Math.floor(ms / DAY);
  const hours = Math.floor((ms % DAY) / HOUR);
  return hours === 0 ? `${days}d` : `${days}d ${hours}h`;
}

/**
 * The state of a ticket's SLA clock.
 *
 * **Both inputs come from the API and neither is recomputed here.** The
 * deadline is `sla_due_at`, calculated once by internal/sla from the ticket's
 * own history (docs/spec.md §4.2); this component subtracts it from the current
 * time to say how far away it is, and does no other arithmetic. It does not
 * know the budget, the schedule, or how much time has been consumed, and it
 * must not learn: a second implementation of the clock is a second answer.
 *
 * `breached` is the server's verdict and it wins outright. A browser clock a
 * few minutes out of step would otherwise invent a breach or conceal one, and
 * the customer would be shown something the SLA report disagrees with.
 *
 * A null deadline is a paused clock, and a paused clock cannot breach — that is
 * the whole point of the pair of CHECK constraints in migration 003. Rendering
 * it blank would hide that the ticket is waiting on the customer; rendering it
 * as overdue would claim a deadline that is not running.
 */
export function SlaTimer({
  dueAt,
  breached,
}: {
  dueAt: string | null;
  breached: boolean;
}) {
  // The current time is state, read during render rather than measured there.
  // Calling Date.now() in the body makes the component impure — React may
  // re-render at any moment, and the output would change for reasons unrelated
  // to its props. ESLint's react-hooks/purity rule refuses it, and it is right:
  // it was written that way first and the label silently changed value on
  // renders caused by something else entirely.
  //
  // Making it state also makes the countdown真 live, which a deadline display
  // should be. Rendering it once and never again would leave "2h left" on
  // screen for two hours.
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    // A paused clock has nothing to count down. An interval on it would wake
    // the component up forever to render the same word.
    if (dueAt === null) return;

    const id = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(id);
  }, [dueAt]);

  const labels: string[] = [];

  if (breached) {
    labels.push("Breached");
  }

  if (dueAt === null) {
    labels.push("Paused");
  } else {
    const remaining = new Date(dueAt).getTime() - now;
    labels.push(remaining <= 0 ? "Due now" : `${distance(remaining)} left`);
  }

  return (
    <span
      className={
        breached
          ? "text-destructive text-sm font-medium"
          : "text-muted-foreground text-sm"
      }
    >
      {labels.map((label) => (
        <span key={label} className="after:content-['_·_'] last:after:content-['']">
          {label}
        </span>
      ))}
    </span>
  );
}
