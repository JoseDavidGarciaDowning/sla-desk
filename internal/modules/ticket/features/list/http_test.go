package list_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/list"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/tickettest"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// fakeLister records the filter the handler built and answers with rows.
//
// One method, because list.Tickets has one. The reader this replaced answered
// three endpoints at once, which meant a test about paging carried stubs for
// reading a ticket and reading a timeline.
type fakeLister struct {
	page []domain.Ticket
	err  error

	listParams list.Filter
	listCalls  int
}

func (f *fakeLister) Handle(_ context.Context, arg list.Filter) ([]domain.Ticket, error) {
	f.listCalls++
	f.listParams = arg
	return f.page, f.err
}

// docs/spec.md §4.3: the scope is in the SQL. The handler never sees another
// customer's row to filter out, so there is no filter to forget.
func TestListScopesToTheCallerInTheQuery(t *testing.T) {
	caller := tickettest.Customer(t)
	reader := &fakeLister{}

	handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(caller)), "/api/tickets", "/api/tickets")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if reader.listParams.RequesterID != tickettest.UUIDOf(t, caller.ID.String()) {
		t.Errorf("requester = %v, want the authenticated caller", reader.listParams.RequesterID)
	}
}

func TestListReturnsAnEmptyArrayNotNull(t *testing.T) {
	handler, r := tickettest.GetRequest(t, list.HTTP(&fakeLister{}, tickettest.ResolverFor(tickettest.Customer(t))), "/api/tickets", "/api/tickets")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	// A JSON null would make every client write a nil check that an empty array
	// makes unnecessary.
	if !strings.Contains(rec.Body.String(), `"tickets":[]`) {
		t.Errorf("body = %s, want an empty array", rec.Body.String())
	}
}

func TestListEmitsACursorOnlyWhenThereIsAnotherPage(t *testing.T) {
	caller := tickettest.Customer(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("a full page plus one more row", func(t *testing.T) {
		// The handler asks for one row beyond the page so it can tell whether
		// another page exists without a second count query.
		rows := make([]domain.Ticket, 3)
		for i := range rows {
			rows[i] = tickettest.SampleTicket(t, "1111111a-1111-1111-1111-11111111111"+string(rune('0'+i)),
				now.Add(-time.Duration(i)*time.Minute))
		}
		reader := &fakeLister{page: rows}

		handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(caller)), "/api/tickets", "/api/tickets?limit=2")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		if reader.listParams.PageSize != 3 {
			t.Errorf("page size asked of the store = %d, want limit+1", reader.listParams.PageSize)
		}

		var body list.Response
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
		reader := &fakeLister{page: []domain.Ticket{
			tickettest.SampleTicket(t, "2222222a-2222-2222-2222-222222222222", now),
		}}

		handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(caller)), "/api/tickets", "/api/tickets?limit=10")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		var body list.Response
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
	caller := tickettest.Customer(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	first := &fakeLister{page: []domain.Ticket{
		tickettest.SampleTicket(t, "3333333a-3333-3333-3333-333333333331", now),
		tickettest.SampleTicket(t, "3333333a-3333-3333-3333-333333333332", now.Add(-time.Minute)),
	}}

	handler, r := tickettest.GetRequest(t, list.HTTP(first, tickettest.ResolverFor(caller)), "/api/tickets", "/api/tickets?limit=1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	var page list.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if page.NextCursor == nil {
		t.Fatal("no cursor was issued")
	}

	second := &fakeLister{}
	handler, r = tickettest.GetRequest(t, list.HTTP(second, tickettest.ResolverFor(caller)),
		"/api/tickets", "/api/tickets?limit=1&cursor="+url.QueryEscape(*page.NextCursor))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if second.listParams.AfterCreatedAt == nil {
		t.Fatal("the cursor was not decoded into the query")
	}
	if !second.listParams.AfterCreatedAt.Equal(now) {
		t.Errorf("after = %v, want the first page's last row %v", second.listParams.AfterCreatedAt, now)
	}
	if second.listParams.AfterID == nil || *second.listParams.AfterID != tickettest.UUIDOf(t, page.Tickets[0].ID) {
		t.Errorf("after id = %v", second.listParams.AfterID)
	}
}

func TestListRejectsAnUnreadableCursor(t *testing.T) {
	reader := &fakeLister{}
	handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))), "/api/tickets", "/api/tickets?cursor=not-a-cursor")
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
		{"", tickethttp.DefaultPageSize + 1},
		{"?limit=0", tickethttp.DefaultPageSize + 1},
		{"?limit=-5", tickethttp.DefaultPageSize + 1},
		{"?limit=nonsense", tickethttp.DefaultPageSize + 1},
		{"?limit=1000", tickethttp.MaxPageSize + 1},
	} {
		t.Run("limit"+tc.query, func(t *testing.T) {
			reader := &fakeLister{}
			handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))), "/api/tickets", "/api/tickets"+tc.query)
			handler.ServeHTTP(httptest.NewRecorder(), r)

			if reader.listParams.PageSize != tc.want {
				t.Errorf("page size = %d, want %d", reader.listParams.PageSize, tc.want)
			}
		})
	}
}

func TestListPassesTheFiltersToTheQuery(t *testing.T) {
	for _, tc := range []struct {
		query        string
		wantStatus   *string
		wantPriority *string
	}{
		{query: ""},
		{query: "?status=open", wantStatus: tickettest.StrPtr("open")},
		{query: "?priority=urgent", wantPriority: tickettest.StrPtr("urgent")},
		{
			query:        "?status=pending&priority=low",
			wantStatus:   tickettest.StrPtr("pending"),
			wantPriority: tickettest.StrPtr("low"),
		},
		// An empty parameter is how a form submits "no filter chosen". It has
		// to mean the same as omitting it, or clearing a filter in the UI would
		// ask for tickets whose status is the empty string and return none.
		{query: "?status=&priority="},
	} {
		t.Run("filters"+tc.query, func(t *testing.T) {
			reader := &fakeLister{}
			handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
				"/api/tickets", "/api/tickets"+tc.query)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
			}
			if !tickettest.SamePtr((*string)(reader.listParams.Status), tc.wantStatus) {
				t.Errorf("status filter = %v, want %v",
					tickettest.Deref((*string)(reader.listParams.Status)), tickettest.Deref(tc.wantStatus))
			}
			if !tickettest.SamePtr((*string)(reader.listParams.Priority), tc.wantPriority) {
				t.Errorf("priority filter = %v, want %v",
					tickettest.Deref((*string)(reader.listParams.Priority)), tickettest.Deref(tc.wantPriority))
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
			reader := &fakeLister{}
			handler, r := tickettest.GetRequest(t, list.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
				"/api/tickets", "/api/tickets"+tc.query)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if reader.listCalls != 0 {
				t.Error("the query ran anyway")
			}

			errs, ok := tickettest.DecodeProblem(t, rec)["errors"].(map[string]any)
			if !ok {
				t.Fatalf("no errors member: %s", rec.Body.String())
			}
			if errs[tc.field] == nil {
				t.Errorf("no message for %q: %v", tc.field, errs)
			}
		})
	}
}
