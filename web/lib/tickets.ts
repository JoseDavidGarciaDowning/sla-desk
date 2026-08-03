import type { Category, Priority } from "@/lib/contract";

/**
 * A ticket as the API returns it — mirroring TicketResponse in
 * internal/api/dto.go, field for field and name for name.
 *
 * Declared once, here. docs/spec.md §8 forbids redeclaring response types per
 * component: two copies of a shape drift the same way two copies of a rule do,
 * except the compiler cannot see it because each file agrees with itself.
 *
 * Hand-mirrored rather than generated, which §8 permits. The vocabularies below
 * are generated because a wrong one is invisible until a user hits a 400; a
 * wrong field name here fails to compile the moment anything reads it.
 */
export type Ticket = {
  id: string;
  title: string;
  description: string;
  category: Category;
  priority: Priority;

  // A plain string for now. The status vocabulary lives in
  // internal/ticket/status.go and nothing here branches on it yet; T14 renders
  // the timeline and should generate the union then rather than transcribe it.
  status: string;

  // Null while the clock is paused, which is what makes a paused ticket unable
  // to breach (docs/spec.md §4.2). The frontend renders the absence and
  // performs no deadline arithmetic of its own.
  sla_due_at: string | null;
  sla_breached: boolean;

  created_at: string;
  updated_at: string;
};

/** One page of tickets — TicketListResponse in internal/api/tickets.go. */
export type TicketPage = {
  tickets: Ticket[];
  next_cursor: string | null;
};

/** The body of POST /api/tickets — CreateTicketRequest in internal/api/dto.go. */
export type NewTicket = {
  title: string;
  description: string;
  category: Category;
  priority: Priority;
};

/**
 * The filters GET /api/tickets accepts, as they travel in the URL.
 *
 * Strings rather than the generated unions, because they arrive from a query
 * string that anyone can type. Validating them is the API's job — it answers a
 * 400 naming the field — and duplicating that check here would put the
 * vocabulary in a second place that decides things.
 */
export type TicketFilters = {
  status?: string;
  priority?: string;
};

/**
 * The cache keys, built in one place so a writer and a reader cannot disagree
 * about them.
 *
 * A key typed inline at a call site is a string nobody checks: invalidating
 * ["ticket"] when the query registered ["tickets"] refetches nothing, silently,
 * and the symptom is a stale screen rather than an error.
 *
 * Three levels, and the middle one earns its place. Each combination of filters
 * is a separate cached query, so `lists()` is the prefix they all share and the
 * only thing worth invalidating: targeting `list({})` would refresh the
 * unfiltered view and leave a filtered one stale on screen. Targeting `all`
 * would go too far the other way and mark a freshly seeded detail stale too.
 */
export const ticketKeys = {
  all: ["tickets"] as const,
  lists: () => [...ticketKeys.all, "list"] as const,
  list: (filters: TicketFilters) => [...ticketKeys.lists(), filters] as const,
  detail: (id: string) => [...ticketKeys.all, "detail", id] as const,
  history: (id: string) => [...ticketKeys.all, "history", id] as const,
};

/**
 * Builds the query string for a filtered list request.
 *
 * Empty values are dropped rather than sent. The API treats an empty filter as
 * absent, but sending `?status=` puts a parameter in the URL that means
 * nothing, and it would make two identical requests look different in a log.
 */
export function ticketQuery(filters: TicketFilters): string {
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(filters)) {
    if (value) params.set(name, value);
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

/**
 * One status change — TicketHistoryEntry in internal/api/dto.go.
 *
 * There is no actor id, deliberately, and none should be added: the API does
 * not send one. A timeline says what kind of person moved the ticket, never
 * which one.
 */
export type TicketHistoryEntry = {
  /** Null on the entry that records creation — the only move from nowhere. */
  from_status: string | null;
  to_status: string;
  actor_role: string;
  reason: string | null;
  created_at: string;
};

/** A ticket's timeline, oldest first. */
export type TicketHistory = {
  entries: TicketHistoryEntry[];
};
