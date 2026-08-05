package http_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"

	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// generatedContractPath is where cmd/gencontract writes, relative to this
// package.
// generatedContractPath is resolved from the module root rather than written
// as a relative path, because a relative one silently breaks when the package
// moves — which is exactly what happened when this package moved from
// internal/api into the ticket module, and the failure read like a missing
// file rather than like a moved test.
func generatedContractPath(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "web", "lib", "contract.ts")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// The bounds the contract publishes must be the ones Validate actually
// enforces, and this test establishes that by measurement rather than by
// reading the same constant twice.
//
// A test that compared tickethttp.TicketContract().MaxTitleLength against
// api.maxTitleLength would pass no matter what either was, because both would
// move together. This one calls Validate at the boundary: a title of exactly
// MaxTitleLength has to be accepted and one character more has to be refused.
// A contract that published 100 while the server enforced 200 would fail here,
// and that is the whole failure mode worth guarding — a form that refuses text
// the API would have taken, or accepts text it will reject.
func TestContractBoundsAreTheOnesValidationEnforces(t *testing.T) {
	c := tickethttp.TicketContract()

	for _, tc := range []struct {
		field string
		limit int
		set   func(*tickethttp.CreateTicketRequest, string)
	}{
		{"title", c.MaxTitleLength, func(r *tickethttp.CreateTicketRequest, s string) { r.Title = s }},
		{"description", c.MaxDescriptionLength, func(r *tickethttp.CreateTicketRequest, s string) { r.Description = s }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			if tc.limit <= 0 {
				t.Fatalf("the contract publishes a limit of %d", tc.limit)
			}

			atLimit := validCreateTicket()
			tc.set(&atLimit, strings.Repeat("a", tc.limit))
			if err, ok := atLimit.Validate()[tc.field]; ok {
				t.Errorf("%d characters was refused (%q); the contract tells clients it is allowed",
					tc.limit, err)
			}

			overLimit := validCreateTicket()
			tc.set(&overLimit, strings.Repeat("a", tc.limit+1))
			if _, ok := overLimit.Validate()[tc.field]; !ok {
				t.Errorf("%d characters was accepted; the contract tells clients the limit is %d",
					tc.limit+1, tc.limit)
			}
		})
	}
}

// Every value the contract offers has to be one the API takes. A select whose
// options produce a 400 is worse than no select at all: the user has no way to
// find the answer the form itself refuses to accept.
func TestContractVocabulariesAreAccepted(t *testing.T) {
	c := tickethttp.TicketContract()

	if len(c.Categories) == 0 || len(c.Priorities) == 0 {
		t.Fatalf("categories = %v, priorities = %v; a form cannot render an empty select",
			c.Categories, c.Priorities)
	}

	for _, category := range c.Categories {
		req := validCreateTicket()
		req.Category = category
		if err, ok := req.Validate()["category"]; ok {
			t.Errorf("category %q is offered but refused: %s", category, err)
		}
	}

	for _, priority := range c.Priorities {
		req := validCreateTicket()
		req.Priority = priority
		if err, ok := req.Validate()["priority"]; ok {
			t.Errorf("priority %q is offered but refused: %s", priority, err)
		}
	}
}

// The reverse direction: a vocabulary the API accepts but the contract omits
// would leave an option out of the form with nothing to notice it. Comparing
// against the domain package rather than against api's own slices is what makes
// this more than a tautology — ticket is where the vocabulary is defined.
func TestContractOffersEveryDomainValue(t *testing.T) {
	c := tickethttp.TicketContract()

	for _, category := range []domain.Category{
		domain.CategoryBilling, domain.CategoryTechnical, domain.CategoryAccount, domain.CategoryOther,
	} {
		if !containsValue(c.Categories, category) {
			t.Errorf("category %q exists in the domain but is missing from the contract", category)
		}
	}

	for _, priority := range []domain.Priority{
		domain.PriorityUrgent, domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow,
	} {
		if !containsValue(c.Priorities, priority) {
			t.Errorf("priority %q exists in the domain but is missing from the contract", priority)
		}
	}

	// Statuses are not accepted in a create body — a client does not choose the
	// state a ticket is in — but they are a list filter and a thing the UI
	// labels, so they travel with the rest.
	for _, status := range []domain.Status{
		domain.StatusOpen, domain.StatusPending, domain.StatusResolved, domain.StatusClosed,
	} {
		if !containsValue(c.Statuses, status) {
			t.Errorf("status %q exists in the domain but is missing from the contract", status)
		}
	}
}

// A status the contract offers has to be one GET /api/tickets?status= accepts,
// or the filter control is built from options that produce a 400.
func TestContractStatusesAreAcceptedAsFilters(t *testing.T) {
	c := tickethttp.TicketContract()

	if len(c.Statuses) == 0 {
		t.Fatal("no statuses in the contract; a filter control cannot be rendered")
	}

	for _, status := range c.Statuses {
		reader := &fakeReader{}
		handler, r := getRequest(t, tickethttp.ListTicketsHandler(reader, resolverFor(customer(t))),
			"/api/tickets", "/api/tickets?status="+string(status))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Errorf("status %q is offered but refused with %d: %s",
				status, rec.Code, rec.Body.String())
		}
	}
}

func containsValue[T ~string](values []T, want T) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// Rendering has to carry every value across. A template that dropped the
// priorities would still produce a file that compiles, and the form would fail
// at runtime with an empty select.
func TestTypeScriptCarriesEveryContractValue(t *testing.T) {
	c := tickethttp.TicketContract()
	out := c.TypeScript()

	for _, want := range []string{
		"MAX_TITLE_LENGTH = 200",
		"MAX_DESCRIPTION_LENGTH = 10000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered contract does not contain %q", want)
		}
	}

	for _, category := range c.Categories {
		if !strings.Contains(out, `"`+string(category)+`"`) {
			t.Errorf("the rendered contract does not contain category %q", category)
		}
	}
	for _, priority := range c.Priorities {
		if !strings.Contains(out, `"`+string(priority)+`"`) {
			t.Errorf("the rendered contract does not contain priority %q", priority)
		}
	}
	for _, status := range c.Statuses {
		if !strings.Contains(out, `"`+string(status)+`"`) {
			t.Errorf("the rendered contract does not contain status %q", status)
		}
	}

	// The header is what stops someone editing the file by hand and losing the
	// change on the next generation.
	if !strings.Contains(out, "DO NOT EDIT") {
		t.Error("the rendered contract has no generated-file marker")
	}
}

// The guard that makes any of this worth building.
//
// Without it, generation is a step somebody has to remember, and a forgotten
// one is invisible: the frontend keeps compiling against last month's contract
// and the drift only surfaces as a 400 in front of a user. With it, adding a
// category or moving a bound turns `make check` red until the file is
// regenerated.
func TestGeneratedContractIsUpToDate(t *testing.T) {
	want := tickethttp.TicketContract().TypeScript()

	path := generatedContractPath(t)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v — run `make contract`", path, err)
	}

	if string(got) != want {
		t.Errorf("%s is stale. Run `make contract`.", path)
	}
}
