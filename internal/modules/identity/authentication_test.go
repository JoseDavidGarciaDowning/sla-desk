package identity_test

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

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
)

// These tests exercise the real verification path: a real RSA signature, a real
// JWKS document fetched over HTTP by the SDK's own client, and the SDK's own
// cache. Nothing about the crypto is stubbed. What is replaced is only where
// the JWKS lives, which is what makes it possible to test a forged signature
// and an expired token without a Clerk instance.
//
// The issuer has to look like a Clerk one: jwt.Verify checks the shape with
// strings.HasPrefix(iss, "https://clerk.") || strings.Contains(iss,
// ".clerk.accounts"). Note what that means in production — the check is on the
// issuer's *shape*, not its identity, so any Clerk instance's issuer passes it.
// What actually binds a token to our instance is the JWKS, and, for the origin
// it was minted for, the authorized party.
const (
	testIssuer = "https://clerk.sla-desk-test.example.com"
	testOrigin = "https://sla-desk.example.com"
)

// fixture is one test's own Clerk: its own signing key, its own key id and its
// own JWKS endpoint.
//
// The key id has to be unique per test. The SDK caches JSON web keys in a
// process-global map keyed by key id alone, with no scoping by instance or
// issuer, so two tests sharing a key id would have the first one's public key
// used to verify the second one's tokens. Discovered the hard way: the caching
// test below started failing with 401 once it ran after another test.
type fixture struct {
	key   *rsa.PrivateKey
	keyID string
	stub  *clerkStub
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	keyID := "ins_key_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())

	return fixture{key: key, keyID: keyID, stub: newClerkStub(t, &key.PublicKey, keyID, knownUserJSON)}
}

// token mints a valid session token for this fixture.
func (f fixture) token(t *testing.T, subject string) string {
	t.Helper()
	return mint(t, f.key, f.keyID, sessionClaims(subject, time.Now().Add(time.Hour)))
}

type clerkStub struct {
	*httptest.Server
	jwksRequests int
}

// newClerkStub serves the two endpoints this package talks to: the JWKS set and
// a single user.
func newClerkStub(t *testing.T, pub *rsa.PublicKey, kid, userJSON string) *clerkStub {
	t.Helper()

	stub := &clerkStub{}
	mux := http.NewServeMux()

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		stub.jwksRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwksDocument(pub, kid)))
	})

	mux.HandleFunc("/users/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(userJSON))
	})

	stub.Server = httptest.NewServer(mux)
	t.Cleanup(stub.Close)
	return stub
}

func jwksDocument(pub *rsa.PublicKey, kid string) string {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return `{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"` + kid + `","n":"` + n + `","e":"` + e + `"}]}`
}

// mint signs a JWT the way Clerk does: RS256, with the key id in the header so
// the verifier knows which JWK to look up.
func mint(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()

	segment := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshalling %v: %v", v, err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}

	signing := segment(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}) +
		"." + segment(claims)

	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func sessionClaims(subject string, expiry time.Time) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": testIssuer,
		"sub": subject,
		"sid": "sess_" + subject,
		"azp": testOrigin,
		"iat": now.Add(-time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"exp": expiry.Unix(),
	}
}

