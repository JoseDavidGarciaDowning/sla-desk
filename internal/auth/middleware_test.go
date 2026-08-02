package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/auth"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// fakeStore stands in for *store.Queries. A fake rather than a mock: the tests
// assert on what the middleware produced, not on which methods it happened to
// call — except for the upsert, where "was it called at all" is the behaviour
// under test.
type fakeStore struct {
	user      store.User
	getErr    error
	upsertErr error

	upserts     int
	upsertParam store.UpsertUserFromClerkParams
}

func (f *fakeStore) GetUserByClerkID(_ context.Context, _ string) (store.User, error) {
	if f.getErr != nil {
		return store.User{}, f.getErr
	}
	return f.user, nil
}

func (f *fakeStore) UpsertUserFromClerk(_ context.Context, arg store.UpsertUserFromClerkParams) (store.User, error) {
	f.upserts++
	f.upsertParam = arg
	if f.upsertErr != nil {
		return store.User{}, f.upsertErr
	}
	return f.user, nil
}

func uuidOf(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("parsing uuid %q: %v", s, err)
	}
	return id
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

	handler := auth.RequireAuth(nil, nil)(protected.handler())

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
	want := store.User{
		ID:          uuidOf(t, "11111111-1111-1111-1111-111111111111"),
		ClerkUserID: "user_existing",
		Email:       "agent@example.test",
		Role:        ticket.RoleAgent,
	}
	users := &fakeStore{user: want}

	var got auth.User
	var ok bool
	handler := auth.RequireAuth(users, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = auth.UserFromContext(r.Context())
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
	if got.Role != ticket.RoleAgent {
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
	identity auth.Identity
	err      error
	calls    int
}

func (f *fakeFetcher) FetchIdentity(_ context.Context, _ string) (auth.Identity, error) {
	f.calls++
	return f.identity, f.err
}

// The signup race in docs/spec.md §4.5: the browser holds a valid token the
// instant signup completes, while Clerk's webhook is still in flight. Every
// first page load would fail without this path.
func TestRequireAuthProvisionsAUserItHasNeverSeen(t *testing.T) {
	provisioned := store.User{
		ID:          uuidOf(t, "22222222-2222-2222-2222-222222222222"),
		ClerkUserID: "user_brand_new",
		Email:       "new@example.test",
		Role:        ticket.RoleCustomer,
	}
	users := &fakeStore{getErr: pgx.ErrNoRows, user: provisioned}
	clerkAPI := &fakeFetcher{identity: auth.Identity{Email: "new@example.test", Name: "New Person"}}

	var got auth.User
	handler := auth.RequireAuth(users, clerkAPI)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.UserFromContext(r.Context())
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
	if users.upsertParam.ClerkUserID != "user_brand_new" {
		t.Errorf("upsert clerk id = %q, want user_brand_new", users.upsertParam.ClerkUserID)
	}
	if users.upsertParam.Email != "new@example.test" {
		t.Errorf("upsert email = %q, want the address fetched from Clerk", users.upsertParam.Email)
	}
	if users.upsertParam.Name == nil || *users.upsertParam.Name != "New Person" {
		t.Errorf("upsert name = %v, want New Person", users.upsertParam.Name)
	}
	if got.Role != ticket.RoleCustomer {
		t.Errorf("role = %q, want customer — new users are never seeded with any other role", got.Role)
	}
}

// An existing user must not cost a Clerk API round trip.
func TestRequireAuthDoesNotCallClerkForAKnownUser(t *testing.T) {
	users := &fakeStore{user: store.User{ClerkUserID: "user_known", Role: ticket.RoleCustomer}}
	clerkAPI := &fakeFetcher{}

	handler := auth.RequireAuth(users, clerkAPI)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	users := &fakeStore{user: store.User{
		ClerkUserID: "user_ambitious",
		Role:        ticket.RoleCustomer, // what our table says
	}}

	r := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	claims := &clerk.SessionClaims{
		RegisteredClaims: clerk.RegisteredClaims{Subject: "user_ambitious"},
	}
	// Whatever a client manages to put in the token, including a role.
	claims.Custom = map[string]string{"role": "admin"}
	r = r.WithContext(clerk.ContextWithSessionClaims(r.Context(), claims))

	var got auth.User
	handler := auth.RequireAuth(users, &fakeFetcher{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.UserFromContext(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if got.Role != ticket.RoleCustomer {
		t.Errorf("role = %q, want customer — a claim in the token became a role", got.Role)
	}
}

func TestRequireAuthFailsClosedWhenClerkIsUnreachable(t *testing.T) {
	users := &fakeStore{getErr: pgx.ErrNoRows}
	clerkAPI := &fakeFetcher{err: errors.New("clerk: connection refused")}

	var protected reached
	handler := auth.RequireAuth(users, clerkAPI)(protected.handler())

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
	handler := auth.RequireAuth(users, clerkAPI)(protected.handler())

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

// Clerk does not guarantee a name. It must arrive as NULL rather than as an
// empty string, so the column can distinguish "not provided" from "blank".
func TestProvisioningWithoutANameStoresNull(t *testing.T) {
	users := &fakeStore{getErr: pgx.ErrNoRows}
	clerkAPI := &fakeFetcher{identity: auth.Identity{Email: "noname@example.test"}}

	handler := auth.RequireAuth(users, clerkAPI)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), withClaims("user_no_name"))

	if users.upsertParam.Name != nil {
		t.Errorf("name = %q, want nil", *users.upsertParam.Name)
	}
}
