package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/user"
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
// with a token it cannot even decode, is passed through untouched. RequireAuth
// has to run behind this or the route is open (docs/spec.md §4.3).
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

// clerkIdentities reads users from Clerk's Backend API.
type clerkIdentities struct {
	users *user.Client
}

// NewIdentityFetcher returns the production IdentityFetcher.
//
// It is only ever called for a subject we have no row for, which is once per
// user in the life of the system, so the round trip does not sit on the hot
// path.
func NewIdentityFetcher(cfg Config) IdentityFetcher {
	return &clerkIdentities{
		users: user.NewClient(&clerk.ClientConfig{BackendConfig: cfg.backendConfig()}),
	}
}

func (c *clerkIdentities) FetchIdentity(ctx context.Context, clerkUserID string) (Identity, error) {
	u, err := c.users.Get(ctx, clerkUserID)
	if err != nil {
		return Identity{}, fmt.Errorf("fetching clerk user %s: %w", clerkUserID, err)
	}
	return Identity{
		Email: primaryEmail(u),
		Name:  fullName(u),
	}, nil
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
