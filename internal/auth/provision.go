package auth

import (
	"context"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
)

// Provision writes the users row for a Clerk identity, creating it if this is
// the first time we have seen them and refreshing the details if it is not.
//
// Both paths in docs/spec.md §4.5 end here: the Clerk webhook, which is the
// primary one, and the lazy fallback in RequireAuth that covers the window
// where a browser already holds a valid token and the webhook has not landed
// yet. Whichever arrives first creates the row; the other updates it.
//
// The role is not a parameter, and that is the point. It is written as a
// literal by the query and is absent from the conflict clause, so a webhook
// payload can neither create an agent nor demote one.
func Provision(ctx context.Context, p Provisioner, clerkUserID string, id Identity) (store.User, error) {
	var name *string
	if id.Name != "" {
		name = &id.Name
	}

	return p.UpsertUserFromClerk(ctx, store.UpsertUserFromClerkParams{
		ClerkUserID: clerkUserID,
		Email:       id.Email,
		Name:        name,
	})
}

// IdentityFromClerkUser reads what we store out of a Clerk user object.
//
// The same shape arrives from two directions: the Backend API response when
// RequireAuth fetches a user it has never seen, and the data field of a
// user.created or user.updated webhook event. One mapping serves both, so the
// two paths cannot disagree about which address is the user's.
func IdentityFromClerkUser(u *clerk.User) Identity {
	return Identity{
		Email: primaryEmail(u),
		Name:  fullName(u),
	}
}

// primaryEmail picks the address Clerk marks as primary. A user can have
// several, and the first in the list is not necessarily the one they sign in
// with; falling back to it is better than storing nothing, since the column is
// NOT NULL.
func primaryEmail(u *clerk.User) string {
	if u.PrimaryEmailAddressID != nil {
		for _, addr := range u.EmailAddresses {
			if addr != nil && addr.ID == *u.PrimaryEmailAddressID {
				return addr.EmailAddress
			}
		}
	}
	for _, addr := range u.EmailAddresses {
		if addr != nil && addr.EmailAddress != "" {
			return addr.EmailAddress
		}
	}
	return ""
}

// fullName is empty when Clerk holds no name, which it often does: a user who
// signed up with an email and a password has given us nothing else. The empty
// string becomes a NULL name rather than a blank one.
func fullName(u *clerk.User) string {
	parts := make([]string, 0, 2)
	if u.FirstName != nil && *u.FirstName != "" {
		parts = append(parts, *u.FirstName)
	}
	if u.LastName != nil && *u.LastName != "" {
		parts = append(parts, *u.LastName)
	}
	return strings.Join(parts, " ")
}
