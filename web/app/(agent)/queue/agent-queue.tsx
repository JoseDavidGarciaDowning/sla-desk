"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { SlaTimer } from "@/components/sla-timer";
import { type QueuePage, agentKeys, queueQuery } from "@/lib/agent";
import { ApiError } from "@/lib/api";
import { useApiFetch } from "@/lib/use-api";

/**
 * Every ticket, ordered by what breaches next.
 *
 * The ordering is the API's and this component does not touch it. Sorting the
 * loaded page in the browser would sort one page of many — an agent would be
 * shown "the most urgent" while the actual most urgent sat on page two. That is
 * the same lie client-side filtering told in T14a, and the reason the queue
 * query carries an expression index rather than a `.sort()` living here.
 *
 * Nothing here scopes anything either. That endpoint has no requester predicate
 * at all: the guarantee lives in the route being mounted behind a role check
 * (tasks/slice-2/plan.md §C), which is why a customer forcing this URL is
 * refused by the layout and by the API independently of each other.
 */
export function AgentQueue() {
  const apiFetch = useApiFetch();
  const params = useSearchParams();

  const filters = {
    status: params.get("status") ?? "",
    priority: params.get("priority") ?? "",
    assignee: params.get("assignee") ?? "",
  };
  const isFiltered = Boolean(
    filters.status || filters.priority || filters.assignee,
  );

  const { data, error, isPending } = useQuery({
    // The filters are part of the key, so switching back to a combination
    // already seen is instant and no view renders the previous filter's rows
    // while its own request is still in flight.
    queryKey: agentKeys.queue(filters),
    queryFn: () =>
      apiFetch<QueuePage>(`/api/agent/tickets${queueQuery(filters)}`),
  });

  if (isPending) {
    return <p className="text-muted-foreground">Loading the queue…</p>;
  }

  if (error) {
    // 403 gets its own sentence. It is the one error here a person can act on
    // — they are signed in as the wrong account — and "the API answered 403"
    // does not tell them that.
    const forbidden = error instanceof ApiError && error.status === 403;

    return (
      <p className="text-destructive" role="alert">
        {forbidden
          ? "This queue is for agents. You are signed in as a customer."
          : error instanceof ApiError
            ? `The API answered ${error.status}.`
            : "The API could not be reached."}
      </p>
    );
  }

  // Two empty states, as the customer list has. "No tickets" in front of an
  // agent who filtered to `closed` reads as an outage rather than as a filter.
  if (data.tickets.length === 0) {
    return isFiltered ? (
      <p className="text-muted-foreground">
        No tickets match these filters.{" "}
        <Link href="/queue" className="underline underline-offset-4">
          Clear them
        </Link>{" "}
        to see the whole queue.
      </p>
    ) : (
      <p className="text-muted-foreground">
        The queue is empty. Nothing is waiting on anyone.
      </p>
    );
  }

  return (
    <ul className="divide-y rounded-md border">
      {data.tickets.map((entry) => (
        <li key={entry.id}>
          <Link
            href={`/queue/${entry.id}`}
            className="hover:bg-muted/50 flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-3"
          >
            <span className="min-w-0 flex-1">
              <span className="block truncate">{entry.title}</span>
              {/* The requester is what makes this a queue rather than a list.
                  The API sends a name, never an id — another user's primary key
                  is not something an agent needs or should be handed. */}
              <span className="text-muted-foreground block truncate text-sm">
                {entry.requester_name}
              </span>
            </span>

            <span className="flex shrink-0 items-center gap-3">
              <span className="text-muted-foreground text-sm">
                {entry.status} · {entry.priority}
              </span>
              {/* Reused unchanged from the customer views. A second countdown
                  would be a second place for the same arithmetic to be wrong,
                  and the frontend performs none of it: it subtracts an instant
                  the API sent from now, and nothing else (docs/spec.md §4.2). */}
              <SlaTimer dueAt={entry.sla_due_at} breached={entry.sla_breached} />
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
