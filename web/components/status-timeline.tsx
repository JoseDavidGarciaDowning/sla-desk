import type { TicketHistoryEntry } from "@/lib/tickets";

/**
 * Formats an instant for a reader.
 *
 * `undefined` as the locale means the reader's own, which is the point — a
 * customer in Buenos Aires should not be shown month-first dates. The exact
 * instant travels separately, in the `dateTime` attribute, which is what
 * anything machine-readable should use and what the tests assert on.
 */
function readable(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

function describeEntry(entry: TicketHistoryEntry): string {
  const who = entry.actor_role.charAt(0).toUpperCase() + entry.actor_role.slice(1);

  // A null origin is the entry recording creation — the one move that came
  // from nowhere. "From null to open" describes a transition that never
  // happened; the ticket was opened.
  if (entry.from_status === null) {
    return `${who} opened it as ${entry.to_status}`;
  }
  return `${who} moved it from ${entry.from_status} to ${entry.to_status}`;
}

/**
 * A ticket's status history, oldest first.
 *
 * This is the fact the SLA clock is rebuilt from (docs/spec.md §4.2) — the
 * `sla_*` columns on a ticket are a cache of it — so it is worth showing as
 * more than decoration. It is the audit trail for why a deadline is what it is.
 *
 * Order is the API's, not this component's. The clock starts at the top and
 * each entry consumes or pauses budget from there, so reversing it would make
 * the story read backwards.
 *
 * There is no actor identity here because the API does not send one. A timeline
 * says what kind of person moved the ticket, never which one.
 */
export function StatusTimeline({ entries }: { entries: TicketHistoryEntry[] }) {
  return (
    <ol className="space-y-4">
      {entries.map((entry, index) => (
        <li
          // The index is safe as a key here and nowhere near it is not: this
          // list is append-only and never reordered, filtered or edited — it is
          // a record of things that already happened.
          key={`${entry.created_at}-${index}`}
          className="border-border relative border-l pb-1 pl-5 last:border-transparent"
        >
          <span className="bg-border absolute top-1.5 -left-[4.5px] size-2 rounded-full" />

          <p className="text-sm">{describeEntry(entry)}</p>

          {entry.reason && (
            <p className="text-muted-foreground mt-1 text-sm italic">
              {entry.reason}
            </p>
          )}

          <time
            dateTime={entry.created_at}
            className="text-muted-foreground mt-1 block text-xs"
          >
            {readable(entry.created_at)}
          </time>
        </li>
      ))}
    </ol>
  );
}
