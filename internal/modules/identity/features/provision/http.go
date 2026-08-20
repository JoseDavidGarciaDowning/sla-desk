package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	clerksdk "github.com/clerk/clerk-sdk-go/v2"
	svix "github.com/svix/svix-webhooks/go"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	clerkadapter "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
)

// maxBody caps what we will read before verifying anything. Clerk's user
// events are a few kilobytes; anything near this is not one.
const maxBody = 1 << 20 // 1 MiB

// UseCase is what the adapter needs: the one call that writes the row.
//
// An interface rather than *Handler so a webhook test can exercise signature
// verification and payload handling without a database behind it.
type UseCase interface {
	Provision(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error)
}

// clerkEvent is the envelope Clerk wraps every event in. Data is left raw so
// the payload is only decoded for the event types we act on.
type clerkEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// WebhookHTTP verifies and applies Clerk user events.
//
// It returns an error rather than a handler that fails at request time, so a
// bad signing secret stops the deploy instead of turning into 500s nobody is
// watching.
func WebhookHTTP(signingSecret string, p UseCase) (http.Handler, error) {
	verifier, err := svix.NewWebhook(signingSecret)
	if err != nil {
		return nil, fmt.Errorf("clerk webhook: %w", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The signature covers the exact bytes Clerk sent, so the body has to be
		// read whole and verified before anything is decoded from it.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "unreadable body")
			return
		}

		if err := verifier.Verify(body, r.Header); err != nil {
			// Deliberately vague to the caller, and never echoing the payload.
			// Svix rejects a missing signature, a forged one, and a replayed one
			// whose timestamp is outside five minutes.
			slog.WarnContext(r.Context(), "rejected an unverified Clerk webhook",
				"error", err, "svix_id", r.Header.Get("svix-id"))
			httperr.Write(w, http.StatusBadRequest, "invalid signature")
			return
		}

		var event clerkEvent
		if err := json.Unmarshal(body, &event); err != nil {
			httperr.Write(w, http.StatusBadRequest, "malformed event")
			return
		}

		switch event.Type {
		case "user.created", "user.updated":
			var u clerksdk.User
			if err := json.Unmarshal(event.Data, &u); err != nil {
				httperr.Write(w, http.StatusBadRequest, "malformed user payload")
				return
			}
			if u.ID == "" {
				httperr.Write(w, http.StatusBadRequest, "user payload has no id")
				return
			}

			if _, err := p.Provision(r.Context(), u.ID, clerkadapter.IdentityFrom(&u)); err != nil {
				// 500 on purpose. Svix retries any non-2xx, and a transient
				// database failure is exactly the case where we want it to.
				slog.ErrorContext(r.Context(), "provisioning from a Clerk webhook failed",
					"error", err, "clerk_user_id", u.ID, "event", event.Type)
				httperr.Write(w, http.StatusInternalServerError, "could not provision the user")
				return
			}

		default:
			// Not an error. Clerk sends events we have not subscribed to and
			// events added after this was written; answering anything other
			// than 2xx would make Svix retry them forever.
			slog.InfoContext(r.Context(), "ignoring an unhandled Clerk event", "event", event.Type)
		}

		w.WriteHeader(http.StatusOK)
	}), nil
}
