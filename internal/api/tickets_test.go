package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/api"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/auth"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

type fakeCreator struct {
	got store.NewTicket
	out store.Ticket
	err error

	calls int
}

func (f *fakeCreator) Create(_ context.Context, in store.NewTicket) (store.Ticket, error) {
	f.calls++
	f.got = in
	if f.err != nil {
		return store.Ticket{}, f.err
	}
	return f.out, nil
}

func uuid(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("parsing uuid: %v", err)
	}
	return id
}

// stubProvisioner satisfies auth.Provisioner so the request can go through the
// real RequireAuth rather than a test-only way of putting a user in the
// context. That matters: it proves the handler reads the caller from where the
// middleware actually puts it, and that there is no back door to forge one.
type stubProvisioner struct{ user store.User }

func (s stubProvisioner) GetUserByClerkID(context.Context, string) (store.User, error) {
	return s.user, nil
}

func (s stubProvisioner) UpsertUserFromClerk(context.Context, store.UpsertUserFromClerkParams) (store.User, error) {
	return s.user, nil
}

// authenticated wraps a handler in RequireAuth and returns a request already
// carrying verified Clerk claims, as clerkhttp.WithHeaderAuthorization would
// have left it.
func authenticated(t *testing.T, caller store.User, h http.Handler, body string) (http.Handler, *http.Request) {
	t.Helper()

	wrapped := auth.RequireAuth(stubProvisioner{user: caller}, nil)(h)

	r := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	claims := &clerk.SessionClaims{
		RegisteredClaims: clerk.RegisteredClaims{Subject: caller.ClerkUserID},
	}
	return wrapped, r.WithContext(clerk.ContextWithSessionClaims(r.Context(), claims))
}

const validTicketBody = `{
	"title": "Cannot download my invoice",
	"description": "The download button returns a 500.",
	"category": "billing",
	"priority": "normal"
}`

func customer(t *testing.T) store.User {
	t.Helper()
	return store.User{
		ID:          uuid(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		ClerkUserID: "user_customer",
		Email:       "customer@example.test",
		Role:        ticket.RoleCustomer,
	}
}

func TestCreateTicketReturns201WithTheTicket(t *testing.T) {
	caller := customer(t)
	due := time.Now().Add(24 * time.Hour).UTC()
	creator := &fakeCreator{out: store.Ticket{
		ID:          uuid(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    ticket.CategoryBilling,
		Priority:    ticket.PriorityNormal,
		Status:      ticket.StatusOpen,
		SlaDueAt:    &due,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}}

	handler, r := authenticated(t, caller, api.CreateTicketHandler(creator), validTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/tickets/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" {
		t.Errorf("Location = %q", got)
	}

	var body api.TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Status != ticket.StatusOpen {
		t.Errorf("status = %q, want open", body.Status)
	}
	if body.SLADueAt == nil {
		t.Error("sla_due_at is null on a ticket whose clock is running")
	}
	if body.SLABreached {
		t.Error("a brand new ticket is reported as breached")
	}
}

// docs/spec.md §4.3: never trust a user id from the client. The requester is
// the authenticated caller, whatever the body says.
func TestRequesterComesFromTheSessionNotTheBody(t *testing.T) {
	caller := customer(t)
	creator := &fakeCreator{}

	body := `{
		"title": "Hello",
		"description": "World",
		"category": "billing",
		"priority": "normal",
		"requester_id": "cccccccc-cccc-cccc-cccc-cccccccccccc"
	}`

	handler, r := authenticated(t, caller, api.CreateTicketHandler(creator), body)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if creator.calls != 1 {
		t.Fatalf("Create calls = %d, want 1", creator.calls)
	}
	if creator.got.RequesterID != caller.ID {
		t.Errorf("requester = %v, want the authenticated caller %v", creator.got.RequesterID, caller.ID)
	}
	if creator.got.ActorRole != ticket.RoleCustomer {
		t.Errorf("actor role = %q, want the caller's role from our table", creator.got.ActorRole)
	}
}

func TestCreateTicketRejectsAnUnauthenticatedRequest(t *testing.T) {
	creator := &fakeCreator{}

	handler := auth.RequireAuth(stubProvisioner{}, nil)(api.CreateTicketHandler(creator))
	r := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(validTicketBody))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if creator.calls != 0 {
		t.Error("an unauthenticated request reached the store")
	}
}

func TestCreateTicketReportsEveryInvalidField(t *testing.T) {
	creator := &fakeCreator{}
	body := `{"title":"","description":"","category":"sales","priority":"critical"}`

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if creator.calls != 0 {
		t.Error("an invalid request reached the store")
	}

	body_ := decodeProblem(t, rec)
	errs, ok := body_["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors = %v, want an object", body_["errors"])
	}
	for _, field := range []string{"title", "description", "category", "priority"} {
		if errs[field] == nil {
			t.Errorf("no error for %q", field)
		}
	}
}

