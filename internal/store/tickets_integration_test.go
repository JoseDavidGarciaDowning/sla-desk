//go:build integration

package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// normalPolicyID is the seeded policy for priority 'normal'. Looked up rather
// than hardcoded, because identity values depend on how often the seed has run.
func normalPolicyID(t *testing.T, ctx testContext) int64 {
	t.Helper()
	p, err := ctx.q.GetActiveSLAPolicyByPriority(ctx.ctx, ticket.PriorityNormal)
	if err != nil {
		t.Fatalf("resolving the normal policy: %v", err)
	}
	return p.ID
}

// testContext bundles what every ticket test needs, so the helpers below do not
// each take four arguments.
type testContext struct {
	ctx context.Context
	tx  pgx.Tx
	q   *store.Queries
}

func setup(t *testing.T) testContext {
	t.Helper()
	c, tx, q := begin(t)
	return testContext{ctx: c, tx: tx, q: q}
}

func newUser(t *testing.T, c testContext, clerkID string, role ticket.Role) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := c.tx.QueryRow(c.ctx,
		`INSERT INTO users (clerk_user_id, email, role) VALUES ($1, $2, $3) RETURNING id`,
		clerkID, clerkID+"@example.test", role,
	).Scan(&id)
	if err != nil {
		t.Fatalf("creating user %s: %v", clerkID, err)
	}
	return id
}

func newTicket(t *testing.T, c testContext, requester pgtype.UUID, title string) store.Ticket {
	t.Helper()
	started := time.Now().UTC()
	due := started.Add(24 * time.Hour)

	tk, err := c.q.CreateTicket(c.ctx, store.CreateTicketParams{
		RequesterID:       requester,
		Title:             title,
		Description:       "body",
		Category:          ticket.CategoryTechnical,
		Priority:          ticket.PriorityNormal,
		SlaPolicyID:       normalPolicyID(t, c),
		SlaClockStartedAt: &started,
		SlaDueAt:          &due,
	})
	if err != nil {
		t.Fatalf("creating ticket %q: %v", title, err)
	}
	return tk
}

// ── The clock invariants from docs/spec.md §4.2 ──────────────────────────────
//
// These are CHECK constraints rather than handler logic, so "a paused ticket
// cannot breach" holds against direct SQL too, not only against our own code.

func TestOpenTicketWithoutARunningClockIsRejected(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_clock_a", ticket.RoleCustomer)

	_, err := c.tx.Exec(c.ctx,
		`INSERT INTO tickets (requester_id, title, description, category, priority, sla_policy_id, status)
		 VALUES ($1, 'No clock', 'body', 'other', 'normal', $2, 'open')`,
		requester, normalPolicyID(t, c))

	if name := rejectedBy(t, err); name != "tickets_clock_runs_only_while_open" {
		t.Errorf("constraint = %q, want tickets_clock_runs_only_while_open", name)
	}
}

func TestPausedTicketWithARunningClockIsRejected(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_clock_b", ticket.RoleCustomer)

	_, err := c.tx.Exec(c.ctx,
		`INSERT INTO tickets (requester_id, title, description, category, priority, sla_policy_id,
		                      status, sla_clock_started_at, sla_due_at)
		 VALUES ($1, 'Paused but ticking', 'body', 'other', 'normal', $2, 'pending', now(), now() + interval '1 hour')`,
		requester, normalPolicyID(t, c))

	if name := rejectedBy(t, err); name != "tickets_clock_runs_only_while_open" {
		t.Errorf("constraint = %q, want tickets_clock_runs_only_while_open", name)
	}
}

// sla_due_at is what the breach worker matches on. A running clock without one
// would be a ticket that consumes budget but can never breach.
func TestRunningClockWithoutADueDateIsRejected(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_clock_c", ticket.RoleCustomer)

	_, err := c.tx.Exec(c.ctx,
		`INSERT INTO tickets (requester_id, title, description, category, priority, sla_policy_id,
		                      status, sla_clock_started_at)
		 VALUES ($1, 'No deadline', 'body', 'other', 'normal', $2, 'open', now())`,
		requester, normalPolicyID(t, c))

	if name := rejectedBy(t, err); name != "tickets_due_at_set_iff_clock_running" {
		t.Errorf("constraint = %q, want tickets_due_at_set_iff_clock_running", name)
	}
}

