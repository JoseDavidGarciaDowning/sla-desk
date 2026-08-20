// Package clerk is the identity module's adapter to Clerk.
//
// Everything that knows Clerk's SDK exists lives here. The layers above work in
// domain.Identity and domain.User, so replacing the provider would be a change
// in this package and nowhere else.
package clerk

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	clerksdk "github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/user"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
)

// Config is everything this package needs to talk to Clerk.
type Config struct {
	// SecretKey authenticates us to Clerk's Backend API.
	SecretKey string

	// AuthorizedParty is the origin our frontend is served from, matched
	// against the token's azp claim.
	//
	// Without it, any token minted by our Clerk instance for any origin is
	// accepted here. That matters because jwt.Verify only checks the shape of
	// the issuer — strings.HasPrefix(iss, "https://clerk.") or a
	// ".clerk.accounts" substring — so issuer validation alone does not tie a
	// token to us. Empty disables the check, which is only appropriate before
	// a frontend origin exists.
	AuthorizedParty string

	// APIURL overrides Clerk's API base URL. Empty means the real one. Set by
	// tests, and by anyone running Clerk behind a proxy.
	APIURL string
}

// backendConfig builds the SDK client configuration.
//
// clerk.SetKey is deliberately not used. It writes a package-level secret that
// every client in the process then reads implicitly, and docs/spec.md §8 rules
// out global state: dependencies are passed explicitly.
func (c Config) backendConfig() clerksdk.BackendConfig {
	cfg := clerksdk.BackendConfig{Key: clerksdk.String(c.SecretKey)}
	if c.APIURL != "" {
		cfg.URL = clerksdk.String(c.APIURL)
	}
	return cfg
}

// Middleware verifies the Clerk session JWT on the Authorization header and
// attaches the resulting claims to the request context.
//
// It does not reject anything, and that is not an oversight in this code — it
// is what clerkhttp.WithHeaderAuthorization does. A request with no token, or
// with a token it cannot even decode, is passed through untouched. RequireAuth
// has to run behind this or the route is open (docs/spec.md §4.3).
//
// The SDK caches the JSON web key by key id for an hour, so the JWKS endpoint
// is not called per request. jwt.Verify on its own would not: caching moved to
// the caller in v2.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	opts := []clerkhttp.AuthorizationOption{
		clerkhttp.JWKSClient(jwks.NewClient(&clerksdk.ClientConfig{BackendConfig: cfg.backendConfig()})),
	}
	if cfg.AuthorizedParty != "" {
		opts = append(opts, clerkhttp.AuthorizedPartyMatches(cfg.AuthorizedParty))
	}
	return clerkhttp.WithHeaderAuthorization(opts...)
}

// IdentityProvider reads users from Clerk's Backend API.
//
// Exported, and returning it concretely rather than as the interface it
// satisfies, because the feature that consumes it also owns the mapper this
// package holds: the webhook is handed a Clerk user in its payload and has to
// turn it into a domain.Identity. Naming provision.Identities here would point
// this package at that one while that one points back, and the cycle is real
// rather than stylistic.
//
// "Accept interfaces, return structs" is the usual phrasing of the rule, and
// this is the case that shows why it is not only taste. The port is asserted
// where it is consumed — identity.NewWith takes a provision.Identities, so a
// provider that stopped satisfying it fails to compile at the call site.
type IdentityProvider struct {
	users *user.Client
}

// NewIdentityProvider builds the production provider.
//
// It is only ever called for a subject we have no row for, which is once per
// user in the life of the system, so the round trip does not sit on the hot
// path.
func NewIdentityProvider(cfg Config) *IdentityProvider {
	return &IdentityProvider{
		users: user.NewClient(&clerksdk.ClientConfig{BackendConfig: cfg.backendConfig()}),
	}
}

func (c *IdentityProvider) FetchIdentity(ctx context.Context, clerkUserID string) (domain.Identity, error) {
	u, err := c.users.Get(ctx, clerkUserID)
	if err != nil {
		return domain.Identity{}, fmt.Errorf("fetching clerk user %s: %w", clerkUserID, err)
	}
	return IdentityFrom(u), nil
}

// IdentityFrom reads what we store out of a Clerk user object.
//
// The same shape arrives from two directions: the Backend API response when a
// subject we have never seen makes a request, and the data field of a
// user.created or user.updated webhook event. One mapping serves both, so the
// two paths cannot disagree about which address is the user's.
func IdentityFrom(u *clerksdk.User) domain.Identity {
	return domain.Identity{
		Email: primaryEmail(u),
		Name:  fullName(u),
	}
}

// primaryEmail picks the address Clerk marks as primary. A user can have
// several, and the first in the list is not necessarily the one they sign in
// with; falling back to it is better than storing nothing, since the column is
// NOT NULL.
func primaryEmail(u *clerksdk.User) string {
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
func fullName(u *clerksdk.User) string {
	parts := make([]string, 0, 2)
	if u.FirstName != nil && *u.FirstName != "" {
		parts = append(parts, *u.FirstName)
	}
	if u.LastName != nil && *u.LastName != "" {
		parts = append(parts, *u.LastName)
	}
	return strings.Join(parts, " ")
}
