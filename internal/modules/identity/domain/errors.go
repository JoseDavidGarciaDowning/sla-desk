package domain

import "errors"

// ErrNoSuchUser means we hold no row for that Clerk subject, or that id.
//
// In the domain rather than with a feature because both of them raise it and
// the repository translates into it: provisioning asks "have we seen this
// subject", the roster asks "is this id one of ours", and the answer "no" is
// the same business fact in each.
//
// Declared here rather than re-exported from the driver, so the provisioning
// path can tell "we have never seen them" apart from "the database is down"
// without importing pgx to do it.
var ErrNoSuchUser = errors.New("identity: no user for that Clerk subject")