func TestCreateTicketRejectsAMalformedBody(t *testing.T) {
	creator := &fakeCreator{}

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), `{"title":`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if creator.calls != 0 {
		t.Error("a malformed body reached the store")
	}
}

// A priority with no policy is our seed being wrong, not the caller's request
// being wrong — validation has already established the priority is one of the
// four. It must not be reported as a client error.
func TestMissingPolicyIsReportedAsAServerFault(t *testing.T) {
	creator := &fakeCreator{err: store.ErrNoPolicyForPriority}

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), validTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestStoreFailuresDoNotLeakTheirCause(t *testing.T) {
	creator := &fakeCreator{err: errors.New(`pq: relation "tickets" does not exist on host db.internal`)}

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), validTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	for _, leak := range []string{"relation", "db.internal", "tickets"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the response echoed %q: %s", leak, rec.Body.String())
		}
	}
}

// Whitespace is trimmed once, before the write, so the stored text and the
// validated text are the same.
func TestCreateTicketTrimsTheText(t *testing.T) {
	creator := &fakeCreator{}
	body := `{"title":"  Padded  ","description":"\n  Body  \n","category":"other","priority":"low"}`

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), body)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if creator.got.Title != "Padded" {
		t.Errorf("title = %q, want Padded", creator.got.Title)
	}
	if creator.got.Description != "Body" {
		t.Errorf("description = %q, want Body", creator.got.Description)
	}
}

// ── Reading tickets ─────────────────────────────────────────────────────────

type fakeReader struct {
	history       []store.TicketStatusHistory
	historyParams store.ListTicketStatusHistoryForRequesterParams
	historyCalls  int

	page []store.Ticket
	one  store.Ticket
	err  error

	listParams store.ListTicketsByRequesterParams
	getParams  store.GetTicketForRequesterParams
	listCalls  int
	getCalls   int
}

func (f *fakeReader) ListTicketsByRequester(_ context.Context, arg store.ListTicketsByRequesterParams) ([]store.Ticket, error) {
	f.listCalls++
	f.listParams = arg
	return f.page, f.err
}

func (f *fakeReader) GetTicketForRequester(_ context.Context, arg store.GetTicketForRequesterParams) (store.Ticket, error) {
	f.getCalls++
	f.getParams = arg
	return f.one, f.err
}

func (f *fakeReader) ListTicketStatusHistoryForRequester(
	_ context.Context, arg store.ListTicketStatusHistoryForRequesterParams,
) ([]store.TicketStatusHistory, error) {
	f.historyCalls++
	f.historyParams = arg
	return f.history, f.err
}

