"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { ApiError } from "@/lib/api";
import { type TicketPage, ticketKeys } from "@/lib/tickets";
import { useApiFetch } from "@/lib/use-api";

/**
 * The customer's own tickets.
 *
 * Scoping is not done here and cannot be: the API filters by requester in SQL,
 * so another customer's row never arrives to be filtered out.
 *
 * T14 owns the real version — SLA remaining, filters in the URL, pagination.
 */
export function TicketList() {
  const apiFetch = useApiFetch();

  const { data, error, isPending } = useQuery({
    queryKey: ticketKeys.list(),
    queryFn: () => apiFetch<TicketPage>("/api/tickets"),
  });

  if (isPending) {
    return <p className="text-muted-foreground">Loading your tickets…</p>;
  }

  if (error) {
    return (
      <p className="text-destructive">
        {error instanceof ApiError
          ? `The API answered ${error.status}.`
          : "The API could not be reached."}
      </p>
    );
  }

  if (data.tickets.length === 0) {
    return (
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
            className="hover:bg-muted/50 flex items-center justify-between gap-4 px-4 py-3"
          >
            <span className="truncate">{ticket.title}</span>
            <span className="text-muted-foreground shrink-0 text-sm">
              {ticket.status}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
