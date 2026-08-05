// Package httpx holds the HTTP mechanics more than one transport repeats:
// writing a JSON body, and deciding which browser origin may talk to us.
//
// The admission rule is the same one internal/httperr states for itself, and it
// is narrow on purpose: does this decide how bytes get onto the wire, without
// knowing what the bytes mean? If yes it belongs here; if no it belongs in the
// module whose concept it is. A helper that knew what a ticket is would fail
// that test — which is the point, because a package that cannot turn anything
// away becomes the place everything ends up.
//
// It imports nothing from this module, and an architecture test enforces that.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// WriteJSON sends body as JSON with the given status.
//
// The request is a parameter only so a failure can be logged with its context
// and path. Nothing about the response depends on it.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already on the wire, so the client cannot be told.
		// Log it rather than swallowing it silently.
		slog.ErrorContext(r.Context(), "encoding response failed",
			"error", err, "path", r.URL.Path)
	}
}
