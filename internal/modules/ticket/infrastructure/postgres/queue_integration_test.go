//go:build integration

// The agent queue: the first read in this project that is not scoped to whoever
// is asking. These tests are what stands in for the requester predicate every
// other read carries — see tasks/slice-2/plan.md decision B.
//
// They run against the generated query rather than through Repository, because
// Repository takes a *pgxpool.Pool — it owns where write transactions begin —
// while every test here runs inside one transaction that is rolled back. What
// is under test is the SQL: the ordering, the filters and the cursor. The
// mapping above it is thin and covered by unit tests.
package postgres_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

// pausedPosition mirrors tickethttp.PausedPosition, and is duplicated rather
// than imported: this package must not depend on the transport layer, and a
// test reaching for it would be the boundary breaking in a test — which is
// where it always breaks first, because "it is only a test" is how every such
// import gets justified.
var pausedPosition = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)

func anyQueue(size int32) ticketdb.ListTicketsForQueueParams {
	return ticketdb.ListTicketsForQueueParams{AssigneeFilter: "any", PageSize: size}
}

func queueTitles(rows []ticketdb.ListTicketsForQueueRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}

// position is where a row sorts: its deadline, or the sentinel when it has
// none. This is what a cursor has to carry.
func position(dueAt *time.Time) time.Time {
	if dueAt == nil {
		return pausedPosition
	}
	return *dueAt
}

// The whole reason the queue exists. Every other read in this module answers
// "is this yours"; this one must answer for tickets that are not.
func TestTheQueueReturnsTicketsBelongingToEveryCustomer(t *testing.T) {
	c := setup(t)

	alice := newUser(t, c, "user_q_alice", domain.RoleCustomer)
	bob := newUser(t, c, "user_q_bob", domain.RoleCustomer)
	newTicket(t, c, alice, "alice's ticket")
	newTicket(t, c, bob, "bob's ticket")

	got, err := c.q.ListTicketsForQueue(c.ctx, anyQueue(50))
	if err != nil {
		t.Fatalf("ListTicketsForQueue: %v", err)
	}

	seen := map[string]bool{}
	for _, title := range queueTitles(got) {
		seen[title] = true
	}
	for _, want := range []string{"alice's ticket", "bob's ticket"} {
		if !seen[want] {
			t.Errorf("%q missing from the queue — it is scoped to someone", want)
		}
	}
}

// A queue is read to find what breaches next. Ordering by anything else makes
// the agent hunt for it, and sorting the loaded page in the browser sorts one
// page of many — the lie T14a already rejected for filters.
func TestTheQueueIsOrderedByDeadlineWithPausedTicketsLast(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_q_order", domain.RoleCustomer)

	// The deadlines are set explicitly rather than left to the priority.
	// newTicketWith stamps every ticket with the same 24-hour deadline whatever
	// its priority — the policy id varies, sla_due_at does not — so seeding an
	// urgent and a low one would produce a tie broken by a random uuid, and
	// this test would pass or fail by coin flip.
	now := time.Now().UTC()
	dueIn := func(title string, d time.Duration) {
		tk := newTicketWith(t, c, customer, title, domain.PriorityNormal, domain.StatusOpen)
		at := now.Add(d)
		if _, err := c.tx.Exec(c.ctx,
			`UPDATE tickets SET sla_due_at = $2 WHERE id = $1`, tk.ID, at); err != nil {
			t.Fatalf("setting the deadline for %q: %v", title, err)
		}
	}

	dueIn("low", 72*time.Hour)
	dueIn("urgent", time.Hour)
	newTicketWith(t, c, customer, "paused", domain.PriorityNormal, domain.StatusPending)

	got, err := c.q.ListTicketsForQueue(c.ctx, anyQueue(50))
	if err != nil {
		t.Fatalf("ListTicketsForQueue: %v", err)
	}

	var order []string
	for _, title := range queueTitles(got) {
		switch title {
		case "low", "urgent", "paused":
			order = append(order, title)
		}
	}

	want := []string{"urgent", "low", "paused"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v — a paused ticket cannot breach, so it sorts last", order, want)
		}
	}
}

