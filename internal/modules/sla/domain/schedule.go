// Package sla owns every deadline calculation in the system.
//
// Nothing here touches a database, an HTTP request, or the wall clock: `now` is
// always a parameter. HTTP handlers, the breach checker and the frontend consume
// the values this package returns and never recompute them.
//
// See docs/spec.md §4.2 for the clock model and docs/adr/0001 for why a single
// calculation path exists.
package domain

import "time"

type Always24x7 struct{}

func (Always24x7) Elapsed(from, to time.Time) time.Duration {
	if !to.After(from) {
		return 0
	}
	return to.Sub(from)
}

// DueAt reports when `remaining` budget runs out, counting from `from`.
//
// Negative `remaining` values are preserved, yielding a deadline in the past.
func (Always24x7) DueAt(from time.Time, remaining time.Duration) time.Time {
	return from.Add(remaining)
}
