"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { SlaTimer } from "@/components/sla-timer";
import { ApiError } from "@/lib/api";
import { type TicketPage, ticketKeys, ticketQuery } from "@/lib/tickets";
import { useApiFetch } from "@/lib/use-api";

/**
 * The customer's own tickets.
 *
 * Scoping is not done here and cannot be: the API filters by requester in SQL,
 * so another customer's row never arrives to be filtered out. That stays true
 * with the status and priority filters applied — they are further predicates on
 * the same query, not a replacement for it.
 *
 * Filtering is likewise the API's, not this component's. Filtering the loaded
 * page in the browser would filter only that page: with pagination the customer
 * would be told they have three open tickets because the other seven were on
 * page two.
 */
export function TicketList() {
  const apiFetch = useApiFetch();
  const params = useSearchParams();

  const filters = {
    status: params.get("status") ?? "",
    priority: params.get("priority") ?? "",
  };
  const isFiltered = Boolean(filters.status || filters.priority);

  const { data, error, isPending } = useQuery({
    // The filters are part of the key, so each combination is cached
    // separately and switching back to one already seen is instant. Without
    // them in the key, every filter change would read the previous filter's
    // rows out of the cache before the refetch landed.
    queryKey: ticketKeys.list(filters),
    queryFn: () => apiFetch<TicketPage>(`/api/tickets${ticketQuery(filters)}`),
  });

  if (isPending) {
    return <p className="text-muted-foreground">Loading your tickets…</p>;
  }

  if (error) {
    return (
      <p className="text-destructive" role="alert">
        {error instanceof ApiError
          ? `The API answered ${error.status}.`
          : "The API could not be reached."}
      </p>
    );
  }

  // Two different empty states. "You have no tickets" is wrong in front of a
  // customer with nine of them who filtered to `closed` — it reads as data
  // loss, and it hides the thing they can actually do about it.
  if (data.tickets.length === 0) {
    return isFiltered ? (
      <p className="text-muted-foreground">
        No tickets match these filters.{" "}
        <Link href="/tickets" className="underline underline-offset-4">
          Clear them
        </Link>{" "}
        to see all of yours.
      </p>
    ) : (
      <p className="text-muted-foreground">
        No tickets yet.{" "}
        <Link href="/tickets/new" className="underline underline-offset-4">
          Raise one
        </Link>{" "}
        and the SLA clock starts.
      </p>
    );
  }

  return (
    <ul className="divide-y rounded-md border">
      {data.tickets.map((ticket) => (
        <li key={ticket.id}>
          <Link
            href={`/tickets/${ticket.id}`}
            className="hover:bg-muted/50 flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-3"
          >
            <span className="min-w-0 flex-1 truncate">{ticket.title}</span>

            <span className="flex shrink-0 items-center gap-3">
              <span className="text-muted-foreground text-sm">
                {ticket.status} · {ticket.priority}
              </span>
              <SlaTimer
                dueAt={ticket.sla_due_at}
                breached={ticket.sla_breached}
              />
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
