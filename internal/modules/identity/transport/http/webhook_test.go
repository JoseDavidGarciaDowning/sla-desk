package http_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	svix "github.com/svix/svix-webhooks/go"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
)

// A real Svix signing secret is base64 after the whsec_ prefix. This one is not
// a secret: it exists so the tests can sign their own payloads with the same
// library that verifies them, rather than with a reimplementation of the scheme
// that would agree with itself no matter what.
const testWebhookSecret = "whsec_lzIrAAiI15CsoFs852lFmxfJ1xYXoQ5x"

type recordingProvisioner struct {
	calls       int
	clerkUserID string
	identity    domain.Identity
	err         error
}

func (r *recordingProvisioner) Provision(_ context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	r.calls++
	r.clerkUserID = clerkUserID
	r.identity = id
	return domain.User{ClerkUserID: clerkUserID, Email: id.Email}, r.err
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return body
}

// signed builds the request Clerk would send: the raw body plus the three Svix
// headers, signed with the endpoint secret.
func signed(t *testing.T, body []byte, at time.Time) *http.Request {
	t.Helper()

	wh, err := svix.NewWebhook(testWebhookSecret)
	if err != nil {
		t.Fatalf("svix.NewWebhook: %v", err)
	}
	const msgID = "msg_2abcDEF123"
	sig, err := wh.Sign(msgID, at, body)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/clerk", bytesReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("svix-id", msgID)
	r.Header.Set("svix-timestamp", strconv.FormatInt(at.Unix(), 10))
	r.Header.Set("svix-signature", sig)
	return r
}

func TestWebhookRejectsAnUnsignedRequest(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)

	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/clerk",
		bytesReader(fixture(t, "user_created.json")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if users.calls != 0 {
		t.Error("an unsigned payload provisioned a user")
	}
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func newWebhookHandler(t *testing.T, p identityhttp.Provisioner) http.Handler {
	t.Helper()
	h, err := identityhttp.WebhookHandler(testWebhookSecret, p)
	if err != nil {
		t.Fatalf("WebhookHandler: %v", err)
	}
	return h
}

func TestWebhookProvisionsTheUserFromASignedEvent(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signed(t, fixture(t, "user_created.json"), time.Now()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — Svix retries anything else", rec.Code)
	}
	if users.calls != 1 {
		t.Fatalf("upserts = %d, want 1", users.calls)
	}
	if users.clerkUserID != "user_2abcDEF123" {
		t.Errorf("clerk id = %q, want user_2abcDEF123", users.clerkUserID)
	}
	if users.identity.Email != "grace@example.test" {
		t.Errorf("email = %q, want the primary address, not the first in the list", users.identity.Email)
	}
	if users.identity.Name != "Grace Hopper" {
		t.Errorf("name = %q, want Grace Hopper", users.identity.Name)
	}
}

// The signature covers the exact bytes Clerk sent. Changing one of them after
// signing must not verify — otherwise anyone who can see a delivery can replay
// it with a different email and take over an account.
func TestWebhookRejectsATamperedBody(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)

	body := fixture(t, "user_created.json")
	r := signed(t, body, time.Now())

	tampered := bytes.Replace(body, []byte("grace@example.test"), []byte("attacker@evil.test"), 1)
	if bytes.Equal(tampered, body) {
		t.Fatal("the fixture changed; this test is no longer swapping the address")
	}
	r.Body = io.NopCloser(bytes.NewReader(tampered))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if users.calls != 0 {
		t.Error("a tampered payload provisioned a user")
	}
}

// Svix stamps every delivery and refuses one whose timestamp is more than five
// minutes away, which is what stops a captured request being replayed later.
func TestWebhookRejectsAStaleDelivery(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signed(t, fixture(t, "user_created.json"), time.Now().Add(-10*time.Minute)))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if users.calls != 0 {
		t.Error("a replayed delivery provisioned a user")
	}
}

