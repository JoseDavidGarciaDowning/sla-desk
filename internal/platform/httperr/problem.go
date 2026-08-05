// Package httperr decides what a failure looks like on the wire.
//
// It is the one package every layer of this service may call, and it exists
// because that was not true of the writer it replaces. The problem document
// lived in internal/api, which imports internal/auth — so the middleware that
// rejects unauthenticated requests could not use it without an import cycle,
// and answered with a bare status line and no body instead. internal/api's own
// webhook handler, meanwhile, used http.Error and answered text/plain. Three
// ways to report a failure in a service that claims to speak one.
//
// The admission rule here is narrow on purpose: does this decide how an error
// appears in a response? If yes it belongs; if no it does not. A package named
// for the fact that several callers use it would have no rule at all — "more
// than one thing imports it" is a fact about the call graph, not about the
// concept — and could never turn anything away.
//
// It imports nothing from this module, and a test in internal/ticket enforces
// that. A single import of a package here would put httperr above it, and the
// next layer needing to report an error would find it out of reach — which is
// exactly the position it was extracted to escape.
package httperr

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// contentType is what RFC 9457 requires. A client can tell an error document
// from a successful one by the media type alone, without parsing it.
const contentType = "application/problem+json"

// Problem is an RFC 9457 problem document.
//
// The standard is worth following rather than inventing a shape: clients and
// tooling already know it, and the members below are the ones it defines.
// Errors is an extension member, which the RFC explicitly allows.
type Problem struct {
	// Type identifies the kind of problem. about:blank means "nothing more
	// specific than the status code", which is the honest answer for most of
	// what this API returns.
	Type string `json:"type"`

	// Title is the human-readable summary. It does not change from occurrence
	// to occurrence — that is what Detail is for.
	Title string `json:"title"`

	// Status repeats the HTTP status code, so a document that has been logged
	// or forwarded still says what happened.
	Status int `json:"status"`

	// Detail describes this occurrence. Never an internal error message: see
	// WriteInternal.
	Detail string `json:"detail,omitempty"`

	// Errors carries per-field validation messages.
	Errors map[string]string `json:"errors,omitempty"`
}

// Write sends a problem document with the given status and detail.
func Write(w http.ResponseWriter, status int, detail string) {
	write(w, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// WriteValidation reports every rejected field at once.
//
// All of them, not the first: a client fixing one field at a time across four
// round trips is a worse experience than one that shows every error in the
// form.
func WriteValidation(w http.ResponseWriter, fields map[string]string) {
	write(w, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusBadRequest),
		Status: http.StatusBadRequest,
		Detail: "the request body failed validation",
		Errors: fields,
	})
}

// WriteInternal reports a failure the caller can do nothing about.
//
// It takes no detail on purpose. Error text in Go accumulates whatever the
// layers below wrapped into it — driver messages, table names, connection
// strings — and an error response is the cheapest place for someone to read
// them. The cause belongs in the logs, which is where the caller's handler puts
// it.
func WriteInternal(w http.ResponseWriter) {
	write(w, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
		Detail: "the request could not be completed",
	})
}

func write(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(p.Status)

	if err := json.NewEncoder(w).Encode(p); err != nil {
		// The status line is already on the wire, so there is nothing left to
		// tell the client. Recording it is all that is left to do.
		slog.Error("writing a problem document failed", "error", err, "status", p.Status)
	}
}
