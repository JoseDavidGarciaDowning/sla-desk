package api

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

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/config"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewRouter(testConfig(), Deps{})
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
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ClerkWebhookPath,
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
func TestTheTicketRoutesRequireASession(t *testing.T) {
	router := testRouter(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, TicketsPath},
		{http.MethodGet, TicketsPath},
		{http.MethodGet, TicketsPath + "/44444444-4444-4444-4444-444444444444"},
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

	if _, err := NewRouter(cfg, Deps{}); err == nil {
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

	caller := store.User{ClerkUserID: "user_router", Email: "router@example.test", Role: ticket.RoleCustomer}
	reader := routerFakeReader{}

	router, err := NewRouter(cfg, Deps{
		Users:  routerStubProvisioner{user: caller},
		Reader: reader,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, TicketsPath, nil)
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

type routerStubProvisioner struct{ user store.User }

func (s routerStubProvisioner) GetUserByClerkID(context.Context, string) (store.User, error) {
	return s.user, nil
}

func (s routerStubProvisioner) UpsertUserFromClerk(context.Context, store.UpsertUserFromClerkParams) (store.User, error) {
	return s.user, nil
}

type routerFakeReader struct{}

func (routerFakeReader) ListTicketsByRequester(context.Context, store.ListTicketsByRequesterParams) ([]store.Ticket, error) {
	return nil, nil
}

func (routerFakeReader) GetTicketForRequester(context.Context, store.GetTicketForRequesterParams) (store.Ticket, error) {
	return store.Ticket{}, nil
}
