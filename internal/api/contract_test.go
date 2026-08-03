package api_test

import (
	"os"
	"strings"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/api"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// generatedContractPath is where cmd/gencontract writes, relative to this
// package.
const generatedContractPath = "../../web/lib/contract.ts"

// The bounds the contract publishes must be the ones Validate actually
// enforces, and this test establishes that by measurement rather than by
// reading the same constant twice.
//
// A test that compared api.TicketContract().MaxTitleLength against
// api.maxTitleLength would pass no matter what either was, because both would
// move together. This one calls Validate at the boundary: a title of exactly
// MaxTitleLength has to be accepted and one character more has to be refused.
// A contract that published 100 while the server enforced 200 would fail here,
// and that is the whole failure mode worth guarding — a form that refuses text
// the API would have taken, or accepts text it will reject.
func TestContractBoundsAreTheOnesValidationEnforces(t *testing.T) {
	c := api.TicketContract()

	for _, tc := range []struct {
		field string
		limit int
		set   func(*api.CreateTicketRequest, string)
	}{
		{"title", c.MaxTitleLength, func(r *api.CreateTicketRequest, s string) { r.Title = s }},
		{"description", c.MaxDescriptionLength, func(r *api.CreateTicketRequest, s string) { r.Description = s }},
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
	c := api.TicketContract()

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
	c := api.TicketContract()

	for _, category := range []ticket.Category{
		ticket.CategoryBilling, ticket.CategoryTechnical, ticket.CategoryAccount, ticket.CategoryOther,
	} {
		if !containsValue(c.Categories, category) {
			t.Errorf("category %q exists in the domain but is missing from the contract", category)
		}
	}

	for _, priority := range []ticket.Priority{
		ticket.PriorityUrgent, ticket.PriorityHigh, ticket.PriorityNormal, ticket.PriorityLow,
	} {
		if !containsValue(c.Priorities, priority) {
			t.Errorf("priority %q exists in the domain but is missing from the contract", priority)
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
	c := api.TicketContract()
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
	want := api.TicketContract().TypeScript()

	got, err := os.ReadFile(generatedContractPath)
	if err != nil {
		t.Fatalf("reading %s: %v — run `make contract`", generatedContractPath, err)
	}

	if string(got) != want {
		t.Errorf("%s is stale. Run `make contract`.", generatedContractPath)
	}
}
