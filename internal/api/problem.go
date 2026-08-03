package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// problemContentType is what RFC 9457 requires. A client can tell an error
// document from a successful one by the media type alone, without parsing it.
const problemContentType = "application/problem+json"

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
	// WriteInternalProblem.
	Detail string `json:"detail,omitempty"`

	// Errors carries per-field validation messages.
	Errors map[string]string `json:"errors,omitempty"`
}

// WriteProblem sends a problem document with the given status and detail.
func WriteProblem(w http.ResponseWriter, status int, detail string) {
	writeProblem(w, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// WriteValidationProblem reports every rejected field at once.
//
// All of them, not the first: a client fixing one field at a time across four
// round trips is a worse experience than one that shows every error in the
// form.
func WriteValidationProblem(w http.ResponseWriter, fields map[string]string) {
	writeProblem(w, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusBadRequest),
		Status: http.StatusBadRequest,
		Detail: "the request body failed validation",
		Errors: fields,
	})
}

// WriteInternalProblem reports a failure the caller can do nothing about.
//
// It takes no detail on purpose. Error text in Go accumulates whatever the
// layers below wrapped into it — driver messages, table names, connection
// strings — and an error response is the cheapest place for someone to read
// them. The cause belongs in the logs, which is where the caller's handler puts
// it.
func WriteInternalProblem(w http.ResponseWriter) {
	writeProblem(w, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
		Detail: "the request could not be completed",
	})
}

func writeProblem(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(p.Status)

	if err := json.NewEncoder(w).Encode(p); err != nil {
		// The status line is already on the wire, so there is nothing left to
		// tell the client. Recording it is all that is left to do.
		slog.Error("writing a problem document failed", "error", err, "status", p.Status)
	}
}
