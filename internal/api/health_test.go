package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/config"
)

func healthRequest(t *testing.T, probes map[string]Probe) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	router := NewRouter(config.Config{DatabaseURL: "postgres://localhost/test"}, probes)
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	return rec
}

func decodeHealth(t *testing.T, rec *httptest.ResponseRecorder) healthResponse {
	t.Helper()

	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	return body
}

func TestHealth_ReportsOKWhenEveryProbePasses(t *testing.T) {
	rec := healthRequest(t, map[string]Probe{
		"database": func(context.Context) error { return nil },
		"cache":    func(context.Context) error { return nil },
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := decodeHealth(t, rec)
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	for name, state := range map[string]string{"database": "ok", "cache": "ok"} {
		if body.Checks[name] != state {
			t.Errorf("checks[%q] = %q, want %q", name, body.Checks[name], state)
		}
	}
}

// A health check that only proves the process is alive proves nothing about the
// deployment. If a dependency is unreachable the endpoint must fail, so the
// platform stops routing traffic to this instance.
func TestHealth_FailsWhenAProbeFails(t *testing.T) {
	rec := healthRequest(t, map[string]Probe{
		"database": func(context.Context) error { return errors.New("connection refused") },
	})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	body := decodeHealth(t, rec)
	if body.Status != "degraded" {
		t.Errorf("status = %q, want %q", body.Status, "degraded")
	}
	if body.Checks["database"] != "failed" {
		t.Errorf("checks[database] = %q, want %q", body.Checks["database"], "failed")
	}
}

// The endpoint is public. Connection strings, hostnames and driver errors must
// never reach the client — they go to the logs instead.
func TestHealth_NeverLeaksProbeErrorDetail(t *testing.T) {
	secret := "postgres://user:hunter2@db.internal:5432/sladesk"

	rec := healthRequest(t, map[string]Probe{
		"database": func(context.Context) error { return errors.New("dial " + secret + ": refused") },
	})

	if got := rec.Body.String(); strings.Contains(got, "hunter2") || strings.Contains(got, "db.internal") {
		t.Errorf("response leaked probe error detail: %s", got)
	}
}

func TestHealth_ReportsOKWithNoProbes(t *testing.T) {
	rec := healthRequest(t, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := decodeHealth(t, rec); body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
}