func TestPausingATicketClearsBothClockColumns(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_clock_d", ticket.RoleCustomer)
	tk := newTicket(t, c, requester, "Will be paused")

	if _, err := c.tx.Exec(c.ctx,
		`UPDATE tickets SET status = 'pending', sla_clock_started_at = NULL, sla_due_at = NULL WHERE id = $1`,
		tk.ID); err != nil {
		t.Fatalf("pausing the ticket should be allowed: %v", err)
	}

	var due *time.Time
	if err := c.tx.QueryRow(c.ctx, `SELECT sla_due_at FROM tickets WHERE id = $1`, tk.ID).Scan(&due); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if due != nil {
		t.Errorf("sla_due_at = %v, want nil — a paused ticket must not be able to breach", *due)
	}
}

// ── The reason the column is microseconds ────────────────────────────────────

// The consistency test in docs/spec.md §9 compares a reconstructed
// time.Duration against this column and must be able to do it with ==. A value
// that is not a whole number of minutes has to survive the round trip intact.
//
// The value is also deliberately larger than an INTEGER can hold in
// microseconds: int32 overflows at about 36 minutes, and the 'low' policy
// budgets 72 hours. BIGINT is a requirement here, not a preference.
func TestConsumedMicrosRoundTripsADurationExactly(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_micros", ticket.RoleCustomer)
	tk := newTicket(t, c, requester, "Odd duration")

	want := 41*time.Hour + 13*time.Minute + 22*time.Second + 123456*time.Microsecond

	if _, err := c.tx.Exec(c.ctx,
		`UPDATE tickets SET sla_consumed_micros = $1 WHERE id = $2`,
		want.Microseconds(), tk.ID); err != nil {
		t.Fatalf("storing the duration: %v", err)
	}

	var micros int64
	if err := c.tx.QueryRow(c.ctx,
		`SELECT sla_consumed_micros FROM tickets WHERE id = $1`, tk.ID).Scan(&micros); err != nil {
		t.Fatalf("reading back: %v", err)
	}

	if got := time.Duration(micros) * time.Microsecond; got != want {
		t.Errorf("round trip = %v, want %v (drift %v)", got, want, got-want)
	}
}

func TestNegativeConsumedTimeIsRejected(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_negative", ticket.RoleCustomer)
	tk := newTicket(t, c, requester, "Negative")

	_, err := c.tx.Exec(c.ctx, `UPDATE tickets SET sla_consumed_micros = -1 WHERE id = $1`, tk.ID)

	if name := rejectedBy(t, err); name != "tickets_sla_consumed_not_negative" {
		t.Errorf("constraint = %q, want tickets_sla_consumed_not_negative", name)
	}
}

// ── Value sets and bounds ────────────────────────────────────────────────────

func TestDatabaseRejectsAnUnknownCategory(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_cat", ticket.RoleCustomer)

	_, err := c.tx.Exec(c.ctx,
		`INSERT INTO tickets (requester_id, title, description, category, priority, sla_policy_id,
		                      status, sla_clock_started_at, sla_due_at)
		 VALUES ($1, 'Bad category', 'body', 'sales', 'normal', $2, 'open', now(), now() + interval '1 hour')`,
		requester, normalPolicyID(t, c))

	if name := rejectedBy(t, err); name != "tickets_category_valid" {
		t.Errorf("constraint = %q, want tickets_category_valid", name)
	}
}

func TestDatabaseRejectsAnUnknownTicketStatus(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_status", ticket.RoleCustomer)
	tk := newTicket(t, c, requester, "Bad status")

	// The clock columns are cleared in the same statement. Leaving them set
	// would also violate tickets_clock_runs_only_while_open, and Postgres does
	// not promise which of two broken constraints it reports — the first
	// version of this test asserted the wrong one and failed. A test that names
	// a constraint has to arrange for exactly one to be violated.
	_, err := c.tx.Exec(c.ctx,
		`UPDATE tickets SET status = 'archived', sla_clock_started_at = NULL, sla_due_at = NULL WHERE id = $1`,
		tk.ID)

	if name := rejectedBy(t, err); name != "tickets_status_valid" {
		t.Errorf("constraint = %q, want tickets_status_valid", name)
	}
}