func sampleTicket(t *testing.T, id string, created time.Time) store.Ticket {
	t.Helper()
	due := created.Add(24 * time.Hour)
	return store.Ticket{
		ID:        uuid(t, id),
		Title:     "Ticket " + id[:8],
		Category:  ticket.CategoryOther,
		Priority:  ticket.PriorityNormal,
		Status:    ticket.StatusOpen,
		SlaDueAt:  &due,
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// getRequest mounts the handler on a chi router at the pattern it will really
// be served from, because GetTicketHandler reads {id} out of chi's route
// context. Calling the handler directly leaves that context empty and every id
// looks malformed — which is how the first version of these tests failed.
func getRequest(t *testing.T, caller store.User, h http.Handler, pattern, target string) (http.Handler, *http.Request) {
	t.Helper()

	router := chi.NewRouter()
	router.With(auth.RequireAuth(stubProvisioner{user: caller}, nil)).Method(http.MethodGet, pattern, h)

	r := httptest.NewRequest(http.MethodGet, target, nil)
	claims := &clerk.SessionClaims{RegisteredClaims: clerk.RegisteredClaims{Subject: caller.ClerkUserID}}
	return router, r.WithContext(clerk.ContextWithSessionClaims(r.Context(), claims))
}

// docs/spec.md §4.3: the scope is in the SQL. The handler never sees another
// customer's row to filter out, so there is no filter to forget.
func TestListScopesToTheCallerInTheQuery(t *testing.T) {
	caller := customer(t)
	reader := &fakeReader{}

	handler, r := getRequest(t, caller, api.ListTicketsHandler(reader), "/api/tickets", "/api/tickets")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if reader.listParams.RequesterID != caller.ID {
		t.Errorf("requester = %v, want the authenticated caller", reader.listParams.RequesterID)
	}
}

func TestListReturnsAnEmptyArrayNotNull(t *testing.T) {
	handler, r := getRequest(t, customer(t), api.ListTicketsHandler(&fakeReader{}), "/api/tickets", "/api/tickets")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	// A JSON null would make every client write a nil check that an empty array
	// makes unnecessary.
	if !strings.Contains(rec.Body.String(), `"tickets":[]`) {
		t.Errorf("body = %s, want an empty array", rec.Body.String())
	}
}

func TestListEmitsACursorOnlyWhenThereIsAnotherPage(t *testing.T) {
	caller := customer(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("a full page plus one more row", func(t *testing.T) {
		// The handler asks for one row beyond the page so it can tell whether
		// another page exists without a second count query.
		rows := make([]store.Ticket, 3)
		for i := range rows {
			rows[i] = sampleTicket(t, "1111111a-1111-1111-1111-11111111111"+string(rune('0'+i)),
				now.Add(-time.Duration(i)*time.Minute))
		}
		reader := &fakeReader{page: rows}

		handler, r := getRequest(t, caller, api.ListTicketsHandler(reader), "/api/tickets", "/api/tickets?limit=2")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		if reader.listParams.PageSize != 3 {
			t.Errorf("page size asked of the store = %d, want limit+1", reader.listParams.PageSize)
		}

		var body api.TicketListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(body.Tickets) != 2 {
			t.Errorf("returned %d tickets, want the requested 2", len(body.Tickets))
		}
		if body.NextCursor == nil {
			t.Fatal("next_cursor is null although a further page exists")
		}
	})

	t.Run("a short page", func(t *testing.T) {
		reader := &fakeReader{page: []store.Ticket{
			sampleTicket(t, "2222222a-2222-2222-2222-222222222222", now),
		}}

		handler, r := getRequest(t, caller, api.ListTicketsHandler(reader), "/api/tickets", "/api/tickets?limit=10")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		var body api.TicketListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if body.NextCursor != nil {
			t.Errorf("next_cursor = %q on the last page", *body.NextCursor)
		}
	})
}

// The cursor carries the sort key of the last row returned, so the next page
// starts exactly where this one stopped, regardless of what was inserted in
// between.
func TestCursorResumesWhereThePageStopped(t *testing.T) {
	caller := customer(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	first := &fakeReader{page: []store.Ticket{
		sampleTicket(t, "3333333a-3333-3333-3333-333333333331", now),
		sampleTicket(t, "3333333a-3333-3333-3333-333333333332", now.Add(-time.Minute)),
	}}

	handler, r := getRequest(t, caller, api.ListTicketsHandler(first), "/api/tickets", "/api/tickets?limit=1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	var page api.TicketListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if page.NextCursor == nil {
		t.Fatal("no cursor was issued")
	}

	second := &fakeReader{}
	handler, r = getRequest(t, caller, api.ListTicketsHandler(second),
		"/api/tickets", "/api/tickets?limit=1&cursor="+url.QueryEscape(*page.NextCursor))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if second.listParams.AfterCreatedAt == nil {
		t.Fatal("the cursor was not decoded into the query")
	}
	if !second.listParams.AfterCreatedAt.Equal(now) {
		t.Errorf("after = %v, want the first page's last row %v", second.listParams.AfterCreatedAt, now)
	}
	if second.listParams.AfterID != uuid(t, page.Tickets[0].ID) {
		t.Errorf("after id = %v", second.listParams.AfterID)
	}
}

func TestListRejectsAnUnreadableCursor(t *testing.T) {
	reader := &fakeReader{}
	handler, r := getRequest(t, customer(t), api.ListTicketsHandler(reader), "/api/tickets", "/api/tickets?cursor=not-a-cursor")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if reader.listCalls != 0 {
		t.Error("a bad cursor reached the store")
	}
}

func TestListClampsThePageSize(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int32
	}{
		{"", api.DefaultPageSize + 1},
		{"?limit=0", api.DefaultPageSize + 1},
		{"?limit=-5", api.DefaultPageSize + 1},
		{"?limit=nonsense", api.DefaultPageSize + 1},
		{"?limit=1000", api.MaxPageSize + 1},
	} {
		t.Run("limit"+tc.query, func(t *testing.T) {
			reader := &fakeReader{}
			handler, r := getRequest(t, customer(t), api.ListTicketsHandler(reader), "/api/tickets", "/api/tickets"+tc.query)
			handler.ServeHTTP(httptest.NewRecorder(), r)

			if reader.listParams.PageSize != tc.want {
				t.Errorf("page size = %d, want %d", reader.listParams.PageSize, tc.want)
			}
		})
	}
}

// docs/spec.md §11: another customer's ticket is 404, never 403. A 403 confirms
// the ticket exists, which is exactly what the caller must not learn.
func TestGetAnswers404ForATicketThatIsNotYours(t *testing.T) {
	reader := &fakeReader{err: pgx.ErrNoRows}

	handler, r := getRequest(t, customer(t), api.GetTicketHandler(reader),
		"/api/tickets/{id}", "/api/tickets/44444444-4444-4444-4444-444444444444")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — 403 would confirm the ticket exists", rec.Code)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "forbidden") {
		t.Errorf("the body hints at existence: %s", rec.Body.String())
	}
}

func TestGetScopesToTheCallerInTheQuery(t *testing.T) {
	caller := customer(t)
	created := time.Now().UTC()
	reader := &fakeReader{one: sampleTicket(t, "55555555-5555-5555-5555-555555555555", created)}

	handler, r := getRequest(t, caller, api.GetTicketHandler(reader),
		"/api/tickets/{id}", "/api/tickets/55555555-5555-5555-5555-555555555555")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if reader.getParams.RequesterID != caller.ID {
		t.Errorf("requester = %v, want the authenticated caller", reader.getParams.RequesterID)
	}
}

func TestGetRejectsAnIDThatIsNotAUUID(t *testing.T) {
	reader := &fakeReader{}
	handler, r := getRequest(t, customer(t), api.GetTicketHandler(reader), "/api/tickets/{id}", "/api/tickets/not-a-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if reader.getCalls != 0 {
		t.Error("a malformed id reached the store")
	}
}

// ── Filters (T14) ────────────────────────────────────────────────────────────

func TestListPassesTheFiltersToTheQuery(t *testing.T) {
	for _, tc := range []struct {
		query        string
		wantStatus   *string
		wantPriority *string
	}{
		{query: ""},
		{query: "?status=open", wantStatus: strPtr("open")},
		{query: "?priority=urgent", wantPriority: strPtr("urgent")},
		{
			query:        "?status=pending&priority=low",
			wantStatus:   strPtr("pending"),
			wantPriority: strPtr("low"),
		},
		// An empty parameter is how a form submits "no filter chosen". It has
		// to mean the same as omitting it, or clearing a filter in the UI would
		// ask for tickets whose status is the empty string and return none.
		{query: "?status=&priority="},
	} {
		t.Run("filters"+tc.query, func(t *testing.T) {
			reader := &fakeReader{}
			handler, r := getRequest(t, customer(t), api.ListTicketsHandler(reader),
				"/api/tickets", "/api/tickets"+tc.query)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
			}
			if !samePtr(reader.listParams.Status, tc.wantStatus) {
				t.Errorf("status filter = %v, want %v",
					deref(reader.listParams.Status), deref(tc.wantStatus))
			}
			if !samePtr(reader.listParams.Priority, tc.wantPriority) {
				t.Errorf("priority filter = %v, want %v",
					deref(reader.listParams.Priority), deref(tc.wantPriority))
			}
		})
	}
}

// A filter the API cannot honour is refused rather than ignored.
//
// Ignoring it is the tempting choice — it always returns something — but it
// returns the wrong thing silently: a bookmark reading ?status=opne shows every
// ticket while the UI says the list is filtered to open ones. A 400 naming the
// field is what the form already knows how to display, since it is the same
// document shape as a rejected create.
func TestListRejectsAFilterThatIsNotInTheVocabulary(t *testing.T) {
	for _, tc := range []struct {
		query string
		field string
	}{
		{"?status=opne", "status"},
		{"?status=archived", "status"},
		{"?priority=critical", "priority"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			reader := &fakeReader{}
			handler, r := getRequest(t, customer(t), api.ListTicketsHandler(reader),
				"/api/tickets", "/api/tickets"+tc.query)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if reader.listCalls != 0 {
				t.Error("the query ran anyway")
			}

			errs, ok := decodeProblem(t, rec)["errors"].(map[string]any)
			if !ok {
				t.Fatalf("no errors member: %s", rec.Body.String())
			}
			if errs[tc.field] == nil {
				t.Errorf("no message for %q: %v", tc.field, errs)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func samePtr(got, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func deref(p *string) string {
	if p == nil {
		return "<none>"
	}
	return *p
}

// ── The status history (T14b) ────────────────────────────────────────────────

func historyRow(from *ticket.Status, to ticket.Status, role ticket.Role, at time.Time) store.TicketStatusHistory {
	var actor pgtype.UUID
	_ = actor.Scan("11111111-1111-1111-1111-111111111111")

	return store.TicketStatusHistory{
		ID:         1,
		FromStatus: from,
		ToStatus:   to,
		ActorID:    actor,
		ActorRole:  role,
		CreatedAt:  at,
	}
}

// An empty history is a 404, not an empty timeline.
//
// Every ticket has at least the row recording its creation, so nothing is
// returned only when the ticket does not exist or is not the caller's — and the
// query cannot tell those apart, which is what stops this handler from
// confirming that an id names a real ticket (docs/spec.md §11).
func TestHistoryAnswers404WhenThereIsNone(t *testing.T) {
	reader := &fakeReader{}
	handler, r := getRequest(t, customer(t), api.GetTicketHistoryHandler(reader),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\nbody: %s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusForbidden {
		t.Error("403 confirms the ticket exists")
	}
}

func TestHistoryReturnsTheEntriesInOrder(t *testing.T) {
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	open := ticket.StatusOpen

	reader := &fakeReader{history: []store.TicketStatusHistory{
		historyRow(nil, ticket.StatusOpen, ticket.RoleCustomer, base),
		historyRow(&open, ticket.StatusPending, ticket.RoleAgent, base.Add(time.Hour)),
	}}

	handler, r := getRequest(t, customer(t), api.GetTicketHistoryHandler(reader),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	var body api.TicketHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v\nbody: %s", err, rec.Body.String())
	}

	if len(body.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(body.Entries))
	}
	if body.Entries[0].FromStatus != nil {
		t.Errorf("the creation entry has from_status %v, want null", *body.Entries[0].FromStatus)
	}
	if body.Entries[1].ToStatus != ticket.StatusPending {
		t.Errorf("entry 1 to_status = %q", body.Entries[1].ToStatus)
	}
	if body.Entries[1].ActorRole != ticket.RoleAgent {
		t.Errorf("entry 1 actor_role = %q — who moved it is the point of a timeline", body.Entries[1].ActorRole)
	}
}

// The timeline says which kind of person moved the ticket, never which one.
// actor_id is another user's primary key, and a customer has no use for it —
// putting it on the wire hands out an identifier for enumeration and links a
// customer's view to the agent roster.
func TestHistoryNeverExposesTheActorsIdentity(t *testing.T) {
	reader := &fakeReader{history: []store.TicketStatusHistory{
		historyRow(nil, ticket.StatusOpen, ticket.RoleAgent, time.Now()),
	}}

	handler, r := getRequest(t, customer(t), api.GetTicketHistoryHandler(reader),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	for _, leak := range []string{"actor_id", "11111111", "ActorID"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the response mentions %q: %s", leak, rec.Body.String())
		}
	}
}

func TestHistoryScopesToTheCallerInTheQuery(t *testing.T) {
	caller := customer(t)
	reader := &fakeReader{history: []store.TicketStatusHistory{
		historyRow(nil, ticket.StatusOpen, ticket.RoleCustomer, time.Now()),
	}}

	handler, r := getRequest(t, caller, api.GetTicketHistoryHandler(reader),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if reader.historyParams.RequesterID != caller.ID {
		t.Errorf("requester = %v, want the authenticated caller", reader.historyParams.RequesterID)
	}
}

func TestHistoryRejectsAnIDThatIsNotAUUID(t *testing.T) {
	reader := &fakeReader{}
	handler, r := getRequest(t, customer(t), api.GetTicketHistoryHandler(reader),
		"/api/tickets/{id}/history", "/api/tickets/not-a-uuid/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if reader.historyCalls != 0 {
		t.Error("the query ran with an unparsed id")
	}
}
