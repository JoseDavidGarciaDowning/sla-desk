"use client";

import { useQuery } from "@tanstack/react-query";

import { ApiError } from "@/lib/api";
import { type Ticket, ticketKeys } from "@/lib/tickets";
import { useApiFetch } from "@/lib/use-api";

/**
 * One ticket, read from the cache when it is already there.
 *
 * Arriving from the create form, it is: the mutation wrote the server's own
 * response under this key, so this renders on the first frame with no request
 * at all. Arriving from a link or a reload, the key is empty and it fetches.
 * Same component either way — the seeding is an optimisation the view does not
 * have to know about.
 *
 * T14 replaces the body of this with the real view: the SLA timer, the status
 * timeline, the transition controls.
 */
export function TicketDetail({ id }: { id: string }) {
  const apiFetch = useApiFetch();

  const { data, error, isPending } = useQuery({
    queryKey: ticketKeys.detail(id),
    queryFn: () => apiFetch<Ticket>(`/api/tickets/${id}`),
  });

  if (isPending) {
    return <p className="text-muted-foreground">Loading…</p>;
  }

  if (error) {
    // 404 is what the API answers for someone else's ticket as well as for one
    // that does not exist (docs/spec.md §11). The wording has to cover both
    // without implying the id names something real.
    const message =
      error instanceof ApiError && error.status === 404
        ? "No such ticket."
        : "This ticket could not be loaded.";
    return <p className="text-destructive">{message}</p>;
  }

  return (
    <article className="space-y-4">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">{data.title}</h1>
        <p className="text-muted-foreground text-sm">
          {data.status} · {data.priority} · {data.category}
        </p>
      </div>

      <p className="whitespace-pre-wrap">{data.description}</p>
    </article>
  );
}
