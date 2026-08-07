import {
  ACTOR_ROLES,
  TRANSITIONS,
  type ActorRole,
  type TicketStatus,
} from "@/lib/contract";
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
export type AgentTicket = Ticket & {
  /**
   * Null when nobody is on it. Explicitly null rather than absent, so a client
   * can tell "nobody" from "this endpoint does not say".
   *
   * It is on the agent's shape and not on Ticket, because the customer's
   * endpoints return the same type and an assignee id there would hand every
   * customer the primary key of the agent working their case.
   */
  assignee_id: string | null;
};

export type QueueEntry = AgentTicket & {
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
/**
 * One person a ticket may be handed to — the `users` member of
 * GET /api/agent/assignable.
 *
 * There is an id here, and it is the only place in this app that carries
 * another user's. The API sends it because PATCH .../assignee needs one; every
 * other view is given a name instead (T14b, T19). Nothing outside the
 * assignment control should read this field.
 *
 * `name` is already resolved by the API: an agent with no name in Clerk arrives
 * as their email address rather than as a blank.
 */
export type AssignableUser = {
  id: string;
  name: string;
  role: ActorRole;
};

/** The roster — GET /api/agent/assignable. */
export type AssignableUsers = {
  users: AssignableUser[];
};

/**
 * The body of POST /api/agent/tickets/{id}/transitions.
 *
 * `reason` is optional and short. It is not a comment — comments are slice 4 —
 * it is the note that explains a move in the timeline.
 */
export type NewTransition = {
  to: TicketStatus;
  reason?: string;
};

/**
 * The body of PATCH /api/agent/tickets/{id}/assignee.
 *
 * `null` unassigns, and it has to be sent explicitly: the API refuses a body
 * with the field absent rather than treating it as an unassignment, so that a
 * client sending {} by accident cannot take somebody off a ticket.
 */
export type NewAssignee = {
  assignee_id: string | null;
};

export const agentKeys = {
  all: ["agent"] as const,
  me: () => [...agentKeys.all, "me"] as const,
  queues: () => [...agentKeys.all, "queue"] as const,
  queue: (filters: QueueFilters) => [...agentKeys.queues(), filters] as const,
  detail: (id: string) => [...agentKeys.all, "detail", id] as const,
  history: (id: string) => [...agentKeys.all, "history", id] as const,

  // The roster is not per-ticket and not per-filter. It changes when somebody
  // is granted a role, which is a deploy-time event (T17), so one key for the
  // whole app is the right granularity.
  assignable: () => [...agentKeys.all, "assignable"] as const,
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

/**
 * The moves `role` may make from `from`, taken from the generated contract.
 *
 * The table is generated rather than transcribed for the reason T13 gives about
 * the categories: the day an edge changes in docs/spec.md §4.1, a transcribed
 * copy leaves the button on screen while the API starts refusing it. A Go test
 * fails while web/lib/contract.ts is stale.
 *
 * The server is still the authority. Anything this lets the UI offer is checked
 * again by domain.Transition, which answers 403 for an edge a role may not take
 * and a field error for one that does not exist — so a stale bundle in
 * somebody's browser cannot make an illegal move, only offer one.
 */
export function allowedTransitions(
  from: TicketStatus,
  role: ActorRole,
): TicketStatus[] {
  const targets = TRANSITIONS[from] ?? {};

  // Object.entries loses the key type, so the cast restores what the contract
  // already guarantees: every key of a TRANSITIONS entry is a TicketStatus.
  return (Object.entries(targets) as [TicketStatus, readonly ActorRole[]][])
    .filter(([, roles]) => roles.includes(role))
    .map(([to]) => to);
}

/**
 * Converts the role our database reports into the one the state machine keys
 * its edges on.
 *
 * They hold the same three strings and are different vocabularies on purpose:
 * `/api/agent/me` answers with the identity module's role — what somebody *is*
 * — while TRANSITIONS is keyed on the ticket module's — what somebody *was when
 * they acted* (docs/adr/0005). Go performs the same conversion in its
 * composition root, exhaustively, for the same reason.
 *
 * Anything unrecognised becomes `customer`, the least privileged. A role added
 * to the database and not accounted for here then offers no moves at all, which
 * is a permission complaint somebody reports rather than a request the API
 * rejects at write time.
 */
export function asActorRole(role: string): ActorRole {
  return ACTOR_ROLES.includes(role as ActorRole)
    ? (role as ActorRole)
    : "customer";
}

/** The roles this app treats as staff. Mirrors RequireRole in the API. */
export const AGENT_ROLES = ["agent", "admin"] as const;

export function isStaff(role: string | undefined): boolean {
  return AGENT_ROLES.includes(role as (typeof AGENT_ROLES)[number]);
}