func TestDatabaseRejectsAnEmptyTitle(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_title", ticket.RoleCustomer)
	tk := newTicket(t, c, requester, "Has a title")

	_, err := c.tx.Exec(c.ctx, `UPDATE tickets SET title = '' WHERE id = $1`, tk.ID)

	if name := rejectedBy(t, err); name != "tickets_title_length" {
		t.Errorf("constraint = %q, want tickets_title_length", name)
	}
}

// ── Referential integrity ────────────────────────────────────────────────────

// No ON DELETE CASCADE anywhere: docs/spec.md §10 forbids hard-deleting a
// ticket, and a cascade would do it from a distance.
func TestDeletingAUserWithTicketsIsRejected(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_with_tickets", ticket.RoleCustomer)
	newTicket(t, c, requester, "Keeps the user alive")

	_, err := c.tx.Exec(c.ctx, `DELETE FROM users WHERE id = $1`, requester)

	if name := rejectedBy(t, err); name != "tickets_requester_id_fkey" {
		t.Errorf("constraint = %q, want tickets_requester_id_fkey", name)
	}
}

func TestDeletingAPolicyInUseIsRejected(t *testing.T) {
	c := setup(t)
	requester := newUser(t, c, "user_policy_fk", ticket.RoleCustomer)
	newTicket(t, c, requester, "Pins the policy")

	_, err := c.tx.Exec(c.ctx, `DELETE FROM sla_policies WHERE id = $1`, normalPolicyID(t, c))

	if name := rejectedBy(t, err); name != "tickets_sla_policy_id_fkey" {
		t.Errorf("constraint = %q, want tickets_sla_policy_id_fkey", name)
	}
}

// ── Tenancy: a customer sees their own tickets and nothing else ──────────────

// The single most important test in this file. docs/spec.md §11 requires that
// customer B asking for customer A's ticket by id gets nothing back — not a row
// the handler is then trusted to reject.
func TestGetTicketForRequesterHidesAnotherCustomersTicket(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_alice", ticket.RoleCustomer)
	bob := newUser(t, c, "user_bob", ticket.RoleCustomer)

	tk := newTicket(t, c, alice, "Alice's private problem")

	// Sanity: Alice can read her own ticket. Without this the test below would
	// also pass if the query were simply broken.
	if _, err := c.q.GetTicketForRequester(c.ctx, store.GetTicketForRequesterParams{
		ID: tk.ID, RequesterID: alice,
	}); err != nil {
		t.Fatalf("Alice cannot read her own ticket: %v", err)
	}

	_, err := c.q.GetTicketForRequester(c.ctx, store.GetTicketForRequesterParams{
		ID: tk.ID, RequesterID: bob,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err = %v, want pgx.ErrNoRows — Bob reached Alice's ticket", err)
	}
}

func TestListTicketsByRequesterExcludesOtherCustomers(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_alice_list", ticket.RoleCustomer)
	bob := newUser(t, c, "user_bob_list", ticket.RoleCustomer)

	newTicket(t, c, alice, "Alice one")
	newTicket(t, c, alice, "Alice two")
	newTicket(t, c, bob, "Bob one")

	got, err := c.q.ListTicketsByRequester(c.ctx, store.ListTicketsByRequesterParams{RequesterID: alice, PageSize: 50})
	if err != nil {
		t.Fatalf("ListTicketsByRequester: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d tickets, want 2", len(got))
	}
	for _, tk := range got {
		if tk.RequesterID != alice {
			t.Errorf("ticket %q belongs to someone else", tk.Title)
		}
	}
}

func TestListTicketsByRequesterReturnsNewestFirst(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_order", ticket.RoleCustomer)

	first := newTicket(t, c, alice, "Older")
	// created_at defaults to now(), which is the transaction timestamp and
	// therefore identical for both rows. Move one back so the ordering has
	// something real to sort on.
	if _, err := c.tx.Exec(c.ctx,
		`UPDATE tickets SET created_at = now() - interval '1 day' WHERE id = $1`, first.ID); err != nil {
		t.Fatalf("backdating: %v", err)
	}
	newTicket(t, c, alice, "Newer")

	got, err := c.q.ListTicketsByRequester(c.ctx, store.ListTicketsByRequesterParams{RequesterID: alice, PageSize: 50})
	if err != nil {
		t.Fatalf("ListTicketsByRequester: %v", err)
	}
	if len(got) != 2 || got[0].Title != "Newer" {
		t.Errorf("order = %v, want Newer first", titles(got))
	}
}

func titles(tickets []store.Ticket) []string {
	out := make([]string, len(tickets))
	for i, tk := range tickets {
		out[i] = tk.Title
	}
	return out
}

// ── Status history ──────────────────────────────────────────────────────────

// The creation row is the only one with no previous status.
func TestCreationHistoryRowHasNoFromStatus(t *testing.T) {
	c := setup(t)
	actor := newUser(t, c, "user_history_create", ticket.RoleCustomer)
	tk := newTicket(t, c, actor, "Fresh")

	row, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
		TicketID:   tk.ID,
		FromStatus: nil,
		ToStatus:   ticket.StatusOpen,
		ActorID:    actor,
		ActorRole:  ticket.RoleCustomer,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("inserting the creation row: %v", err)
	}
	if row.FromStatus != nil {
		t.Errorf("from_status = %q, want nil", *row.FromStatus)
	}
	if row.Reason != nil {
		t.Errorf("reason = %q, want nil", *row.Reason)
	}
}

