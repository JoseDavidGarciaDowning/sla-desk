package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
)

// fakeStore stands in for the users table. A fake rather than a mock: the tests
// assert on what the middleware produced, not on which methods it happened to
// call — except for the upsert, where "was it called at all" is the behaviour
// under test.
type fakeStore struct {
	user      domain.User
	getErr    error
	upsertErr error

	upserts        int
	upsertClerkID  string
	upsertIdentity domain.Identity
}

var _ application.UserRepository = (*fakeStore)(nil)

func (f *fakeStore) ByClerkID(_ context.Context, _ string) (domain.User, error) {
	if f.getErr != nil {
		return domain.User{}, f.getErr
	}
	return f.user, nil
}

func (f *fakeStore) Upsert(_ context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	f.upserts++
	f.upsertClerkID = clerkUserID
	f.upsertIdentity = id
	if f.upsertErr != nil {
		return domain.User{}, f.upsertErr
	}
	return f.user, nil
}

func uuidOf(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parsing uuid %q: %v", s, err)
	}
	return id
}

// requireAuth wires the real service behind the real middleware, so these tests
// still exercise the resolve-or-provision path rather than a stub standing in
// for it. Only the collaborators at the very edge are fake.
func requireAuth(users application.UserRepository, ids application.IdentityProvider) func(http.Handler) http.Handler {
	if users == nil && ids == nil {
		return identityhttp.RequireAuth(nil)
	}
	return identityhttp.RequireAuth(application.NewService(users, ids))
}

// withClaims builds the request clerkhttp.WithHeaderAuthorization would have
// produced for a token that verified.
func withClaims(subject string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	claims := &clerk.SessionClaims{
		RegisteredClaims: clerk.RegisteredClaims{Subject: subject},
	}
	return r.WithContext(clerk.ContextWithSessionClaims(r.Context(), claims))
}

// reached records whether the protected handler ran. Every rejection test
// asserts on it: a middleware that returns 401 but still calls the handler has
// not protected anything.
type reached struct{ called bool }

func (r *reached) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.called = true
		w.WriteHeader(http.StatusOK)
	})
}

// The trap documented in docs/spec.md §4.3: clerkhttp.WithHeaderAuthorization
// puts claims in the context when a token verifies and otherwise lets the
// request through untouched. Without this middleware behind it, an endpoint
// that looks protected is open.
func TestRequireAuthRejectsARequestWithNoSessionClaims(t *testing.T) {
	var protected reached

	handler := requireAuth(nil, nil)(protected.handler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tickets", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if protected.called {
		t.Error("the protected handler ran on an unauthenticated request")
	}
}

// The role is read from our users table, never from the token. A client that
// forges a role claim gets whatever our own row says.
func TestRequireAuthPutsOurUserInTheContext(t *testing.T) {
	want := domain.User{
		ID:          uuidOf(t, "11111111-1111-1111-1111-111111111111"),
		ClerkUserID: "user_existing",
		Email:       "agent@example.test",
		Role:        domain.RoleAgent,
	}
	users := &fakeStore{user: want}

	var got domain.User
	var ok bool
	handler := requireAuth(users, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = identityhttp.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims("user_existing"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !ok {
		t.Fatal("no user in the request context")
	}
	if got.ClerkUserID != want.ClerkUserID {
		t.Errorf("clerk id = %q, want %q", got.ClerkUserID, want.ClerkUserID)
	}
	if got.ID != want.ID {
		t.Errorf("id = %v, want %v", got.ID, want.ID)
	}
	if got.Role != domain.RoleAgent {
		t.Errorf("role = %q, want agent — the role must come from our table", got.Role)
	}
	if users.upserts != 0 {
		t.Errorf("upserts = %d, want 0 — the user already existed", users.upserts)
	}
}

// fakeFetcher stands in for the Clerk Backend API. The session token carries
// only the subject, so email and name have to be fetched the first time we see
// a user.
type fakeFetcher struct {
	identity domain.Identity
	err      error
	calls    int
}

var _ application.IdentityProvider = (*fakeFetcher)(nil)

func (f *fakeFetcher) FetchIdentity(_ context.Context, _ string) (domain.Identity, error) {
	f.calls++
	return f.identity, f.err
}

// The signup race in docs/spec.md §4.5: the browser holds a valid token the
// instant signup completes, while Clerk's webhook is still in flight. Every
// first page load would fail without this path.
func TestRequireAuthProvisionsAUserItHasNeverSeen(t *testing.T) {
	provisioned := domain.User{
		ID:          uuidOf(t, "22222222-2222-2222-2222-222222222222"),
		ClerkUserID: "user_brand_new",
		Email:       "new@example.test",
		Role:        domain.RoleCustomer,
	}
	users := &fakeStore{getErr: application.ErrNoSuchUser, user: provisioned}
	clerkAPI := &fakeFetcher{identity: domain.Identity{Email: "new@example.test", Name: "New Person"}}

	var got domain.User
	handler := requireAuth(users, clerkAPI)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = identityhttp.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims("user_brand_new"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a brand new user's first request must succeed", rec.Code)
	}
	if users.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", users.upserts)
	}
	if clerkAPI.calls != 1 {
		t.Errorf("Clerk API calls = %d, want 1", clerkAPI.calls)
	}
	if users.upsertClerkID != "user_brand_new" {
		t.Errorf("upsert clerk id = %q, want user_brand_new", users.upsertClerkID)
	}
	if users.upsertIdentity.Email != "new@example.test" {
		t.Errorf("upsert email = %q, want the address fetched from Clerk", users.upsertIdentity.Email)
	}
	if users.upsertIdentity.Name != "New Person" {
		t.Errorf("upsert name = %q, want New Person", users.upsertIdentity.Name)
	}
	if got.Role != domain.RoleCustomer {
		t.Errorf("role = %q, want customer — new users are never seeded with any other role", got.Role)
	}
}

// An existing user must not cost a Clerk API round trip.
func TestRequireAuthDoesNotCallClerkForAKnownUser(t *testing.T) {
	users := &fakeStore{user: domain.User{ClerkUserID: "user_known", Role: domain.RoleCustomer}}
	clerkAPI := &fakeFetcher{}

	handler := requireAuth(users, clerkAPI)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), withClaims("user_known"))

	if clerkAPI.calls != 0 {
		t.Errorf("Clerk API calls = %d, want 0", clerkAPI.calls)
	}
}

