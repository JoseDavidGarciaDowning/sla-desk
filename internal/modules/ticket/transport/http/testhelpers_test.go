package http_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// decodeProblem reads an RFC 9457 document out of a recorded response.
//
// The documents themselves are tested in internal/platform/httperr, which owns
// them. What the tests here assert is that each handler reaches for one — with
// the right status, and without leaking a cause — not that the encoding works.
func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the problem document: %v\nbody: %s", err, rec.Body.String())
	}
	return body
}
