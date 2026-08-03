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
 * The cache keys, built in one place so a writer and a reader cannot disagree
 * about them.
 *
 * A key typed inline at a call site is a string nobody checks: invalidating
 * ["ticket"] when the query registered ["tickets"] refetches nothing, silently,
 * and the symptom is a stale screen rather than an error.
 *
 * `list` and `detail` sit under `all` but are invalidated separately on
 * purpose. Invalidating `all` after a create would also mark the detail we just
 * seeded as stale, and the seeding would buy nothing.
 */
export const ticketKeys = {
  all: ["tickets"] as const,
  list: () => [...ticketKeys.all, "list"] as const,
  detail: (id: string) => [...ticketKeys.all, "detail", id] as const,
};
