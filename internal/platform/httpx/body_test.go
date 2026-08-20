package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// The cap is the caller's, and that is the difference from the version of this
// that lived in the ticket module.
//
// It used to close over maxTicketBody, a constant about how big a ticket is —
// which is exactly the knowledge this package may not hold (see the package
// comment). Passing the limit in is what lets a second transport reuse the
// mechanics without inheriting the ticket module's idea of a large body.
func TestTheLimitIsTheCallers(t *testing.T) {
	body := `{"name":"` + strings.Repeat("x", 200) + `"}`

	var generous struct {
		Name string `json:"name"`
	}
	if err := decode(t, body, 64<<10, &generous); err != nil {
		t.Errorf("a 64 KiB limit refused a %d byte body: %v", len(body), err)
	}

	var stingy struct {
		Name string `json:"name"`
	}
	if err := decode(t, body, 32, &stingy); !errors.Is(err, httpx.ErrBadBody) {
		t.Errorf("a 32 byte limit accepted a %d byte body: err = %v", len(body), err)
	}
}

// json.Decoder reads one value and stops, so a body with something after it
// decodes happily and the trailing value is never seen. A client shipping
// garbage after a valid body is told, because the next thing it ships may be
// the half the caller meant.
func TestAValueAfterTheBodyIsRefused(t *testing.T) {
	var into struct {
		To string `json:"to"`
	}

	if err := decode(t, `{"to":"pending"}{}`, 64<<10, &into); !errors.Is(err, httpx.ErrBadBody) {
		t.Errorf("a value after the body was accepted: err = %v", err)
	}
}

func TestOneValueDecodes(t *testing.T) {
	var into struct {
		To string `json:"to"`
	}

	if err := decode(t, `{"to":"pending"}`, 64<<10, &into); err != nil {
		t.Fatalf("decoding one value: %v", err)
	}
	if into.To != "pending" {
		t.Errorf("To = %q, want pending", into.To)
	}
}

func TestMalformedJSONIsRefused(t *testing.T) {
	var into struct{}

	if err := decode(t, `{"to":`, 64<<10, &into); !errors.Is(err, httpx.ErrBadBody) {
		t.Errorf("malformed JSON was accepted: err = %v", err)
	}
}

// An empty body is not one JSON value either. It reaches here as io.EOF on the
// first Decode rather than the second, and both are the same refusal.
func TestAnEmptyBodyIsRefused(t *testing.T) {
	var into struct{}

	if err := decode(t, ``, 64<<10, &into); !errors.Is(err, httpx.ErrBadBody) {
		t.Errorf("an empty body was accepted: err = %v", err)
	}
}

func decode(t *testing.T, body string, max int64, into any) error {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	return httpx.DecodeJSON(httptest.NewRecorder(), req, into, max)
}