// A move to the status a ticket is already in is a no-op, and recording it
// would pad the history the SLA clock is rebuilt from.
func TestSelfTransitionIsRejected(t *testing.T) {
	c := setup(t)
	actor := newUser(t, c, "user_self_transition", ticket.RoleCustomer)
	tk := newTicket(t, c, actor, "Self")

	_, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
		TicketID:   tk.ID,
		FromStatus: ptr(ticket.StatusOpen),
		ToStatus:   ticket.StatusOpen,
		ActorID:    actor,
		ActorRole:  ticket.RoleCustomer,
		CreatedAt:  time.Now().UTC(),
	})

	if name := rejectedBy(t, err); name != "ticket_status_history_is_a_real_transition" {
		t.Errorf("constraint = %q, want ticket_status_history_is_a_real_transition", name)
	}
}

// Note on what this does NOT cover: the query orders by `created_at, id`, and
// the `id` tiebreaker is not verified by any test here, deliberately.
//
// Removing it from the query leaves every test in this file green. Rows written
// in one transaction share created_at, and Postgres returns those ties in
// insertion order through every plan a test can provoke — including after an
// UPDATE that moves the tuple, because a HOT update leaves the index entry
// pointing at the original item and the scan follows the chain to the new
// version. Verified by reading ctid before and after, and the plan (Bitmap Heap
// Scan on ticket_status_history_by_ticket).
//
// The tiebreaker stays because Postgres guarantees no order for equal sort
// keys, and clock reconstruction walks this sequence. Correctness is not
// defined by what a test can reach. What is testable is that created_at is the
// primary key of the ordering, which the next test does cover.
func TestHistoryComesBackInTransitionOrder(t *testing.T) {
	c := setup(t)
	actor := newUser(t, c, "user_history_order", ticket.RoleAgent)
	tk := newTicket(t, c, actor, "Bounces")

	sequence := []struct {
		from *ticket.Status
		to   ticket.Status
	}{
		{nil, ticket.StatusOpen},
		{ptr(ticket.StatusOpen), ticket.StatusPending},
		{ptr(ticket.StatusPending), ticket.StatusOpen},
		{ptr(ticket.StatusOpen), ticket.StatusResolved},
	}
	for _, s := range sequence {
		if _, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
			TicketID: tk.ID, FromStatus: s.from, ToStatus: s.to,
			ActorID: actor, ActorRole: ticket.RoleAgent, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("inserting %v -> %s: %v", s.from, s.to, err)
		}
	}

	got, err := c.q.ListTicketStatusHistory(c.ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListTicketStatusHistory: %v", err)
	}
	if len(got) != len(sequence) {
		t.Fatalf("returned %d rows, want %d", len(got), len(sequence))
	}
	for i, want := range sequence {
		if got[i].ToStatus != want.to {
			t.Errorf("row %d to_status = %q, want %q", i, got[i].ToStatus, want.to)
		}
	}
}