// One case per filter. T14a's mutation testing caught a query that was scoped
// for the filter a test happened to exercise and open for the one it did not,
// so "the filters work" is not a claim one case can support.
func TestEachQueueFilterReturnsExactlyItsSet(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_q_filters", domain.RoleCustomer)
	agent := newUser(t, c, "user_q_agent", domain.RoleAgent)

	newTicketWith(t, c, customer, "open urgent", domain.PriorityUrgent, domain.StatusOpen)
	newTicketWith(t, c, customer, "pending low", domain.PriorityLow, domain.StatusPending)
	assigned := newTicketWith(t, c, customer, "assigned", domain.PriorityNormal, domain.StatusOpen)

	if _, err := c.tx.Exec(c.ctx,
		`UPDATE tickets SET assignee_id = $2 WHERE id = $1`, assigned.ID, agent); err != nil {
		t.Fatalf("assigning: %v", err)
	}

	open := string(domain.StatusOpen)
	urgent := string(domain.PriorityUrgent)

	cases := []struct {
		name    string
		params  ticketdb.ListTicketsForQueueParams
		want    string
		exclude string
	}{
		{
			name:    "status",
			params:  ticketdb.ListTicketsForQueueParams{AssigneeFilter: "any", Status: &open, PageSize: 50},
			want:    "open urgent",
			exclude: "pending low",
		},
		{
			name:    "priority",
			params:  ticketdb.ListTicketsForQueueParams{AssigneeFilter: "any", Priority: &urgent, PageSize: 50},
			want:    "open urgent",
			exclude: "pending low",
		},
		{
			name:    "assignee: one",
			params:  ticketdb.ListTicketsForQueueParams{AssigneeFilter: "one", AssigneeID: &agent, PageSize: 50},
			want:    "assigned",
			exclude: "open urgent",
		},
		{
			name:    "assignee: unassigned",
			params:  ticketdb.ListTicketsForQueueParams{AssigneeFilter: "unassigned", PageSize: 50},
			want:    "open urgent",
			exclude: "assigned",
		},
		{
			name: "status and priority together",
			params: ticketdb.ListTicketsForQueueParams{
				AssigneeFilter: "any", Status: &open, Priority: &urgent, PageSize: 50,
			},
			want:    "open urgent",
			exclude: "assigned",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.q.ListTicketsForQueue(c.ctx, tc.params)
			if err != nil {
				t.Fatalf("ListTicketsForQueue: %v", err)
			}

			seen := map[string]bool{}
			for _, title := range queueTitles(got) {
				seen[title] = true
			}
			if !seen[tc.want] {
				t.Errorf("%q missing", tc.want)
			}
			if seen[tc.exclude] {
				t.Errorf("%q present, but the filter excludes it", tc.exclude)
			}
		})
	}
}

// The trap the whole design is arranged around. A paused ticket's sort position
// is the sentinel, and a cursor carrying its absent deadline would make the next
// page's row comparison NULL — dropping every paused ticket, silently. Walking
// past the running ones must still reach them.
func TestPagingReachesThePausedTicketsAtTheEnd(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_q_paging", domain.RoleCustomer)
	newTicketWith(t, c, customer, "running", domain.PriorityUrgent, domain.StatusOpen)
	newTicketWith(t, c, customer, "paused one", domain.PriorityUrgent, domain.StatusPending)
	newTicketWith(t, c, customer, "paused two", domain.PriorityLow, domain.StatusPending)

	// One row at a time, the way a client walks it.
	seen := map[string]bool{}
	params := anyQueue(1)
	for range 20 {
		page, err := c.q.ListTicketsForQueue(c.ctx, params)
		if err != nil {
			t.Fatalf("ListTicketsForQueue: %v", err)
		}
		if len(page) == 0 {
			break
		}

		last := page[len(page)-1]
		seen[last.Title] = true

		pos := position(last.SlaDueAt)
		id := last.ID
		params.AfterDueAt = &pos
		params.AfterID = &id
	}

	for _, want := range []string{"running", "paused one", "paused two"} {
		if !seen[want] {
			t.Errorf("%q was never reached by paging", want)
		}
	}
}

// A page must not hand out a row it already handed out.
func TestPagingDoesNotRepeatARow(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_q_norepeat", domain.RoleCustomer)
	for _, p := range []domain.Priority{domain.PriorityUrgent, domain.PriorityHigh, domain.PriorityNormal} {
		newTicketWith(t, c, customer, "t-"+string(p), p, domain.StatusOpen)
	}

	first, err := c.q.ListTicketsForQueue(c.ctx, anyQueue(2))
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first page has %d rows, want 2", len(first))
	}

	last := first[1]
	pos := position(last.SlaDueAt)
	id := last.ID

	params := anyQueue(2)
	params.AfterDueAt = &pos
	params.AfterID = &id

	second, err := c.q.ListTicketsForQueue(c.ctx, params)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	for _, a := range first {
		for _, b := range second {
			if a.ID == b.ID {
				t.Errorf("%q appears on both pages", a.Title)
			}
		}
	}
}

