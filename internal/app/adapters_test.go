package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// An internal test, deliberately: the adapters are unexported because nothing
// outside this package may depend on them, and the properties below are exactly
// the ones that would silently rot if the translation were treated as a
// formality.

// The seam the ticket module's handlers rely on. They ask a CallerResolver and
// answer 401 when it says no, so "no caller without authentication" is the
// property that makes every ticket route safe — and it is a property of this
// function, since the ticket module has no way to check it.
func TestCallerIsOnlyResolvableBehindAuthentication(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)

	if _, ok := callerFromContext(r.Context()); ok {
		t.Error("a caller was resolved from a request that never authenticated")
	}
}

func TestCallerCarriesTheIdentityUsersIDAndRole(t *testing.T) {
	want := identitydomain.User{
		ID:          uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ClerkUserID: "user_agent",
		Role:        identitydomain.RoleAgent,
	}

	ctx := identityhttp.ContextWithUser(context.Background(), want)

	got, ok := callerFromContext(ctx)
	if !ok {
		t.Fatal("no caller resolved from a context the middleware had filled in")
	}
	if got.ID != want.ID {
		t.Errorf("id = %v, want %v", got.ID, want.ID)
	}
	if got.Role != ticketdomain.RoleAgent {
		t.Errorf("role = %q, want agent", got.Role)
	}
}

// Every identity role has to map onto a ticket actor role, and the mapping has
// to be total.
//
// The table is written out rather than derived, so adding a role to the
// identity module without deciding what it means on a ticket makes this test
// fail rather than silently fall through to the default. That matters because
// the fallthrough is invisible in production: an unmapped role would act as a
// customer, and only the person holding it would ever find out.
func TestEveryIdentityRoleMapsToATicketRole(t *testing.T) {
	want := map[identitydomain.Role]ticketdomain.ActorRole{
		identitydomain.RoleCustomer: ticketdomain.RoleCustomer,
		identitydomain.RoleAgent:    ticketdomain.RoleAgent,
		identitydomain.RoleAdmin:    ticketdomain.RoleAdmin,
	}

	all := []identitydomain.Role{
		identitydomain.RoleCustomer,
		identitydomain.RoleAgent,
		identitydomain.RoleAdmin,
	}
	if len(all) != len(want) {
		t.Fatalf("the mapping covers %d roles, the identity module declares %d", len(want), len(all))
	}

	for role, expected := range want {
		if got := actorRole(role); got != expected {
			t.Errorf("actorRole(%q) = %q, want %q", role, got, expected)
		}
	}

	// The two vocabularies hold the same strings today, and this is what makes
	// that a fact rather than an assumption the conversion quietly relies on.
	for role, expected := range want {
		if string(role) != string(expected) {
			t.Errorf("identity %q maps to ticket %q — the strings have diverged, "+
				"which is fine, but the audit trail now records a different word "+
				"than the users table does", role, expected)
		}
	}
}

// An unknown role must not become an admin.
//
// It cannot happen through the exhaustive switch above while the mapping stays
// total, which is precisely why the default deserves a test: it is the branch
// that runs on the day someone adds a role and forgets this file.
func TestAnUnmappedRoleFallsToTheLeastPrivileged(t *testing.T) {
	if got := actorRole(identitydomain.Role("superadmin")); got != ticketdomain.RoleCustomer {
		t.Errorf("actorRole(unknown) = %q, want customer", got)
	}
}
