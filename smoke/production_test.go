//go:build smoke

// Package smoke checks that the deployed application is actually up.
//
// It is not a test of behaviour — the unit, integration and end-to-end suites
// own that, and they run against code rather than against a URL. What this
// notices is the class of failure that only exists in production: a certificate
// that expired, a secret that was rotated and not redeployed, a CORS origin
// left pointing at the wrong host, a database the service can no longer reach.
//
// Every check is a read. Nothing here signs in, writes a row, or needs a
// credential, which is what makes it safe to run against production on every
// push and on a schedule.
//
//	go test -tags=smoke ./smoke/...
//
// Override the targets with SMOKE_WEB_URL and SMOKE_API_URL to point it at a
// preview deployment.
package smoke

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	defaultWebURL   = "https://sla-desk.josegd.me"
	defaultAPIURL   = "https://sla-desk-api-vlenh6dmxa-uc.a.run.app"
	defaultClerkAPI = "clerk.sla-desk.josegd.me"
)

// A deployed service that has to be woken from zero instances is allowed to be
// slow. Cloud Run cold starts on this service measured around 1.8s; ten seconds
// is generous enough that a timeout here means something is actually wrong.
const timeout = 10 * time.Second

func webURL() string   { return envOr("SMOKE_WEB_URL", defaultWebURL) }
func apiURL() string   { return envOr("SMOKE_API_URL", defaultAPIURL) }
func clerkAPI() string { return envOr("SMOKE_CLERK_API", defaultClerkAPI) }

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// client does not follow redirects. The redirect is the assertion in one of
// these checks, and a client that followed it would report the destination and
// hide where it came from.
func client() *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func get(t *testing.T, target string) *http.Response {
	t.Helper()

	resp, err := client().Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v\n\nThe host did not answer at all — DNS, TLS or the service being down.", target, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// The API is up and can still reach its database.
//
// /health runs the probes rather than returning a constant, so a 200 here means
// Neon answered. A 503 means the service is alive and telling us the truth
// about a dependency, which is a different problem from the service being gone.
func TestAPIIsHealthyAndCanReachItsDatabase(t *testing.T) {
	resp := get(t, apiURL()+"/health")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200\n\n503 means the service is up and a probe is failing; anything else means the service itself.", resp.StatusCode)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /health: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if got := body.Checks["database"]; got != "ok" {
		t.Errorf("the database probe reports %q — the service is running but cannot reach Neon", got)
	}
}

// An unauthenticated write is refused, and refused in the shape the frontend
// knows how to read.
//
// This is the check that would notice CLERK_SECRET_KEY missing after a deploy:
// the process would not have started at all, so the request would fail to
// connect rather than return 401 — and the message above says so.
func TestTheAPIRefusesAnUnauthenticatedWrite(t *testing.T) {
	resp, err := client().Post(apiURL()+"/api/tickets", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /api/tickets: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /api/tickets = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
}

// The browser can talk to the API from the deployed frontend.
//
// CORS_ALLOWED_ORIGIN is set on the API and the frontend's origin is set at
// build time, in two different systems. Nothing in either one notices when they
// stop agreeing, and the symptom is every authenticated request failing while
// both services look perfectly healthy.
func TestTheAPIAllowsTheDeployedFrontend(t *testing.T) {
	origin := webURL()

	req, err := http.NewRequest(http.MethodOptions, apiURL()+"/api/tickets", nil)
	if err != nil {
		t.Fatalf("building the preflight: %v", err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	resp, err := client().Do(req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q\n\nCORS_ALLOWED_ORIGIN on the API disagrees with where the frontend is served from. Every authenticated request from the browser is failing.", got, origin)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, missing Authorization — the session token cannot be sent", got)
	}
}

// A signed-out visitor is sent to this application's own sign-in route.
//
// This is the T12 regression, and it is worth a permanent check because both
// wrong answers are a 307. Setting signInUrl only on <ClerkProvider> sends the
// user to Clerk's hosted pages on accounts.dev instead, and the status code is
// identical — only the Location header says which happened.
func TestASignedOutVisitorIsSentToOurOwnSignIn(t *testing.T) {
	resp := get(t, webURL()+"/tickets")

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("GET /tickets = %d, want 307", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("Location %q is not a URL: %v", location, err)
	}

	wantHost, err := url.Parse(webURL())
	if err != nil {
		t.Fatalf("SMOKE_WEB_URL is not a URL: %v", err)
	}

	if parsed.Host != wantHost.Host {
		t.Fatalf("redirected to %s, want a route on %s\n\nThis is the T12 failure: the redirect went to Clerk's hosted pages rather than this application's sign-in route. Both answer 307.", parsed.Host, wantHost.Host)
	}
	if parsed.Path != "/sign-in" {
		t.Errorf("redirected to %q, want /sign-in", parsed.Path)
	}
}

// The sign-in route renders rather than erroring.
func TestTheSignInRouteIsServed(t *testing.T) {
	resp := get(t, webURL()+"/sign-in")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sign-in = %d, want 200", resp.StatusCode)
	}
}

// Clerk's Frontend API answers on our domain, with a certificate that is valid
// and not about to expire.
//
// Without this, the application looks completely healthy from the outside — the
// pages load, the API answers — and nobody can sign in, because the browser
// cannot reach Clerk. It is also the one certificate here that this project
// does not control: Vercel's renews itself, and so does Clerk's, but a
// certificate that fails to renew fails silently.
func TestClerksFrontendAPIIsReachableWithAValidCertificate(t *testing.T) {
	host := clerkAPI()

	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: timeout}, "tcp", host+":443",
		&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	)
	if err != nil {
		t.Fatalf("TLS handshake with %s: %v\n\nNo usable certificate. Nobody can sign in — the pages will load and Clerk's script will fail.", host, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		t.Fatal("no peer certificate presented")
	}

	// Renewal is automatic, so the useful signal is not "expired" — by then it
	// is far too late — but "close enough that renewal should already have
	// happened and evidently has not".
	const renewalWindow = 14 * 24 * time.Hour
	if left := time.Until(certs[0].NotAfter); left < renewalWindow {
		t.Errorf("the certificate for %s expires in %s (%s) and has not renewed",
			host, left.Round(time.Hour), certs[0].NotAfter.Format(time.RFC3339))
	}

	resp := get(t, fmt.Sprintf("https://%s/v1/client", host))
	if resp.StatusCode >= 500 {
		t.Errorf("Clerk's Frontend API answered %d", resp.StatusCode)
	}
}
