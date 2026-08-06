package app

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity"
	identityapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/config"
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewRouter(testConfig(), Deps{Identity: testIdentity(t, testConfig(), routerStubUsers{}), Tickets: testTickets()})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return h
}

// testConfig is a config complete enough to build the router. The webhook
// secret has to be a real Svix one because NewRouter refuses an unusable one,
// which is the point of it returning an error.
func testConfig() config.Config {
	return config.Config{
		Port:               "8080",
		DatabaseURL:        "postgres://localhost/test",
		ClerkSecretKey:     "sk_test_not_a_real_key",
		ClerkWebhookSecret: "whsec_lzIrAAiI15CsoFs852lFmxfJ1xYXoQ5x",
	}
}

func TestHealth_RespondsAsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Errorf("body is not valid JSON: %s", rec.Body.String())
	}
}

func TestHealth_RejectsMethodsOtherThanGET(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			testRouter(t).ServeHTTP(rec, httptest.NewRequest(method, "/health", nil))

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// The two properties that make the wiring correct, and that nothing else
// checks. Both handlers are already tested in isolation; what can be wrong here
// is which middleware they sit behind.

// Clerk signs with Svix and sends no session JWT. Behind RequireAuth every
// delivery would be answered 401, Svix would retry each one until it gave up,
// and the primary provisioning path in docs/spec.md §4.5 would be silently
// dead. 400 proves the request reached the webhook handler and was rejected on
// its signature, which is the correct reason.
func TestTheClerkWebhookIsNotBehindRequireAuth(t *testing.T) {
	router := testRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, identityhttp.WebhookPath,
		strings.NewReader(`{"type":"user.created","data":{"id":"user_1"}}`)))

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("the webhook is mounted behind RequireAuth; Clerk would never get through")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 from the signature check", rec.Code)
	}
}

// The mirror image: the ticket routes must not be reachable without a session.
// clerkhttp.WithHeaderAuthorization on its own would let all of these through,
// so this is what proves RequireAuth is mounted behind it.
//
// It also proves each route is mounted at all, which is not obvious and is
// worth stating: a path inside this group that no handler is registered for
// answers 404, because chi routes before it runs the group's middleware. So a
// 401 here means the route exists *and* is protected, and a handler someone
// forgot to wire — which happened between T8 and T10 — shows up as a 404.
func TestTheTicketRoutesRequireASession(t *testing.T) {
	router := testRouter(t)

	const someTicket = "/44444444-4444-4444-4444-444444444444"

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, tickethttp.TicketsPath},
		{http.MethodGet, tickethttp.TicketsPath},
		{http.MethodGet, tickethttp.TicketsPath + someTicket},
		{http.MethodGet, tickethttp.TicketsPath + someTicket + tickethttp.TicketHistorySuffix},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// The health check stays open: Cloud Run and any uptime monitor call it without
// credentials.
func TestHealthNeedsNoSession(t *testing.T) {
	router := testRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, HealthPath, nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRouterRefusesAnUnusableWebhookSecret(t *testing.T) {
	cfg := testConfig()
	cfg.ClerkWebhookSecret = "not-a-svix-secret"

	// The identity module is supplied, and built from the same bad config, so
	// this fails for the reason under test. Passing Deps{} would now fail on
	// the missing module instead and the test would pass having proved nothing.
	_, err := NewRouter(cfg, Deps{Identity: testIdentity(t, cfg, routerStubUsers{}), Tickets: testTickets()})
	if err == nil {
		t.Fatal("expected an error — a bad secret must stop the process at startup")
	}
	// Both modules are supplied, and from the same bad config, so this fails
	// for the reason under test. Omitting either would fail on the missing
	// module instead and the test would pass having proved nothing.
	for _, wrong := range []string{"Deps.Identity", "Deps.Tickets"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("failed on a missing module, not on the secret: %v", err)
		}
	}
}

// ── The assembled router, end to end ────────────────────────────────────────

// Everything above proves a request without a session is refused. That is
// necessary and not sufficient: dropping RequireAuth from the router leaves
// those tests green, because each handler also checks the context and refuses
// on its own. The system fails closed, which is the right direction — but the
// wiring would be broken and nothing would say so.
//
// This is the test that notices. It signs a real token against a real JWKS and
// expects the request to succeed, so the whole chain has to be present and in
// order: verify the signature, attach the claims, resolve the caller, reach the
// handler.
func TestASignedRequestReachesTheHandlerThroughTheRouter(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	const kid = "ins_router_key"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"` + kid +
			`","n":"` + n + `","e":"` + e + `"}]}`))
	}))
	t.Cleanup(jwksServer.Close)

	cfg := testConfig()
	cfg.ClerkAPIURL = jwksServer.URL

	caller := identitydomain.User{ClerkUserID: "user_router", Email: "router@example.test", Role: identitydomain.RoleCustomer}

	router, err := NewRouter(cfg, Deps{
		Identity: testIdentity(t, cfg, routerStubUsers{user: caller}),
		Tickets:  ticket.NewWith(stubTicketRepo{}, stubSLA{}),
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, tickethttp.TicketsPath, nil)
	r.Header.Set("Authorization", "Bearer "+mintRouterToken(t, key, kid, "user_router"))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the middleware chain did not resolve a verified caller\nbody: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tickets"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func mintRouterToken(t *testing.T, key *rsa.PrivateKey, kid, subject string) string {
	t.Helper()

	segment := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}

	now := time.Now()
	signing := segment(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}) + "." +
		segment(map[string]any{
			"iss": "https://clerk.sla-desk-test.example.com",
			"sub": subject,
			"sid": "sess_" + subject,
			"iat": now.Add(-time.Minute).Unix(),
			"nbf": now.Add(-time.Minute).Unix(),
			"exp": now.Add(time.Hour).Unix(),
		})

	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// routerStubUsers stands in for the users table, so the router can be built
