package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ErrBadBody means the request body was not one JSON value the caller can read.
//
// One error for four situations — malformed, empty, oversized, or carrying a
// second value — and callers answer 400 to all of them. They are different
// mistakes, but the same refusal, and a handler that told them apart would be
// describing the shape of a body it has already refused to read.
var ErrBadBody = errors.New("httpx: the request body is not one JSON value")

// DecodeJSON reads exactly one JSON value out of a request, up to max bytes.
//
// The limit is a parameter, not a constant here, and that is the one thing that
// changed when this moved out of the ticket module. It used to close over
// maxTicketBody — a constant about how large a *ticket* is, which is precisely
// the knowledge this package may not hold. How big a body may be is the
// caller's decision; reading one is the mechanic this package owns.
//
// The ResponseWriter is a parameter because http.MaxBytesReader needs it to
// mark the connection when the limit is hit. Nothing is written to it here.
//
// Two properties, and each was a bug before it was a rule:
//
//   - The limit is http.MaxBytesReader and not io.LimitReader. LimitReader
//     *truncates* silently, so a body one byte over the cap arrived as invalid
//     JSON and was reported as a syntax error rather than as the size problem
//     it was.
//   - A second value is refused. json.Decoder reads one value and stops, so
//     `{"to":"pending"}{}` decoded happily and the trailing object was never
//     seen. Nothing downstream was wrong about it — it simply was not read —
//     but a client shipping garbage after a valid body should be told rather
//     than served, because the next thing it ships may be the half the caller
//     meant.
//
// Found by CodeRabbit on PR #13 against one endpoint, fixed for all three,
// because a rule that holds where somebody happened to look is not a rule.
func DecodeJSON(w http.ResponseWriter, r *http.Request, into any, max int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	if err := decoder.Decode(into); err != nil {
		return ErrBadBody
	}

	// A second Decode must find nothing at all. io.EOF is the only acceptable
	// answer: any value, and any error other than the end of the input, means
	// the body carried something this endpoint never looked at.
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return ErrBadBody
	}

	return nil
}
