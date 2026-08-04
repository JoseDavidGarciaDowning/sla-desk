// Package clerk adapts Clerk's SDK to this module's ports.
//
// Everything Clerk-shaped lives here: the SDK configuration, the JWT
// verification middleware, the Backend API client, and the mapping from a
// Clerk user payload to a domain.Identity. The transport layer above mounts
// these; it does not know what a *clerk.User looks like.
//
// One mapping serves both directions a Clerk user arrives from — the Backend
// API response, and the data field of a user.created or user.updated webhook —
// so the two paths cannot disagree about which address is the user's.
package clerk

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/user"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
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
func (c Config) backendConfig() clerk.BackendConfig {
	cfg := clerk.BackendConfig{Key: clerk.String(c.SecretKey)}
	if c.APIURL != "" {
		cfg.URL = clerk.String(c.APIURL)
	}
	return cfg
}

// Middleware verifies the Clerk session JWT on the Authorization header and
// attaches the resulting claims to the request context.
//
// It does not reject anything, and that is not an oversight in this code — it
// is what clerkhttp.WithHeaderAuthorization does. A request with no token, or
// with a token it cannot even decode, is passed through untouched. The
// transport layer's RequireAuth has to run behind this or the route is open
// (docs/spec.md §4.3).
//
// The SDK caches the JSON web key by key id for an hour, so the JWKS endpoint
// is not called per request. jwt.Verify on its own would not: caching moved to
// the caller in v2.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	opts := []clerkhttp.AuthorizationOption{
		clerkhttp.JWKSClient(jwks.NewClient(&clerk.ClientConfig{BackendConfig: cfg.backendConfig()})),
	}
	if cfg.AuthorizedParty != "" {
		opts = append(opts, clerkhttp.AuthorizedPartyMatches(cfg.AuthorizedParty))
	}
	return clerkhttp.WithHeaderAuthorization(opts...)
}

// SubjectFromContext returns the verified Clerk subject Middleware attached.
//
// It exists so the transport layer never has to import Clerk's SDK just to ask
// whether a request carried a verified session.
func SubjectFromContext(ctx context.Context) (string, bool) {
	claims, ok := clerk.SessionClaimsFromContext(ctx)
	if !ok || claims == nil {
		return "", false
	}
	return claims.Subject, true
}

// identityProvider reads users from Clerk's Backend API.
type identityProvider struct {
	users *user.Client
}

var _ application.IdentityProvider = (*identityProvider)(nil)

// NewIdentityProvider returns the production IdentityProvider.
//
// It is only ever called for a subject we have no row for, which is once per
// user in the life of the system, so the round trip does not sit on the hot
// path.
func NewIdentityProvider(cfg Config) application.IdentityProvider {
	return &identityProvider{
		users: user.NewClient(&clerk.ClientConfig{BackendConfig: cfg.backendConfig()}),
	}
}

func (c *identityProvider) Fetch(ctx context.Context, clerkUserID string) (domain.Identity, error) {
	u, err := c.users.Get(ctx, clerkUserID)
	if err != nil {
		return domain.Identity{}, fmt.Errorf("fetching clerk user %s: %w", clerkUserID, err)
	}
	return IdentityFromUser(u), nil
}

// IdentityFromUser reads what we store out of a Clerk user object.
//
// The same shape arrives from two directions: the Backend API response when
// Resolve fetches a user it has never seen, and the data field of a
// user.created or user.updated webhook event. One mapping serves both, so the
// two paths cannot disagree about which address is the user's.
func IdentityFromUser(u *clerk.User) domain.Identity {
	return domain.Identity{
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