// A forged claim is not a role. docs/spec.md §4.3: the role lives in our table
// and a client-supplied token can never assert one.
func TestRoleComesFromOurTableNotFromTheToken(t *testing.T) {
	users := &fakeStore{user: domain.User{
		ClerkUserID: "user_ambitious",
		Role:        domain.RoleCustomer, // what our table says
	}}

	r := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	claims := &clerk.SessionClaims{
		RegisteredClaims: clerk.RegisteredClaims{Subject: "user_ambitious"},
	}
	// Whatever a client manages to put in the token, including a role.
	claims.Custom = map[string]string{"role": "admin"}
	r = r.WithContext(clerk.ContextWithSessionClaims(r.Context(), claims))

	var got domain.User
	handler := requireAuth(users, &fakeFetcher{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = identityhttp.UserFromContext(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if got.Role != domain.RoleCustomer {
		t.Errorf("role = %q, want customer — a claim in the token became a role", got.Role)
	}
}

func TestRequireAuthFailsClosedWhenClerkIsUnreachable(t *testing.T) {
	users := &fakeStore{getErr: application.ErrNoSuchUser}
	clerkAPI := &fakeFetcher{err: errors.New("clerk: connection refused")}

	var protected reached
	handler := requireAuth(users, clerkAPI)(protected.handler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims("user_unreachable"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if protected.called {
		t.Error("the handler ran without a resolved user")
	}
	if users.upserts != 0 {
		t.Error("a row was written from an identity we never managed to fetch")
	}
}

func TestRequireAuthFailsClosedWhenTheDatabaseErrors(t *testing.T) {
	users := &fakeStore{getErr: errors.New("connection reset by peer")}
	clerkAPI := &fakeFetcher{}

	var protected reached
	handler := requireAuth(users, clerkAPI)(protected.handler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims("user_db_down"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if protected.called {
		t.Error("the handler ran without a resolved user")
	}
	if clerkAPI.calls != 0 {
		t.Error("a database error was mistaken for a missing user and triggered provisioning")
	}
}

// Clerk does not guarantee a name, and the absence has to survive the whole
// path. That it lands as NULL rather than as a blank string is the repository's
// decision and is asserted against a real database by
// TestUpsertAcceptsAMissingName; what is checked here is that nothing between
// Clerk and the repository invents one.
func TestProvisioningWithoutANameCarriesNoName(t *testing.T) {
	users := &fakeStore{getErr: application.ErrNoSuchUser}
	clerkAPI := &fakeFetcher{identity: domain.Identity{Email: "noname@example.test"}}

	handler := requireAuth(users, clerkAPI)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), withClaims("user_no_name"))

	if users.upsertIdentity.Name != "" {
		t.Errorf("name = %q, want empty — nothing may invent one", users.upsertIdentity.Name)
	}
}

// Every other failure this API reports is an RFC 9457 problem document. These
// two were bare status lines with no body at all — not a different wording, an
// empty response — because internal/auth could not reach internal/api's writer
// without an import cycle. internal/httperr exists to end that.
//
// A client cannot tell a 401 from this middleware apart from a 401 from a proxy
// in front of it when neither says anything, and the frontend's error handling
// reads the document to decide what to show.
func TestRequireAuthAnswersWithAProblemDocument(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.Handler
		request *http.Request
		want    int
	}{
		{
			name:    "no session claims",
			handler: requireAuth(nil, nil)(nil),
			request: httptest.NewRequest(http.MethodGet, "/api/tickets", nil),
			want:    http.StatusUnauthorized,
		},
		{
			name: "the database is unreachable",
			handler: requireAuth(
				&fakeStore{getErr: errors.New("connection refused")},
				&fakeFetcher{},
			)(nil),
			request: withClaims("user_whatever"),
			want:    http.StatusInternalServerError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, tc.request)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding the body: %v\nbody: %q", err, rec.Body.String())
			}
			if body["status"] != float64(tc.want) {
				t.Errorf("status in body = %v, want %d", body["status"], tc.want)
			}
		})
	}
}

// The 500 above must not carry the cause. "connection refused" is the mildest
// thing a pgx error can say; they also carry host names, ports and table names.
func TestRequireAuthDoesNotEchoTheDatabaseError(t *testing.T) {
	handler := requireAuth(
		&fakeStore{getErr: errors.New("dial tcp 10.1.2.3:5432: connection refused")},
		&fakeFetcher{},
	)(nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims("user_whatever"))

	for _, leak := range []string{"dial", "10.1.2.3", "5432", "refused"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the response mentions %q: %s", leak, rec.Body.String())
		}
	}
}