// A queue of uuids is not usable. The name comes from a join, because it
// belongs to the identity module's table and this read needs it on screen.
func TestTheQueueJoinsTheRequestersNameAndEmail(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_q_named", domain.RoleCustomer)
	if _, err := c.tx.Exec(c.ctx,
		`UPDATE users SET name = 'Ada Lovelace' WHERE id = $1`, customer); err != nil {
		t.Fatalf("naming the user: %v", err)
	}
	newTicket(t, c, customer, "named ticket")

	got, err := c.q.ListTicketsForQueue(c.ctx, anyQueue(50))
	if err != nil {
		t.Fatalf("ListTicketsForQueue: %v", err)
	}

	for _, row := range got {
		if row.Title != "named ticket" {
			continue
		}
		if row.RequesterName == nil || *row.RequesterName != "Ada Lovelace" {
			t.Errorf("RequesterName = %v, want Ada Lovelace", row.RequesterName)
		}
		// Both are carried. Clerk holds no name for someone who signed up with
		// an email and a password, and which one to show is decided in the DTO.
		if row.RequesterEmail != "user_q_named@example.test" {
			t.Errorf("RequesterEmail = %q", row.RequesterEmail)
		}
		return
	}
	t.Fatal("the seeded ticket is not in the queue")
}

// The customer's read must be untouched by all of this. An agent queue that
// quietly widened the scoped query would pass every test above.
func TestTheCustomerListStillExcludesOtherCustomers(t *testing.T) {
	c := setup(t)

	alice := newUser(t, c, "user_q_still_alice", domain.RoleCustomer)
	bob := newUser(t, c, "user_q_still_bob", domain.RoleCustomer)
	newTicket(t, c, alice, "alice only")
	newTicket(t, c, bob, "bob only")

	got, err := c.q.ListTicketsByRequester(c.ctx, ticketdb.ListTicketsByRequesterParams{
		RequesterID: alice,
		PageSize:    50,
	})
	if err != nil {
		t.Fatalf("ListTicketsByRequester: %v", err)
	}

	for _, tk := range got {
		if tk.RequesterID != alice {
			t.Fatalf("the customer list returned %q, which belongs to someone else", tk.Title)
		}
	}
}

// --- The agent's single-ticket reads (T20) --------------------------------

// The unscoped counterpart to GetTicketForRequester. An agent opens a ticket
// that belongs to a customer, which is the entire job.
func TestATicketIsReadableWithoutBeingItsRequester(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_d_customer", domain.RoleCustomer)
	tk := newTicket(t, c, customer, "someone else's ticket")

	got, err := c.q.GetTicketByID(c.ctx, tk.ID)
	if err != nil {
		t.Fatalf("GetTicketByID: %v", err)
	}
	if got.Title != "someone else's ticket" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.RequesterID != customer {
		t.Errorf("the row came back attached to the wrong requester")
	}
}

// An id that names nothing returns no rows, which the handler turns into a 404.
// The agent group answering 403 to a customer does not change this: that is
// about the prefix, and this is about a specific ticket (docs/spec.md §11).
func TestAnUnknownTicketIDReturnsNoRows(t *testing.T) {
	c := setup(t)

	// A ticket has to exist for this to mean anything. Without one the table is
	// empty inside this transaction, "no rows" is true for any query at all,
	// and a predicate that ignored its argument entirely would pass — which is
	// exactly what a mutation proved before this line was added.
	customer := newUser(t, c, "user_d_unknown", domain.RoleCustomer)
	newTicket(t, c, customer, "a real ticket")

	_, err := c.q.GetTicketByID(c.ctx, uuid.New())
	if err == nil {
		t.Fatal("GetTicketByID found a ticket for an id that names none")
	}
}

// The timeline an agent sees is the full one, including entries written by
// somebody else. It reuses the query sla.Reconstruct already reads, which has
// never carried a requester predicate.
func TestTheTimelineIsReadableWithoutBeingTheRequester(t *testing.T) {
	c := setup(t)

	customer := newUser(t, c, "user_d_timeline", domain.RoleCustomer)
	tk := newTicket(t, c, customer, "with a timeline")

	if _, err := c.q.InsertTicketStatusHistory(c.ctx, ticketdb.InsertTicketStatusHistoryParams{
		TicketID:  tk.ID,
		ToStatus:  domain.StatusOpen,
		ActorID:   customer,
		ActorRole: domain.RoleCustomer,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seeding a history row: %v", err)
	}

	got, err := c.q.ListTicketStatusHistory(c.ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListTicketStatusHistory: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the timeline is empty, so the handler would answer 404 for a real ticket")
	}
}

// The scoped read must still refuse. An unscoped query added beside it is only
// safe while the original keeps its predicate.
func TestTheScopedTicketReadStillRefusesAnotherCustomer(t *testing.T) {
	c := setup(t)

	alice := newUser(t, c, "user_d_alice", domain.RoleCustomer)
	bob := newUser(t, c, "user_d_bob", domain.RoleCustomer)
	tk := newTicket(t, c, alice, "alice's private ticket")

	_, err := c.q.GetTicketForRequester(c.ctx, ticketdb.GetTicketForRequesterParams{
		ID:          tk.ID,
		RequesterID: bob,
	})
	if err == nil {
		t.Fatal("the scoped query returned another customer's ticket")
	}
}