// chain builds the production middleware stack, through the module's own front
// door rather than by reassembling it here: the SDK verifies and caches,
// RequireAuth rejects and resolves.
//
// Going through Module.Authenticate is the point. A test that composed the two
// middlewares itself would keep passing if that method ever mounted only the
// first one — which is the exact failure docs/spec.md §4.3 warns about, because
// the token check alone rejects nothing.
func chain(t *testing.T, stub *clerkStub, users application.UserRepository, next http.Handler) http.Handler {
	t.Helper()

	cfg := clerk.Config{
		SecretKey:       "sk_test_not_a_real_key",
		AuthorizedParty: testOrigin,
		APIURL:          stub.URL,
	}
	m, err := identity.NewWith(users, clerk.NewIdentityProvider(cfg), identity.Config{Clerk: cfg})
	if err != nil {
		t.Fatalf("identity.NewWith: %v", err)
	}
	return m.Authenticate(next)
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

// fakeStore stands in for the users table.
type fakeStore struct {
	user   domain.User
	getErr error

	upserts int
}

var _ application.UserRepository = (*fakeStore)(nil)

func (f *fakeStore) ByClerkID(context.Context, string) (domain.User, error) {
	if f.getErr != nil {
		return domain.User{}, f.getErr
	}
	return f.user, nil
}

func (f *fakeStore) Upsert(_ context.Context, clerkUserID string, id domain.Identity, role domain.Role) (domain.User, error) {
	f.upserts++
	return domain.User{ClerkUserID: clerkUserID, Email: id.Email, Role: role}, nil
}

func (f *fakeStore) ByID(context.Context, uuid.UUID) (domain.User, error) {
	if f.getErr != nil {
		return domain.User{}, f.getErr
	}
	return f.user, nil
}

func (f *fakeStore) GrantRole(_ context.Context, clerkUserID string, role domain.Role) (domain.User, error) {
	return domain.User{ClerkUserID: clerkUserID, Role: role}, nil
}

func request(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

const knownUserJSON = `{
  "id": "user_known",
  "first_name": "Ada",
  "last_name": "Lovelace",
  "primary_email_address_id": "idn_primary",
  "email_addresses": [
    {"id": "idn_old", "email_address": "old@example.test"},
    {"id": "idn_primary", "email_address": "ada@example.test"}
  ]
}`

func TestVerifiedTokenReachesTheHandler(t *testing.T) {
	f := newFixture(t)
	users := &fakeStore{user: domain.User{ClerkUserID: "user_known", Role: domain.RoleCustomer}}

	var protected reached
	handler := chain(t, f.stub, users, protected.handler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request(f.token(t, "user_known")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !protected.called {
		t.Error("the handler did not run for a valid token")
	}
}

// Every way a request can fail authentication answers 401. The SDK alone does
// not do this: a missing or undecodable token passes straight through
// WithHeaderAuthorization, and RequireHeaderAuthorization answers those with
// 403 while a bad signature gets 401 from the failure handler. A mixed surface
// tells an attacker which of their guesses was closer.
func TestEveryAuthenticationFailureAnswers401(t *testing.T) {
	f := newFixture(t)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating an untrusted key: %v", err)
	}
	key, stub := f.key, f.stub

	cases := []struct {
		name  string
		token string
	}{
		{
			name:  "no authorization header",
			token: "",
		},
		{
			name:  "not a JWT at all",
			token: "hunter2",
		},
		{
			name:  "three segments but not decodable",
			token: "aaa.bbb.ccc",
		},
		{
			name:  "expired",
			token: mint(t, key, f.keyID, sessionClaims("user_known", time.Now().Add(-time.Hour))),
		},
		{
			name:  "signed by a key we do not trust",
			token: mint(t, otherKey, f.keyID, sessionClaims("user_known", time.Now().Add(time.Hour))),
		},
		{
			name:  "signature does not match the payload",
			token: tamper(t, mint(t, key, f.keyID, sessionClaims("user_known", time.Now().Add(time.Hour)))),
		},
		{
			name:  "key id we have never issued",
			token: mint(t, key, "ins_unknown_key", sessionClaims("user_known", time.Now().Add(time.Hour))),
		},
		{
			name: "minted for a different origin",
			token: mint(t, key, f.keyID, func() map[string]any {
				c := sessionClaims("user_known", time.Now().Add(time.Hour))
				c["azp"] = "https://attacker.example.com"
				return c
			}()),
		},
		{
			name: "issuer is not a Clerk instance",
			token: mint(t, key, f.keyID, func() map[string]any {
				c := sessionClaims("user_known", time.Now().Add(time.Hour))
				c["iss"] = "https://attacker.example.com"
				return c
			}()),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			users := &fakeStore{user: domain.User{ClerkUserID: "user_known", Role: domain.RoleCustomer}}

			var protected reached
			handler := chain(t, stub, users, protected.handler())

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, request(tc.token))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if protected.called {
				t.Error("the protected handler ran")
			}
			if users.upserts != 0 {
				t.Error("a user row was written for a request that never authenticated")
			}
		})
	}
}

// tamper swaps a character in the payload, leaving the signature behind.
func tamper(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected three segments, got %d", len(parts))
	}
	payload := []byte(parts[1])
	if payload[0] == 'A' {
		payload[0] = 'B'
	} else {
		payload[0] = 'A'
	}
	return parts[0] + "." + string(payload) + "." + parts[2]
}

// The JWK is fetched once and reused. Fetching per request would put a network
// call from Cloud Run to Clerk in front of every authenticated request, and
// make Clerk's availability our availability.
func TestTheJWKIsFetchedOnceAndCached(t *testing.T) {
	f := newFixture(t)

	users := &fakeStore{user: domain.User{ClerkUserID: "user_known", Role: domain.RoleCustomer}}
	handler := chain(t, f.stub, users, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for range 5 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request(f.token(t, "user_known")))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	}

	if f.stub.jwksRequests != 1 {
		t.Errorf("JWKS fetches = %d, want 1 across five requests", f.stub.jwksRequests)
	}
}

// The session token carries the subject and nothing else we need, so a user we
// have never seen costs one call to Clerk. It must pick the primary address,
// not simply the first one.
func TestIdentityFetcherReadsThePrimaryEmail(t *testing.T) {
	f := newFixture(t)

	fetcher := clerk.NewIdentityProvider(clerk.Config{
		SecretKey: "sk_test_not_a_real_key",
		APIURL:    f.stub.URL,
	})

	got, err := fetcher.FetchIdentity(t.Context(), "user_known")
	if err != nil {
		t.Fatalf("FetchIdentity: %v", err)
	}
	if got.Email != "ada@example.test" {
		t.Errorf("email = %q, want the primary address, not the first in the list", got.Email)
	}
	if got.Name != "Ada Lovelace" {
		t.Errorf("name = %q, want Ada Lovelace", got.Name)
	}
}
