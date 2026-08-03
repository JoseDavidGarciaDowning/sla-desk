"use client";

import { useQuery } from "@tanstack/react-query";

import { ApiError } from "@/lib/api";
import { useApiFetch } from "@/lib/use-api";

/** Mirrors TicketResponse in internal/api/dto.go. */
type Ticket = {
  id: string;
  title: string;
  status: string;
  priority: string;
  sla_due_at: string | null;
};

type TicketPage = {
  tickets: Ticket[];
  next_cursor: string | null;
};

/**
 * The first authenticated call this app makes.
 *
 * T14 replaces it with the real list. It exists now because until something
 * calls the API with a token, the token plumbing is code that has never run —
 * and an auth path nobody has exercised is not one to build a form on top of.
 */
export function TicketList() {
  const apiFetch = useApiFetch();

  const { data, error, isPending } = useQuery({
    queryKey: ["tickets"],
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
        No tickets yet — the create form arrives in T13. The API answered, which
        means the session token reached it and it recognised you.
      </p>
    );
  }

  return (
    <ul className="divide-y rounded-md border">
      {data.tickets.map((ticket) => (
        <li key={ticket.id} className="flex justify-between px-4 py-3">
          <span>{ticket.title}</span>
          <span className="text-muted-foreground text-sm">{ticket.status}</span>
        </li>
      ))}
    </ul>
  );
}