// with the real identity module — real middleware, real Clerk verification
// against the stub JWKS — and no database.
type routerStubUsers struct{ user identitydomain.User }

func (s routerStubUsers) ByClerkID(context.Context, string) (identitydomain.User, error) {
	return s.user, nil
}

func (s routerStubUsers) Upsert(context.Context, string, identitydomain.Identity, identitydomain.Role) (identitydomain.User, error) {
	return s.user, nil
}

func (s routerStubUsers) GrantRole(context.Context, string, identitydomain.Role) (identitydomain.User, error) {
	return s.user, nil
}

// testIdentity builds the identity module the way NewRouter's caller does, so a
// router test exercises the real middleware chain rather than a stand-in for
// it. Only the users table and the JWKS endpoint are replaced.
func testIdentity(tb testing.TB, cfg config.Config, users identityapp.UserRepository) *identity.Module {
	tb.Helper()
	clerkCfg := clerk.Config{
		SecretKey:       cfg.ClerkSecretKey,
		AuthorizedParty: cfg.ClerkAuthorizedParty,
		APIURL:          cfg.ClerkAPIURL,
	}
	m, err := identity.NewWith(users, clerk.NewIdentityProvider(clerkCfg), identity.Config{
		Clerk:         clerkCfg,
		WebhookSecret: cfg.ClerkWebhookSecret,
	})
	if err != nil {
		tb.Fatalf("identity.NewWith: %v", err)
	}
	return m
}

// stubTicketRepo and stubSLA let the router be built with the real ticket
// module — real handlers, real use cases — and no database.
//
// The router tests assert which middleware a route sits behind, so what the
// handlers return does not matter; that they are the real ones does.
// testTickets builds the real ticket module over stubs, so a router test
// mounts the real handlers behind the real middleware.
func testTickets() *ticket.Module {
	return ticket.NewWith(stubTicketRepo{}, stubSLA{})
}

type stubTicketRepo struct{}

func (stubTicketRepo) Create(context.Context, ticketapp.NewTicket, ticketapp.SLAClock) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{}, nil
}

func (stubTicketRepo) Transition(context.Context, ticketapp.StatusChange, ticketapp.SLAClock) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{}, nil
}

func (stubTicketRepo) PolicyIDOf(context.Context, uuid.UUID) (int64, error) { return 1, nil }

func (stubTicketRepo) ListForRequester(context.Context, ticketapp.ListFilter) ([]ticketdomain.Ticket, error) {
	return nil, nil
}

func (stubTicketRepo) ListForQueue(context.Context, ticketapp.QueueFilter) ([]ticketapp.QueueEntry, error) {
	return nil, nil
}

// The agent detail reads answer with something rather than with
// ErrTicketNotFound, and that matters to more than convenience: chi routes
// before it runs a group's middleware, so an unmounted path answers 404. A stub
// that returned "no such ticket" would make a missing route and a present one
// look identical, and TestEveryAgentPathAdmitsAnAgentAndAnAdmin would stop
// proving the route exists.
func (stubTicketRepo) OneByID(_ context.Context, id uuid.UUID) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{ID: id, Title: "a ticket"}, nil
}

func (stubTicketRepo) Timeline(context.Context, uuid.UUID) ([]ticketdomain.HistoryEntry, error) {
	return []ticketdomain.HistoryEntry{{ToStatus: ticketdomain.StatusOpen}}, nil
}

func (stubTicketRepo) OneForRequester(context.Context, uuid.UUID, uuid.UUID) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{}, nil
}

func (stubTicketRepo) HistoryForRequester(context.Context, uuid.UUID, uuid.UUID) ([]ticketdomain.HistoryEntry, error) {
	return nil, nil
}

type stubSLA struct{}

func (stubSLA) ForPriority(context.Context, ticketdomain.Priority) (ticketapp.SLAClock, error) {
	return nil, nil
}

func (stubSLA) ForPolicy(context.Context, int64) (ticketapp.SLAClock, error) { return nil, nil }

// --- The agent route group (slice 2, T18) ---------------------------------

