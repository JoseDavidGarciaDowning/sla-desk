package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const allowedOrigin = "https://sla-desk.vercel.app"

func corsRouter(t *testing.T) http.Handler {
	t.Helper()
	cfg := testConfig()
	cfg.CORSAllowedOrigin = allowedOrigin
	h, err := NewRouter(cfg, Deps{Identity: testIdentity(cfg, routerStubUsers{}), Tickets: testTickets()})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return h
}

func request(t *testing.T, handler http.Handler, method, path, origin string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestCORS_EchoesTheConfiguredOrigin(t *testing.T) {
	rec := request(t, corsRouter(t), http.MethodGet, "/health", allowedOrigin)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, allowedOrigin)
	}
	// Without Vary: Origin a shared cache can serve one origin's response to
	// another, which quietly defeats the whole restriction.
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

func TestCORS_IgnoresOtherOrigins(t *testing.T) {
	rec := request(t, corsRouter(t), http.MethodGet, "/health", "https://evil.example")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it absent for an unlisted origin", got)
	}
	// The request itself still succeeds — CORS is enforced by the browser, not
	// by refusing to answer. What matters is that the header is withheld.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// A wildcard would allow any site on the internet to read authenticated
// responses. It must never appear, whatever the configuration says.
func TestCORS_NeverEmitsAWildcard(t *testing.T) {
	for _, origin := range []string{allowedOrigin, "https://evil.example", "null"} {
		rec := request(t, corsRouter(t), http.MethodGet, "/health", origin)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Errorf("origin %q produced a wildcard Access-Control-Allow-Origin", origin)
		}
	}
}

func TestCORS_AnswersPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", allowedOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	rec := httptest.NewRecorder()
	corsRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, allowedOrigin)
	}
	for _, header := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if rec.Header().Get(header) == "" {
			t.Errorf("%s is missing from the preflight response", header)
		}
	}
}

// Locally the web app and the API share an origin through the dev server, so
// CORS is simply off. An unset value must not become a wildcard.
func TestCORS_IsDisabledWhenNoOriginIsConfigured(t *testing.T) {
	handler, err := NewRouter(testConfig(), Deps{Identity: testIdentity(testConfig(), routerStubUsers{}), Tickets: testTickets()})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	rec := request(t, handler, http.MethodGet, "/health", allowedOrigin)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it absent when CORS is unconfigured", got)
	}
}