// Event time decides the sequence, not insertion order. Two transitions written
// by concurrent transactions can land with ids in the opposite order to their
// timestamps, and the SLA clock measures intervals between timestamps — so
// created_at has to be what the query sorts on.
func TestHistoryIsOrderedByEventTimeNotByID(t *testing.T) {
	c := setup(t)
	actor := newUser(t, c, "user_history_backdated", ticket.RoleAgent)
	tk := newTicket(t, c, actor, "Out of order")

	first, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
		TicketID: tk.ID, FromStatus: nil, ToStatus: ticket.StatusOpen,
		ActorID: actor, ActorRole: ticket.RoleAgent, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("first row: %v", err)
	}
	second, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
		TicketID: tk.ID, FromStatus: ptr(ticket.StatusOpen), ToStatus: ticket.StatusPending,
		ActorID: actor, ActorRole: ticket.RoleAgent, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("second row: %v", err)
	}

	// Give the higher id the earlier timestamp, so ordering by id and ordering
	// by created_at disagree.
	if _, err := c.tx.Exec(c.ctx,
		`UPDATE ticket_status_history SET created_at = now() - interval '1 hour' WHERE id = $1`,
		second.ID); err != nil {
		t.Fatalf("backdating the second row: %v", err)
	}

	got, err := c.q.ListTicketStatusHistory(c.ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListTicketStatusHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d rows, want 2", len(got))
	}
	if got[0].ID != second.ID {
		t.Errorf("first row id = %d, want %d — the query is ordering by id, not by event time",
			got[0].ID, second.ID)
	}
	if got[1].ID != first.ID {
		t.Errorf("second row id = %d, want %d", got[1].ID, first.ID)
	}
}

// The role is a snapshot of what the actor was at the time. Promoting someone
// later must not rewrite what the audit trail says they were when they acted.
func TestActorRoleIsRecordedIndependentlyOfTheUsersCurrentRole(t *testing.T) {
	c := setup(t)
	actor := newUser(t, c, "user_promoted", ticket.RoleCustomer)
	tk := newTicket(t, c, actor, "Acted on as a customer")

	if _, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
		TicketID: tk.ID, FromStatus: nil, ToStatus: ticket.StatusOpen,
		ActorID: actor, ActorRole: ticket.RoleCustomer, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("inserting history: %v", err)
	}

	if _, err := c.tx.Exec(c.ctx, `UPDATE users SET role = 'agent' WHERE id = $1`, actor); err != nil {
		t.Fatalf("promoting: %v", err)
	}

	rows, err := c.q.ListTicketStatusHistory(c.ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListTicketStatusHistory: %v", err)
	}
	if rows[0].ActorRole != ticket.RoleCustomer {
		t.Errorf("actor_role = %q, want customer — the promotion rewrote history", rows[0].ActorRole)
	}
}

func TestHistoryForAnUnknownTicketIsEmpty(t *testing.T) {
	c := setup(t)

	var unknown pgtype.UUID
	if err := c.tx.QueryRow(c.ctx, `SELECT gen_random_uuid()`).Scan(&unknown); err != nil {
		t.Fatalf("generating an id: %v", err)
	}

	rows, err := c.q.ListTicketStatusHistory(c.ctx, unknown)
	if err != nil {
		t.Fatalf("ListTicketStatusHistory: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("returned %d rows, want none", len(rows))
	}
}

// newTicketWith creates a ticket at a given priority, and optionally moves it
// out of open. Moving it is a direct UPDATE rather than a transition because
// what is under test here is the query's WHERE clause, not the clock: going
// through TicketRepo.Transition would drag the whole SLA reconstruction into a
// test about filtering.
//
// Both clock columns are cleared along with the status, because the CHECK
// constraints in migration 003 refuse a paused ticket that still has a running
// clock. The test would fail on the constraint rather than on the filter.
func newTicketWith(t *testing.T, c testContext, requester pgtype.UUID, title string,
	priority ticket.Priority, status ticket.Status,
) store.Ticket {
	t.Helper()

	started := time.Now().UTC()
	due := started.Add(24 * time.Hour)

	tk, err := c.q.CreateTicket(c.ctx, store.CreateTicketParams{
		RequesterID:       requester,
		Title:             title,
		Description:       "body",
		Category:          ticket.CategoryTechnical,
		Priority:          priority,
		SlaPolicyID:       policyIDFor(t, c, priority),
		SlaClockStartedAt: &started,
		SlaDueAt:          &due,
	})
	if err != nil {
		t.Fatalf("creating ticket %q: %v", title, err)
	}

	if status == ticket.StatusOpen {
		return tk
	}

	_, err = c.tx.Exec(c.ctx,
		`UPDATE tickets
		    SET status = $2, sla_clock_started_at = NULL, sla_due_at = NULL
		  WHERE id = $1`,
		tk.ID, status)
	if err != nil {
		t.Fatalf("moving ticket %q to %s: %v", title, status, err)
	}
	tk.Status = status
	return tk
}

func policyIDFor(t *testing.T, c testContext, priority ticket.Priority) int64 {
	t.Helper()
	p, err := c.q.GetActiveSLAPolicyByPriority(c.ctx, priority)
	if err != nil {
		t.Fatalf("resolving the %s policy: %v", priority, err)
	}
	return p.ID
}

func titlesOf(rows []store.Ticket) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}

