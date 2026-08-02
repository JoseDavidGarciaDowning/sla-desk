package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/config"
)

func testRouter() http.Handler {
	return NewRouter(
		config.Config{Port: "8080", DatabaseURL: "postgres://localhost/test"},
		nil,
	)
}

func TestHealth_RespondsAsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

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
			testRouter().ServeHTTP(rec, httptest.NewRequest(method, "/health", nil))

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
