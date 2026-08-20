package http

import (
	"strings"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Bounds mirror the CHECK constraints in migration 003. Validating against them
// as well is not duplication for its own sake: without it an oversized title
// comes back as a constraint violation, which is a 500 that names nothing. With
// it the client gets a 400 that names the field.
//
// Exported because two features validate against them and the contract
// publishes them, and shared here rather than in one of those because none of
// them owns the answer.
const (
	MaxTitleLength       = 200
	MaxDescriptionLength = 10000
)

// The vocabularies the API accepts, and the reason they are module-wide rather
// than feature-local: create validates a body against them, list and queue
// validate a filter against them, transition validates a target against them,
// and the contract publishes all three to the frontend. A copy per feature
// would be four lists that can disagree about what a category is.
var (
	ValidCategories = []domain.Category{
		domain.CategoryBilling, domain.CategoryTechnical, domain.CategoryAccount, domain.CategoryOther,
	}
	ValidPriorities = []domain.Priority{
		domain.PriorityUrgent, domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow,
	}

	// Statuses are never accepted in a request body — a client does not choose
	// what state a ticket is in — but they are accepted as a list filter, and
	// the frontend needs them to render both the filter control and a status
	// label. In the contract for that reason, and validated for the same one.
	ValidStatuses = []domain.Status{
		domain.StatusOpen, domain.StatusPending, domain.StatusResolved, domain.StatusClosed,
	}
)

// Join renders a vocabulary for a "must be one of ..." message. Unlike a
// membership test there is no version of this in the standard library —
// strings.Join takes []string, and these are named types over string.
func Join[T ~string](values []T) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return strings.Join(out, ", ")
}

// MaxBodyBytes caps a request body. The description is bounded at 10000
// characters, so anything approaching this is not a ticket.
//
// Shared rather than per-feature because three endpoints take a body and a cap
// that differs between them is a cap somebody has to look up before answering
// "how large may a request be".
const MaxBodyBytes = 64 << 10 // 64 KiB
