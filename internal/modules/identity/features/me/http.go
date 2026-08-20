// Package me answers with the authenticated caller's own id and role.
//
// One file, and no handler: it calls no use case. What it returns is already in
// the request context, put there by the middleware that authenticated it, so
// there is nothing between the adapter and the answer.
//
// It is a feature rather than a stray handler in transport because "who am I"
// is something the system does, and because "where is GET /me" should have the
// same shape of answer as every other endpoint. The progressive complexity rule
// is what keeps it to one file rather than growing it a handler.go it would
// have nothing to put in.
package me

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
)

// meResponse is what the caller is told about themselves.
//
// Two fields, and both earn their place. The role is what the frontend's agent
// layout decides on: Clerk holds no role, so asking the browser's session is
// not an option (docs/spec.md §4.3).
//
// The id is ours, not Clerk's. It is the value that goes in assignee_id, and
// the frontend cannot derive it — Clerk knows a subject and nothing about our
// users table — so "assign this to me" would otherwise need a second lookup.
//
// Deliberately not the email or the name: the browser already has both from
// Clerk, and re-serving them would create a second copy to drift.
type response struct {
	ID   uuid.UUID   `json:"id"`
	Role domain.Role `json:"role"`
}

// HTTP answers with the authenticated caller's own id and role.
//
// It is the smallest endpoint that proves the whole chain: reaching it at all
// means the token verified, our users row was resolved, and the role guard let
// the request through. A test that requires it to *succeed* therefore fails if
// any link is missing or out of order — which is what T10 learned when
// deleting RequireAuth from the router left every test green.
func HTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		user, ok := identityhttp.UserFromContext(req.Context())
		if !ok {
			// Unreachable behind RequireAuth, and answered anyway. Every
			// handler in this codebase fails closed on a missing caller so
			// that mounting one outside the authenticated group is a 401
			// rather than a nil dereference.
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response{ID: user.ID, Role: user.Role}); err != nil {
			// The status is already written by then, so there is nothing to
			// tell the client. It is still worth not swallowing silently.
			return
		}
	})
}
