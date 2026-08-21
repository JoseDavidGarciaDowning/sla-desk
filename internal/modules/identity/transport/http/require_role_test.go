package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
)

// reached records whether the guarded handler ran. A middleware that refuses a
// request and then calls the next one anyway would still produce the right
// status code, so the status alone is not evidence.
func guarded(reached *bool, roles ...domain.Role) http.Handler {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
	return identityhttp.RequireRole(roles...)(next)
}

func asUser(role domain.Role) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/agent/tickets", nil)
	user := domain.User{ID: uuid.New(), ClerkUserID: "user_test", Role: role}
	return r.WithContext(identityhttp.ContextWithUser(r.Context(), user))
}

func TestAGrantedRoleReachesTheHandler(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleAgent, domain.RoleAdmin} {
		reached := false
		rec := httptest.NewRecorder()

		guarded(&reached, domain.RoleAgent, domain.RoleAdmin).ServeHTTP(rec, asUser(role))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", role, rec.Code)
		}
		if !reached {
			t.Errorf("%s: the handler did not run", role)
		}
	}
}

// 403 and not 404. The 404-rather-than-403 rule in docs/spec.md §11 protects
// the existence of a specific ticket, and there is no id in this path to
// confirm — a customer learns nothing from being told the agent surface exists.
func TestACustomerIsRefusedWithForbidden(t *testing.T) {
	reached := false
	rec := httptest.NewRecorder()

	guarded(&reached, domain.RoleAgent, domain.RoleAdmin).ServeHTTP(rec, asUser(domain.RoleCustomer))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if reached {
		t.Error("the handler ran for a customer")
	}
}

// Every other error in this API is an RFC 9457 problem document. A bare status
// with no body is what internal/auth used to write, and T13 found it: the
// frontend renders the API's own sentence, and there was none to render.
func TestTheRefusalIsAProblemDocument(t *testing.T) {
	reached := false
	rec := httptest.NewRecorder()

	guarded(&reached, domain.RoleAgent).ServeHTTP(rec, asUser(domain.RoleCustomer))

	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("the response has no body — there is nothing for a client to render")
	}
}

// The refusal must not name the roles that would have worked. It tells a
// caller how to describe an account worth attacking, and the answer is the same
// whatever we say: they still cannot pass.
func TestTheRefusalDoesNotNameTheRequiredRoles(t *testing.T) {
	reached := false
	rec := httptest.NewRecorder()

	guarded(&reached, domain.RoleAgent, domain.RoleAdmin).ServeHTTP(rec, asUser(domain.RoleCustomer))

	for _, leaked := range []string{"agent", "admin"} {
		if body := rec.Body.String(); strings.Contains(body, leaked) {
			t.Errorf("the refusal names %q: %s", leaked, body)
		}
	}
}

// 401, not 403. No user in the context means the request never passed through
// RequireAuth, which is a wiring mistake rather than a permission problem —
// the same distinction ticketports.CallerResolver already makes. Answering 403
// would tell an unauthenticated caller they are logged in as the wrong person.
func TestARequestThatSkippedAuthenticationIsUnauthorized(t *testing.T) {
	reached := false
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/agent/tickets", nil)

	guarded(&reached, domain.RoleAgent).ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if reached {
		t.Error("the handler ran for a request with no authenticated user")
	}
}

// An empty role list is what a caller writes by mistake — RequireRole() with
// the arguments forgotten. It must refuse everyone rather than admit everyone:
// a guard that lets every request through while looking mounted is the exact
// failure mode of clerkhttp.WithHeaderAuthorization (docs/spec.md §4.3).
func TestAnEmptyRoleListAdmitsNobody(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleCustomer, domain.RoleAgent, domain.RoleAdmin} {
		reached := false
		rec := httptest.NewRecorder()

		guarded(&reached).ServeHTTP(rec, asUser(role))

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 — an empty list must fail closed", role, rec.Code)
		}
		if reached {
			t.Errorf("%s: the handler ran behind a guard requiring no role", role)
		}
	}
}

// A role our own database somehow holds that is not in the list is refused like
// any other. The CHECK constraint makes this unreachable today; the guard must
// not depend on that staying true.
func TestAnUnknownRoleIsRefused(t *testing.T) {
	reached := false
	rec := httptest.NewRecorder()

	guarded(&reached, domain.RoleAgent).ServeHTTP(rec, asUser(domain.Role("superadmin")))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if reached {
		t.Error("the handler ran for a role nobody granted")
	}
}
