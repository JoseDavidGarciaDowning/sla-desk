import type { Ticket } from "@/lib/tickets";

/**
 * Who the API says the caller is — the response of GET /api/agent/me.
 *
 * The role comes from our own users table and not from Clerk, which holds none
 * (docs/spec.md §4.3). That is why this endpoint exists at all: the browser
 * cannot ask its session what someone is allowed to do.
 *
 * The id is ours too. Clerk knows a subject and nothing about our users table,
 * so "assign this to me" has no other source for it — T24 needs this field.
 */
export type AgentMe = {
  id: string;
  role: string;
};

/**
 * One row of the agent queue — QueueEntryResponse in the ticket module's
 * transport layer.
 *
 * It extends Ticket rather than restating it, exactly as the Go type embeds
 * TicketResponse: a field added there appears here without anyone remembering
 * to add it, and the two views of a ticket cannot disagree about what a ticket
 * is.
 *
 * There is no requester id, deliberately, and none should be added: the API
 * does not send one. It is another user's primary key, and a queue needs a name
 * to show, not an identifier to enumerate.
 */
export type QueueEntry = Ticket & {
  requester_name: string;
};

/** One page of the queue — QueueResponse in the ticket module. */
export type QueuePage = {
  tickets: QueueEntry[];
  next_cursor: string | null;
};

/**
 * The filters GET /api/agent/tickets accepts, as they travel in the URL.
 *
 * Strings rather than the generated unions, for the reason TicketFilters gives:
 * they arrive from a query string anyone can type, and validating them is the
 * API's job — it answers a 400 naming the field.
 *
 * `assignee` is the one this list has and the customer's does not. It carries
 * three questions in one parameter — "any", "unassigned", "me", or a user id —
 * because a missing value already means "no filter" and cannot also mean
 * "nobody is on it".
 */
export type QueueFilters = {
  status?: string;
  priority?: string;
  assignee?: string;
};

/**
 * The cache keys for the agent's views.
 *
 * A separate tree from ticketKeys, and that is not tidiness. The two lists are
 * different endpoints with different scopes: invalidating the customer's list
 * must not refetch the queue, and a ticket read as an agent is not the same
 * cached value as the same ticket read by its requester — one of them is
 * reachable and the other answers 404.
 */
export const agentKeys = {
  all: ["agent"] as const,
  me: () => [...agentKeys.all, "me"] as const,
  queues: () => [...agentKeys.all, "queue"] as const,
  queue: (filters: QueueFilters) => [...agentKeys.queues(), filters] as const,
  detail: (id: string) => [...agentKeys.all, "detail", id] as const,
  history: (id: string) => [...agentKeys.all, "history", id] as const,
};

/**
 * Builds the query string for a filtered queue request.
 *
 * Empty values are dropped rather than sent, so two identical requests do not
 * look different in a log — the same rule ticketQuery follows.
 */
export function queueQuery(filters: QueueFilters): string {
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(filters)) {
    if (value) params.set(name, value);
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

/** The roles this app treats as staff. Mirrors RequireRole in the API. */
export const AGENT_ROLES = ["agent", "admin"] as const;

export function isStaff(role: string | undefined): boolean {
  return AGENT_ROLES.includes(role as (typeof AGENT_ROLES)[number]);
}
