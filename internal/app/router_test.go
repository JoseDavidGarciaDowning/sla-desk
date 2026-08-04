// These tests are about the *assembly*: which routes exist, which sit behind
// authentication, and that a real signed request reaches a handler through the
// whole chain. None of it is a property any single module can assert about
// itself, which is why it lives with the wiring.
package app_test

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

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/app"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity"
	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/config"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/health"
	"github.com/google/uuid"
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()

	h, err := testApp(t, testConfig(), identitydomain.User{}).Router()
	if err != nil {
		t.Fatalf("Router: %v", err)
	}
	return h
}

// testApp assembles the real application without a database.
//
// Only the two outermost ports are stood in for — the users table and Clerk's
// Backend API. Everything between them is production code: the real Clerk JWT
// middleware, the real RequireAuth, the real adapter that turns an identity
// user into a ticket caller, and the real handlers. Swapping any of those out
// would leave these tests unable to notice a route that is not protected.
func testApp(t *testing.T, cfg config.Config, caller identitydomain.User) *app.App {
	t.Helper()

	identityModule := identity.NewWith(
		routerStubUsers{user: caller},
		routerStubIdentities{},
		identity.Config{
			Clerk: clerk.Config{
				SecretKey:       cfg.ClerkSecretKey,
				AuthorizedParty: cfg.ClerkAuthorizedParty,
				APIURL:          cfg.ClerkAPIURL,
			},
			WebhookSecret: cfg.ClerkWebhookSecret,
		})

	return app.NewWith(cfg, identityModule, routerFakeRepo{}, routerStubSLA{},
		map[string]health.Probe{"database": func(context.Context) error { return nil }})
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
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, health.Path, nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRouterRefusesAnUnusableWebhookSecret(t *testing.T) {
	cfg := testConfig()
	cfg.ClerkWebhookSecret = "not-a-svix-secret"

	if _, err := testApp(t, cfg, identitydomain.User{}).Router(); err == nil {
		t.Error("expected an error — a bad secret must stop the process at startup")
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

	caller := identitydomain.User{
		ClerkUserID: "user_router",
		Email:       "router@example.test",
		Role:        identitydomain.RoleCustomer,
	}

	router, err := testApp(t, cfg, caller).Router()
	if err != nil {
		t.Fatalf("Router: %v", err)
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

type routerStubUsers struct{ user identitydomain.User }

func (s routerStubUsers) ByClerkID(context.Context, string) (identitydomain.User, error) {
	return s.user, nil
}

func (s routerStubUsers) Upsert(context.Context, string, identitydomain.Identity) (identitydomain.User, error) {
	return s.user, nil
}

type routerStubIdentities struct{}

func (routerStubIdentities) Fetch(context.Context, string) (identitydomain.Identity, error) {
	panic("a known user must not cost a Clerk round trip")
}

type routerFakeRepo struct{}

func (routerFakeRepo) Create(context.Context, ticketapp.NewTicket, ticketapp.SLAClock) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{}, nil
}

func (routerFakeRepo) Transition(context.Context, ticketapp.StatusChange, ticketapp.SLAClock) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{}, nil
}

func (routerFakeRepo) PolicyIDOf(context.Context, uuid.UUID) (int64, error) { return 1, nil }

func (routerFakeRepo) ListByRequester(context.Context, ticketapp.ListFilter) ([]ticketdomain.Ticket, error) {
	return nil, nil
}

func (routerFakeRepo) GetForRequester(context.Context, uuid.UUID, uuid.UUID) (ticketdomain.Ticket, error) {
	return ticketdomain.Ticket{}, nil
}

func (routerFakeRepo) HistoryForRequester(context.Context, uuid.UUID, uuid.UUID) ([]ticketdomain.HistoryEntry, error) {
	return nil, nil
}

type routerStubSLA struct{}

func (routerStubSLA) ForPriority(context.Context, ticketdomain.Priority) (ticketapp.SLAClock, error) {
	return routerStubClock{}, nil
}

func (routerStubSLA) ForPolicy(context.Context, int64) (ticketapp.SLAClock, error) {
	return routerStubClock{}, nil
}

type routerStubClock struct{}

func (routerStubClock) PolicyID() int64 { return 1 }

func (routerStubClock) Compute([]ticketdomain.Phase) (ticketapp.ClockState, error) {
	return ticketapp.ClockState{}, nil
}
