package create

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// Request is the body of POST /api/tickets.
//
// There is deliberately no requester_id, status or sla_* field. docs/spec.md
// §4.3 forbids trusting a user id from a client, and a struct with nowhere to
// put one cannot be talked into it — the value is dropped when the body is
// decoded, before any code has a chance to read it.
type Request struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Category    domain.Category `json:"category"`
	Priority    domain.Priority `json:"priority"`
}

// Normalised returns the request with surrounding whitespace removed, so the
// handler and the database see the same text and no caller has to remember to
// trim.
func (r Request) Normalised() Request {
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)
	return r
}

// Validate returns one message per rejected field, keyed by its JSON name.
//
// Every field is checked, not just the first to fail: a client fixing one
// problem per round trip is a worse experience than a form that shows all of
// them at once.
func (r Request) Validate() map[string]string {
	r = r.Normalised()
	errs := make(map[string]string)

	// utf8.RuneCountInString, not len: the database counts characters, and a
	// title of 200 accented ones is 200 characters and rather more bytes.
	switch n := utf8.RuneCountInString(r.Title); {
	case n == 0:
		errs["title"] = "must not be empty"
	case n > tickethttp.MaxTitleLength:
		errs["title"] = fmt.Sprintf("must be at most %d characters", tickethttp.MaxTitleLength)
	}

	switch n := utf8.RuneCountInString(r.Description); {
	case n == 0:
		errs["description"] = "must not be empty"
	case n > tickethttp.MaxDescriptionLength:
		errs["description"] = fmt.Sprintf("must be at most %d characters", tickethttp.MaxDescriptionLength)
	}

	if !slices.Contains(tickethttp.ValidCategories, r.Category) {
		errs["category"] = "must be one of " + tickethttp.Join(tickethttp.ValidCategories)
	}
	if !slices.Contains(tickethttp.ValidPriorities, r.Priority) {
		errs["priority"] = "must be one of " + tickethttp.Join(tickethttp.ValidPriorities)
	}

	return errs
}