// agentRouter builds the real router with a caller of the given role, and mints
// a token for them. Nothing is stubbed but the users table and the JWKS: the
// request goes through Clerk verification, RequireAuth and RequireRole in the
// order production mounts them.
func agentRouter(t *testing.T, role identitydomain.Role) (http.Handler, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	// A fresh key id per router, not a constant. Clerk's JWK cache is global to
	// the process and keyed by key id alone, with no scoping by instance or
	// issuer — recorded in T7 when it first made tests leak keys into each
	// other. Reusing one id here means the first key minted wins for the rest
	// of the run, and every later test gets a 401 that has nothing to do with
	// what it is testing.
	kid := "ins_agent_" + uuid.NewString()
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"` + kid +
			`","n":"` + n + `","e":"` + e + `"}]}`))
	}))
	t.Cleanup(jwksServer.Close)

	cfg := testConfig()
	cfg.ClerkAPIURL = jwksServer.URL

	caller := identitydomain.User{
		ID:          uuid.New(),
		ClerkUserID: "user_" + string(role),
		Email:       string(role) + "@example.test",
		Role:        role,
	}

	router, err := NewRouter(cfg, Deps{
		Identity: testIdentity(t, cfg, routerStubUsers{user: caller}),
		Tickets:  ticket.NewWith(stubTicketRepo{}, stubSLA{}),
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	return router, mintRouterToken(t, key, kid, caller.ClerkUserID)
}

// agentPaths is every route mounted under the agent prefix. New endpoints are
// added here, and the tests below then cover them without being edited — which
// is the property T14a's mutation testing said was missing when one scope test
// happened to exercise one filter and the query was open for the other.
// agentPathTicketID is any well-formed uuid. The stub answers for every id, so
// what these paths exercise is the guard and the wiring, not a lookup.
const agentPathTicketID = "11111111-2222-3333-4444-555555555555"

func agentPaths() []string {
	return []string{
		AgentPathPrefix + identityhttp.MePath,
		AgentPathPrefix + tickethttp.QueuePath,
		AgentPathPrefix + tickethttp.QueuePath + "/" + agentPathTicketID,
		AgentPathPrefix + tickethttp.QueuePath + "/" + agentPathTicketID + tickethttp.TicketHistorySuffix,
	}
}

// The whole point of the group. A customer is refused every path under it, one
// case each rather than one case for the surface.
func TestEveryAgentPathRefusesACustomer(t *testing.T) {
	router, token := agentRouter(t, identitydomain.RoleCustomer)

	for _, path := range agentPaths() {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)

		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s as a customer = %d, want 403\nbody: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// A route that is mounted answers 403 to a customer; one that is not answers
// 404, because chi routes before it runs the group's middleware. So this also
// proves every path in the list actually exists — the gap between T8 and T10,
// where handlers were written and never wired, would show up here.
func TestEveryAgentPathAdmitsAnAgentAndAnAdmin(t *testing.T) {
	for _, role := range []identitydomain.Role{identitydomain.RoleAgent, identitydomain.RoleAdmin} {
		router, token := agentRouter(t, role)

		for _, path := range agentPaths() {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("Authorization", "Bearer "+token)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Errorf("GET %s as %s = %d, want 200\nbody: %s", path, role, rec.Code, rec.Body.String())
			}
		}
	}
}

// No token at all must stop at authentication, before the role guard has an
// opinion. A 403 here would tell a signed-out caller they are signed in as the
// wrong person.
func TestTheAgentGroupAnswers401WithoutASession(t *testing.T) {
	router, _ := agentRouter(t, identitydomain.RoleAgent)

	for _, path := range agentPaths() {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with no token = %d, want 401", path, rec.Code)
		}
	}
}

// The customer surface must not have moved. An agent group that quietly closed
// the ticket endpoints would pass every test above.
func TestTheTicketEndpointsStillAdmitACustomer(t *testing.T) {
	router, token := agentRouter(t, identitydomain.RoleCustomer)

	r := httptest.NewRequest(http.MethodGet, tickethttp.TicketsPath, nil)
	r.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("GET %s as a customer = %d, want 200 — slice 2 must not close slice 1",
			tickethttp.TicketsPath, rec.Code)
	}
}

// The role in the response comes from our users row, which is what the frontend
// agent layout will decide on. The id is ours too: Clerk knows a subject and
// nothing about our users table, so "assign this to me" has no other source.
func TestMeReportsTheRoleAndIdFromOurDatabase(t *testing.T) {
	router, token := agentRouter(t, identitydomain.RoleAgent)

	r := httptest.NewRequest(http.MethodGet, AgentPathPrefix+identityhttp.MePath, nil)
	r.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	var got struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v\nbody: %s", err, rec.Body.String())
	}

	if got.Role != string(identitydomain.RoleAgent) {
		t.Errorf("role = %q, want agent", got.Role)
	}
	if _, err := uuid.Parse(got.ID); err != nil {
		t.Errorf("id = %q, want our own user uuid: %v", got.ID, err)
	}
}
