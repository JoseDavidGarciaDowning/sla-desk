"use client";

import { useQuery } from "@tanstack/react-query";

import { SlaTimer } from "@/components/sla-timer";
import { StatusTimeline } from "@/components/status-timeline";
import { agentKeys } from "@/lib/agent";
import { ApiError } from "@/lib/api";
import type { Ticket, TicketHistory } from "@/lib/tickets";
import { useApiFetch } from "@/lib/use-api";

/**
 * One ticket and its whole timeline, for a caller who is not its requester.
 *
 * Two queries rather than one endpoint returning both, for the reason T14b
 * gives: they fail independently, so a history that will not load leaves the
 * ticket on screen — the history is context, not the content.
 *
 * The cache keys are the agent's own and not ticketKeys. The same ticket read
 * as an agent and read by its requester are different answers from different
 * endpoints — one is reachable and the other is a 404 — so they must not share
 * a cache entry.
 *
 * 404 here means the id names no ticket. It does not mean "not yours": the
 * agent endpoints have no requester predicate, which is exactly what the route
 * group's role check is standing in for.
 */
export function AgentTicketDetail({ id }: { id: string }) {
  const apiFetch = useApiFetch();

  const ticket = useQuery({
    queryKey: agentKeys.detail(id),
    queryFn: () => apiFetch<Ticket>(`/api/agent/tickets/${id}`),
  });

  const history = useQuery({
    queryKey: agentKeys.history(id),
    queryFn: () => apiFetch<TicketHistory>(`/api/agent/tickets/${id}/history`),
    // Pointless while the ticket itself is a 404, and it would race the ticket
    // query to render two copies of the same message.
    enabled: ticket.isSuccess,
  });

  if (ticket.isPending) {
    return <p className="text-muted-foreground">Loading the ticket…</p>;
  }

  if (ticket.error) {
    const missing = ticket.error instanceof ApiError && ticket.error.status === 404;

    return (
      <p className="text-destructive" role="alert">
        {missing
          ? "No such ticket."
          : ticket.error instanceof ApiError
            ? `The API answered ${ticket.error.status}.`
            : "The API could not be reached."}
      </p>
    );
  }

  return (
    <article className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">
          {ticket.data.title}
        </h1>
        <div className="text-muted-foreground flex flex-wrap items-center gap-3 text-sm">
          <span>{ticket.data.status}</span>
          <span>·</span>
          <span>{ticket.data.priority}</span>
          <span>·</span>
          <span>{ticket.data.category}</span>
          {/* The same countdown the customer sees and the queue shows. A
              second implementation would be a second place for the same
              arithmetic to be wrong. */}
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

        {history.error && (
          // The ticket stays on screen. This is the half that is context.
          <p className="text-muted-foreground text-sm" role="alert">
            The history could not be loaded.
          </p>
        )}

        {history.data && <StatusTimeline entries={history.data.entries} />}
      </section>
    </article>
  );
}
