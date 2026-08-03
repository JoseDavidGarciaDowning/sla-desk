"use client";

import { useQuery } from "@tanstack/react-query";

import { SlaTimer } from "@/components/sla-timer";
import { StatusTimeline } from "@/components/status-timeline";
import { ApiError } from "@/lib/api";
import { type Ticket, type TicketHistory, ticketKeys } from "@/lib/tickets";
import { useApiFetch } from "@/lib/use-api";

/**
 * One ticket, with the history its SLA clock was rebuilt from.
 *
 * Two queries rather than one endpoint returning both. The ticket is often
 * already in the cache — the create form writes the server's own response there
 * before navigating — and a combined endpoint would throw that away on arrival.
 * They also fail independently: a history that will not load should not blank
 * out the ticket.
 *
 * 404 is what the API answers for a ticket that does not exist *and* for one
 * belonging to someone else, and it cannot tell them apart (docs/spec.md §11).
 * The wording here has to cover both without implying the id names something
 * real.
 */
export function TicketDetail({ id }: { id: string }) {
  const apiFetch = useApiFetch();

  const ticket = useQuery({
    queryKey: ticketKeys.detail(id),
    queryFn: () => apiFetch<Ticket>(`/api/tickets/${id}`),
  });

  const history = useQuery({
    queryKey: ticketKeys.history(id),
    queryFn: () => apiFetch<TicketHistory>(`/api/tickets/${id}/history`),
    // Pointless while the ticket itself is a 404, and it would race the ticket
    // query to render two copies of the same "no such ticket".
    enabled: ticket.isSuccess,
  });

  if (ticket.isPending) {
    return <p className="text-muted-foreground">Loading…</p>;
  }

  if (ticket.error) {
    return (
      <p className="text-destructive" role="alert">
        {ticket.error instanceof ApiError && ticket.error.status === 404
          ? "No such ticket."
          : "This ticket could not be loaded."}
      </p>
    );
  }

  return (
    <article className="space-y-8">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">
          {ticket.data.title}
        </h1>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-muted-foreground text-sm">
            {ticket.data.status} · {ticket.data.priority} · {ticket.data.category}
          </span>
          <SlaTimer
            dueAt={ticket.data.sla_due_at}
            breached={ticket.data.sla_breached}
          />
        </div>
      </header>

      <p className="whitespace-pre-wrap">{ticket.data.description}</p>

      <section className="space-y-3">
        <h2 className="text-sm font-medium">History</h2>

        {history.isPending && (
          <p className="text-muted-foreground text-sm">Loading the history…</p>
        )}

        {/* The ticket stays on screen when only the history fails. It is
            context, not the content — losing it should not look like losing
            the ticket. */}
        {history.error && (
          <p className="text-destructive text-sm" role="alert">
            The history could not be loaded.
          </p>
        )}

        {history.data && <StatusTimeline entries={history.data.entries} />}
      </section>
    </article>
  );
}