// Clerk sends events we never subscribed to, and adds new ones over time.
// Answering anything but 2xx would make Svix retry them until it gives up.
func TestWebhookIgnoresUnhandledEventTypes(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)

	body := []byte(`{"type":"session.ended","object":"event","data":{"id":"sess_1"}}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signed(t, body, time.Now()))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if users.calls != 0 {
		t.Error("an unrelated event provisioned a user")
	}
}

// Svix retries on any non-2xx, so the same event arrives more than once as a
// matter of course. Both deliveries must succeed; the row stays single because
// the upsert conflicts on clerk_user_id, which
// TestConcurrentUpsertsCreateExactlyOneUser proves against the real database.
func TestWebhookAcceptsTheSameEventTwice(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)
	body := fixture(t, "user_created.json")

	for delivery := 1; delivery <= 2; delivery++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, signed(t, body, time.Now()))
		if rec.Code != http.StatusOK {
			t.Fatalf("delivery %d: status = %d, want 200", delivery, rec.Code)
		}
	}

	if users.calls != 2 {
		t.Errorf("upserts = %d, want 2 — both deliveries must reach the idempotent write", users.calls)
	}
	if users.clerkUserID != "user_2abcDEF123" {
		t.Errorf("clerk id = %q after the replay", users.clerkUserID)
	}
}

// A transient database failure must come back as 5xx precisely so that Svix
// retries. Answering 200 would drop the event on the floor.
func TestWebhookAsksForARetryWhenTheWriteFails(t *testing.T) {
	users := &recordingProvisioner{err: errors.New("connection reset by peer")}
	handler := newWebhookHandler(t, users)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signed(t, fixture(t, "user_created.json"), time.Now()))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 so Svix retries", rec.Code)
	}
}

func TestWebhookRejectsAUserPayloadWithNoID(t *testing.T) {
	users := &recordingProvisioner{}
	handler := newWebhookHandler(t, users)

	body := []byte(`{"type":"user.created","object":"event","data":{"email_addresses":[]}}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signed(t, body, time.Now()))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if users.calls != 0 {
		t.Error("a user with no id was provisioned")
	}
}

func TestWebhookHandlerRefusesAnUnusableSigningSecret(t *testing.T) {
	if _, err := identityhttp.WebhookHandler("not-a-svix-secret", &recordingProvisioner{}); err == nil {
		t.Error("expected an error — a bad secret must stop the deploy, not become runtime 500s")
	}
}

// Every error out of this API is an RFC 9457 problem document. These six paths
// were http.Error, which writes text/plain — an API that answers two different
// media types for the same class of failure teaches whoever reads it next to
// pick whichever they saw first.
//
// The consumer here is Svix rather than a browser, and Svix only reads the
// status. That is an argument for the body not mattering, not an argument for
// it being inconsistent: the delivery log in Clerk's dashboard shows it, and
// it costs nothing to say the same thing everywhere.
func TestWebhookErrorsAreProblemDocuments(t *testing.T) {
	handler, err := identityhttp.WebhookHandler(testWebhookSecret, &recordingProvisioner{})
	if err != nil {
		t.Fatalf("building the handler: %v", err)
	}

	for _, tc := range []struct {
		name    string
		request func(*testing.T) *http.Request
		want    int
	}{
		{
			name: "an unsigned request",
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, identityhttp.WebhookPath,
					bytes.NewReader([]byte(`{"type":"user.created","data":{}}`)))
			},
			want: http.StatusBadRequest,
		},
		{
			name: "a signed body that is not JSON",
			request: func(t *testing.T) *http.Request {
				return signed(t, []byte("not json at all"), time.Now())
			},
			want: http.StatusBadRequest,
		},
		{
			name: "a user event with no id",
			request: func(t *testing.T) *http.Request {
				return signed(t, []byte(`{"type":"user.created","data":{}}`), time.Now())
			},
			want: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tc.request(t))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d\nbody: %s", rec.Code, tc.want, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}

			body := decodeProblem(t, rec)
			if body["status"] != float64(tc.want) {
				t.Errorf("status in body = %v, want %d", body["status"], tc.want)
			}
		})
	}
}
