package http

import (
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Page sizes. The default keeps a first render small; the maximum stops a
// caller asking for the whole table in one request.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// The paging mechanics below are shared by the two list endpoints — a
// customer's tickets and the agent queue — which page the same way over
// different orderings. The cursor's *meaning* is per-endpoint and stays with
// the feature: list packs created_at, queue packs the coalesced deadline. What
// is common is the packing, the clamping and the filter vocabulary check.

// EncodeCursor packs the sort key of the last row on a page.
//
// Opaque on purpose: a client that parses it becomes coupled to the sort key,
// and changing the ordering would then be a breaking change.
func EncodeCursor(at time.Time, id uuid.UUID) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// ErrBadCursor means the cursor was not one this API issued.
var ErrBadCursor = errors.New("api: cursor is not readable")

// DecodeCursor unpacks what EncodeCursor wrote.
func DecodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, ErrBadCursor
	}

	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.UUID{}, ErrBadCursor
	}

	parsedAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, uuid.UUID{}, ErrBadCursor
	}

	parsedID, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.UUID{}, ErrBadCursor
	}
	return parsedAt, parsedID, nil
}

// PageSize reads ?limit=, clamping anything absent, unreadable or out of range
// to something serviceable rather than rejecting it. A caller asking for 1000
// wants "as many as I can have".
func PageSize(raw string) int32 {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultPageSize
	}
	return int32(min(n, MaxPageSize))
}

// FilterParam reads an optional ?name= filter, checking it against the
// vocabulary the API actually supports.
//
// Absent and empty mean the same thing — no filter. Empty matters because that
// is what a form submits for "any", and treating it as a value would ask the
// database for tickets whose status is the empty string and return none.
//
// A value outside the vocabulary is refused rather than ignored. Ignoring it
// always returns something, but it returns the wrong thing silently: a
// bookmarked ?status=opne would show every ticket while the page said the list
// was filtered. The message names what is allowed, so the client does not have
// to hold a copy of the list to explain the failure.
func FilterParam[T ~string](q url.Values, name string, valid []T) (*string, string) {
	raw := q.Get(name)
	if raw == "" {
		return nil, ""
	}

	if !slices.Contains(valid, T(raw)) {
		return nil, "must be one of " + Join(valid)
	}
	return &raw, ""
}
