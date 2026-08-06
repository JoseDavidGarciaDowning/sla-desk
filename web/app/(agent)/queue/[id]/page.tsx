import Link from "next/link";

import { AgentTicketDetail } from "./agent-ticket-detail";

/**
 * One ticket, as an agent works it.
 *
 * `params` is a Promise in this version of Next, and awaiting it is what keeps
 * this a Server Component with no client cost.
 */
export default async function AgentTicketPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;

  return (
    <div className="max-w-3xl space-y-6">
      <Link
        href="/queue"
        className="text-muted-foreground hover:text-foreground text-sm"
      >
        ← Queue
      </Link>

      <AgentTicketDetail id={id} />
    </div>
  );
}