// Filtering is done in SQL rather than in the browser. A page filtered on the
// client only filters the rows already loaded, so with pagination it lies: the
// customer sees "3 open tickets" because the other seven were on page two.
func TestListTicketsByRequesterFiltersByStatusAndPriority(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_alice_filters", ticket.RoleCustomer)

	newTicketWith(t, c, alice, "open normal", ticket.PriorityNormal, ticket.StatusOpen)
	newTicketWith(t, c, alice, "open urgent", ticket.PriorityUrgent, ticket.StatusOpen)
	newTicketWith(t, c, alice, "pending normal", ticket.PriorityNormal, ticket.StatusPending)
	newTicketWith(t, c, alice, "pending urgent", ticket.PriorityUrgent, ticket.StatusPending)

	for _, tc := range []struct {
		name     string
		status   *string
		priority *string
		want     []string
	}{
		{
			name: "no filter returns everything",
			want: []string{"open normal", "open urgent", "pending normal", "pending urgent"},
		},
		{
			name:   "status alone",
			status: ptr(string(ticket.StatusOpen)),
			want:   []string{"open normal", "open urgent"},
		},
		{
			name:     "priority alone",
			priority: ptr(string(ticket.PriorityUrgent)),
			want:     []string{"open urgent", "pending urgent"},
		},
		{
			name:     "both, which intersect",
			status:   ptr(string(ticket.StatusPending)),
			priority: ptr(string(ticket.PriorityUrgent)),
			want:     []string{"pending urgent"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.q.ListTicketsByRequester(c.ctx, store.ListTicketsByRequesterParams{
				RequesterID: alice,
				Status:      tc.status,
				Priority:    tc.priority,
				PageSize:    50,
			})
			if err != nil {
				t.Fatalf("ListTicketsByRequester: %v", err)
			}

			titles := titlesOf(got)
			if len(titles) != len(tc.want) {
				t.Fatalf("got %v, want %v", titles, tc.want)
			}
			for _, want := range tc.want {
				if !slices.Contains(titles, want) {
					t.Errorf("%q is missing from %v", want, titles)
				}
			}
		})
	}
}

