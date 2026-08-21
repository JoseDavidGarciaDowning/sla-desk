package assignable

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
)

// UseCase is what the adapter needs: the one call that reads the roster.
type UseCase interface {
	List(ctx context.Context) ([]domain.User, error)
}

// assignableUser is one person a ticket may be handed to.
//
// **This is the first response in the project that carries another user's id**,
// and the departure is worth stating rather than slipping in. T14b dropped
// actor_id from the history DTO and T19 sends requester_name instead of
// requester_id, both so that reading a ticket does not hand out identifiers to
// enumerate.
//
// It is unavoidable here: PATCH .../assignee takes an id, so a control that
// lets one agent hand a ticket to another has to know it. What bounds the
// exposure is the shape of the answer rather than good intentions — the query
// returns agents and admins only, by predicate, and the route sits inside the
// group that already refuses everyone else. The people who can read this list
// are the people who are already on it.
//
// The Clerk subject is not here and must not be added. It identifies a person
// to a third party we do not control, and nothing in this app needs it on the
// wire.
//
// The role travels because an agent and an admin are different colleagues to
// hand a ticket to, and a roster that will not say which is which makes the
// reader guess.
type user struct {
	ID   uuid.UUID   `json:"id"`
	Name string      `json:"name"`
	Role domain.Role `json:"role"`
}

// HTTP adapts GET /api/agent/assignable.
//
// Unpaginated, on purpose. This is a staff roster rather than a data set: it is
// the people who work here, it is read to fill one select, and a desk with
// enough agents to need a page break has other problems first. If that ever
// stops being true the fix is a search parameter, not an offset — an agent
// looking for a colleague types a name, they do not page through everyone.
func HTTP(h UseCase) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identityhttp.UserFromContext(r.Context()); !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		found, err := h.List(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "listing the assignable users failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		// Built with make so an empty roster encodes as [] rather than null. A
		// deploy with no agents configured is the default state (T17), so this
		// is the ordinary answer rather than an edge case.
		out := make([]user, 0, len(found))
		for _, u := range found {
			// Clerk holds no name for someone who signed up with an email and a
			// password. Falling back to the address here rather than in the
			// query keeps it a display decision, the same way the agent queue
			// does it.
			name := u.Name
			if name == "" {
				name = u.Email
			}

			out = append(out, user{ID: u.ID, Name: name, Role: u.Role})
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"users": out}); err != nil {
			// The status is written by now, so there is nothing left to tell
			// the client.
			return
		}
	})
}
