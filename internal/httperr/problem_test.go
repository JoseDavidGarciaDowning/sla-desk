package httperr_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/httperr"
)

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the problem document: %v\nbody: %s", err, rec.Body.String())
	}
	return body
}

func TestProblemIsServedAsProblemJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	httperr.Write(rec, http.StatusNotFound, "no ticket with that id")

	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}

	body := decodeProblem(t, rec)
	if body["status"] != float64(http.StatusNotFound) {
		t.Errorf("status in body = %v, want 404 — it must agree with the HTTP status", body["status"])
	}
	if body["title"] != "Not Found" {
		t.Errorf("title = %v, want Not Found", body["title"])
	}
	if body["detail"] != "no ticket with that id" {
		t.Errorf("detail = %v", body["detail"])
	}
	if body["type"] != "about:blank" {
		t.Errorf("type = %v, want about:blank when there is no specific type", body["type"])
	}
}

func TestValidationProblemCarriesPerFieldMessages(t *testing.T) {
	rec := httptest.NewRecorder()
	httperr.WriteValidation(rec, map[string]string{
		"title":    "must not be empty",
		"priority": `must be one of urgent, high, normal, low`,
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	body := decodeProblem(t, rec)
	errs, ok := body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors = %v, want an object keyed by field name", body["errors"])
	}
	if errs["title"] != "must not be empty" {
		t.Errorf("errors.title = %v", errs["title"])
	}
	if errs["priority"] == nil {
		t.Error("errors.priority is missing")
	}
}

// A 500 must say nothing about why. Connection strings, table names and driver
// messages all end up in error text, and an error page is the cheapest place
// for an attacker to read them.
func TestInternalProblemNeverEchoesTheCause(t *testing.T) {
	rec := httptest.NewRecorder()
	httperr.WriteInternal(rec)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	body := decodeProblem(t, rec)
	detail, _ := body["detail"].(string)
	for _, leak := range []string{"postgres", "pgx", "sql", "connection", "sladesk"} {
		if strings.Contains(strings.ToLower(detail+rec.Body.String()), leak) {
			t.Errorf("the response mentions %q: %s", leak, rec.Body.String())
		}
	}
}
