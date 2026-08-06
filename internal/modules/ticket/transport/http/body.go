package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// errBadBody means the request body was not one JSON value this endpoint can
// read.
var errBadBody = errors.New("api: the request body is not one JSON value")

// decodeBody reads exactly one JSON value out of a request.
//
// Three endpoints decode a body and each did it slightly differently, which is
// how the rule ended up applying to some of them:
//
//   - The limit was io.LimitReader in one and http.MaxBytesReader in the others.
//     LimitReader *truncates* silently, so a body one byte over the cap arrives
//     as invalid JSON and is reported as a syntax error; MaxBytesReader fails
//     with a size error instead, which is the true reason.
//   - None of them rejected a second value. json.Decoder reads one value and
//     stops, so `{"to":"pending"}{}` decoded happily and the trailing object
//     was never seen. Nothing downstream was wrong about it — it simply was not
//     read — but a client shipping garbage after a valid body should be told
//     rather than served, because the next thing it ships may be the half the
//     caller meant.
//
// Found by CodeRabbit on PR #13, against the transition endpoint. Fixed in one
// place instead of that one, because two endpoints left lax is a rule that
// holds where somebody happened to look.
func decodeBody(w http.ResponseWriter, r *http.Request, into any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTicketBody))
	if err := decoder.Decode(into); err != nil {
		return errBadBody
	}

	// A second Decode must find nothing at all. io.EOF is the only acceptable
	// answer: any value, and any error other than the end of the input, means
	// the body carried something this endpoint never looked at.
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return errBadBody
	}

	return nil
}
