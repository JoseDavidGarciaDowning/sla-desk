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
