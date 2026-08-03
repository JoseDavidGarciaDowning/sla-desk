import Link from "next/link";

import { TicketDetail } from "./ticket-detail";

/**
 * A single ticket.
 *
 * `/tickets/new` sits alongside this dynamic segment and wins: Next matches
 * static segments before dynamic ones, so the form is never mistaken for a
 * ticket whose id is the word "new".
 *
 * Deliberately thin. T14 owns this view — the SLA timer, the status timeline,
 * the transition controls. What is here is what T13 needs to be honest: the
 * form navigates to the ticket it created, so that ticket has to have somewhere
 * to arrive.
 *
 * `params` is a Promise in this version of Next, and awaiting it is what makes
 * this page a Server Component with no client cost.
 */
export default async function TicketPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;

  return (
    <div className="max-w-2xl space-y-6">
      <Link
        href="/tickets"
        className="text-muted-foreground hover:text-foreground text-sm"
      >
        ← Your tickets
      </Link>

      <TicketDetail id={id} />
    </div>
  );
}