// The requester predicate has to survive every filter, and every combination of
// them. A WHERE clause is exactly where an AND becomes an OR, and the failure is
// silent: the customer sees somebody else's ticket and nothing errors.
//
// One case per filter, not one case for the pair. Written first with only the
// priority filter, this missed a mutation that bypassed the requester whenever
// a *status* filter was present — the query was scoped for the case the test
// happened to exercise and open for the one it did not.
func TestFilteringNeverReachesAnotherCustomersTickets(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_alice_scope", ticket.RoleCustomer)
	bob := newUser(t, c, "user_bob_scope", ticket.RoleCustomer)

	// Everything Bob has is what Alice's filters ask for. Everything Alice has
	// is something else, so any row coming back is Bob's.
	newTicketWith(t, c, bob, "bob's urgent open", ticket.PriorityUrgent, ticket.StatusOpen)
	newTicketWith(t, c, alice, "alice's low pending", ticket.PriorityLow, ticket.StatusPending)

	for _, tc := range []struct {
		name     string
		status   *string
		priority *string
	}{
		{name: "no filter"},
		{name: "status only", status: ptr(string(ticket.StatusOpen))},
		{name: "priority only", priority: ptr(string(ticket.PriorityUrgent))},
		{
			name:     "both",
			status:   ptr(string(ticket.StatusOpen)),
			priority: ptr(string(ticket.PriorityUrgent)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.q.ListTicketsByRequester(c.ctx, store.ListTicketsByRequesterParams{
				RequesterID: alice,
				Status:      tc.status,
				Priority:    tc.priority,
				PageSize:    50,
			})
			if err != nil {
				t.Fatalf("ListTicketsByRequester: %v", err)
			}

			for _, row := range got {
				if row.RequesterID != alice {
					t.Errorf("returned %q, which is not Alice's", row.Title)
				}
			}
		})
	}
}

// ── The history read for the timeline (T14b) ─────────────────────────────────

// The requester predicate lives in the JOIN, not in a check the handler could
// forget. Same reason as GetTicketForRequester: a missed check in Go must not
// be enough to leak another customer's ticket (docs/spec.md §4.3).
func TestHistoryForRequesterHidesAnotherCustomersTicket(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_alice_history", ticket.RoleCustomer)
	bob := newUser(t, c, "user_bob_history", ticket.RoleCustomer)

	bobs := newTicket(t, c, bob, "bob's ticket")
	writeHistory(t, c, bobs.ID, bob, nil, ticket.StatusOpen)

	got, err := c.q.ListTicketStatusHistoryForRequester(c.ctx,
		store.ListTicketStatusHistoryForRequesterParams{
			TicketID:    bobs.ID,
			RequesterID: alice,
		})
	if err != nil {
		t.Fatalf("ListTicketStatusHistoryForRequester: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("got %d rows of Bob's history, want none", len(got))
	}
}

// Which is what lets the handler answer 404 without being able to tell "no such
// ticket" from "not yours" — it cannot, because the query returns nothing for
// both. An existing ticket always has at least the row recording its creation,
// so an empty history means one of those two and never a real ticket.
func TestHistoryForRequesterReturnsTheOwnersRowsInOrder(t *testing.T) {
	c := setup(t)
	alice := newUser(t, c, "user_alice_own_history", ticket.RoleCustomer)

	tk := newTicket(t, c, alice, "alice's ticket")
	writeHistory(t, c, tk.ID, alice, nil, ticket.StatusOpen)
	writeHistory(t, c, tk.ID, alice, ptr(ticket.StatusOpen), ticket.StatusPending)
	writeHistory(t, c, tk.ID, alice, ptr(ticket.StatusPending), ticket.StatusResolved)

	got, err := c.q.ListTicketStatusHistoryForRequester(c.ctx,
		store.ListTicketStatusHistoryForRequesterParams{
			TicketID:    tk.ID,
			RequesterID: alice,
		})
	if err != nil {
		t.Fatalf("ListTicketStatusHistoryForRequester: %v", err)
	}

	want := []ticket.Status{ticket.StatusOpen, ticket.StatusPending, ticket.StatusResolved}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i, status := range want {
		if got[i].ToStatus != status {
			t.Errorf("row %d is %s, want %s — the timeline reads in order", i, got[i].ToStatus, status)
		}
	}
	if got[0].FromStatus != nil {
		t.Errorf("the creation row has from_status %v, want null", *got[0].FromStatus)
	}
}

// writeHistory inserts a history row directly. Direct rather than through
// TicketRepo.Transition because what is under test is the read, and going
// through the write path would drag the SLA reconstruction into it.
func writeHistory(t *testing.T, c testContext, ticketID, actor pgtype.UUID,
	from *ticket.Status, to ticket.Status,
) {
	t.Helper()

	_, err := c.q.InsertTicketStatusHistory(c.ctx, store.InsertTicketStatusHistoryParams{
		TicketID:   ticketID,
		FromStatus: from,
		ToStatus:   to,
		ActorID:    actor,
		ActorRole:  ticket.RoleCustomer,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("inserting history %v -> %s: %v", from, to, err)
	}
}
