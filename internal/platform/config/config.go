// Package config loads runtime configuration from the environment.
//
// Load returns a value. Nothing here is global and nothing is cached: callers
// hold the Config they were given and pass it explicitly, so tests can build one
// without touching the process environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultPort is used when PORT is unset. Cloud Run always injects PORT, so this
// only applies to local runs.
const DefaultPort = "8080"

// ErrMissingRequired reports a required variable that was absent or empty.
var ErrMissingRequired = errors.New("config: required environment variable is not set")

type Config struct {
	// Port the HTTP server binds to. Cloud Run injects this; hardcoding a port
	// instead makes the container fail to start.
	Port string

	// DatabaseURL is required. Booting without it and failing later, per
	// request, turns a deployment mistake into an intermittent runtime error.
	DatabaseURL string

	// RedisURL is optional until slice 5 introduces the breach queue.
	RedisURL string

	// CORSAllowedOrigin is the single origin permitted to call this API from a
	// browser. Empty disables CORS entirely; a wildcard is never accepted.
	CORSAllowedOrigin string

	// ClerkSecretKey authenticates us to Clerk. Required: every route except
	// the health check needs a verified session, so booting without it would
	// leave an API that can only answer /health.
	ClerkSecretKey string

	// ClerkWebhookSecret verifies the Svix signature on Clerk's user events.
	// Required for the same reason — without it the primary provisioning path
	// in docs/spec.md §4.5 rejects everything Clerk sends.
	ClerkWebhookSecret string

	// ClerkAuthorizedParty is the origin a session token must have been minted
	// for, matched against its azp claim.
	//
	// It defaults to CORSAllowedOrigin because in practice they are the same
	// value: the origin our frontend is served from. The fallback is not
	// convenience, it is a safety net — jwt.Verify only checks the *shape* of
	// the issuer, so without an authorized party any token our Clerk instance
	// minted for any origin is accepted here, and an operator who set the CORS
	// origin and forgot this one would never find out.
	ClerkAuthorizedParty string

	// AgentClerkUserIDs and AdminClerkUserIDs are the Clerk subjects the
	// operator grants a privileged role to. Both are optional and empty by
	// default: an unconfigured deploy has no agents, which is exactly how slice
	// 1 shipped.
	//
	// This is how someone becomes an agent, resolving docs/spec.md §12.4. It is
	// configuration rather than a migration because the subject differs between
	// Clerk's development and production instances, and because the users row
	// it refers to is written when that person first signs up — long after any
	// migration has run. See tasks/slice-2/plan.md decision A.
	//
	// Reading them here keeps them out of git and out of every request. The
	// role still never comes from a token or a webhook payload (§4.5); it comes
	// from the server's own environment, the same place DATABASE_URL comes
	// from.
	AgentClerkUserIDs []string
	AdminClerkUserIDs []string

	// ClerkAPIURL overrides the base URL of Clerk's Backend API. Empty means
	// the real one. It exists because Clerk supports running behind a proxy,
	// and because it is what lets the router be exercised end to end against a
	// stand-in that serves a JWKS we hold the private key for.
	ClerkAPIURL string
}

// Load reads configuration from the environment, returning the zero Config
// alongside any error so a caller that ignores the error gets nothing usable.
func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("%w: DATABASE_URL", ErrMissingRequired)
	}

	clerkSecretKey := os.Getenv("CLERK_SECRET_KEY")
	if clerkSecretKey == "" {
		return Config{}, fmt.Errorf("%w: CLERK_SECRET_KEY", ErrMissingRequired)
	}

	clerkWebhookSecret := os.Getenv("CLERK_WEBHOOK_SECRET")
	if clerkWebhookSecret == "" {
		return Config{}, fmt.Errorf("%w: CLERK_WEBHOOK_SECRET", ErrMissingRequired)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = DefaultPort
	}

	corsOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")

	authorizedParty := os.Getenv("CLERK_AUTHORIZED_PARTY")
	if authorizedParty == "" {
		authorizedParty = corsOrigin
	}

	return Config{
		Port:                 port,
		DatabaseURL:          databaseURL,
		RedisURL:             os.Getenv("REDIS_URL"),
		CORSAllowedOrigin:    corsOrigin,
		ClerkSecretKey:       clerkSecretKey,
		ClerkWebhookSecret:   clerkWebhookSecret,
		ClerkAuthorizedParty: authorizedParty,
		AgentClerkUserIDs:    splitList(os.Getenv("AGENT_CLERK_USER_IDS")),
		AdminClerkUserIDs:    splitList(os.Getenv("ADMIN_CLERK_USER_IDS")),
		ClerkAPIURL:          os.Getenv("CLERK_API_URL"),
	}, nil
}

// splitList parses a comma-separated environment variable, dropping surrounding
// whitespace and empty entries.
//
// Empty entries are dropped rather than preserved because a trailing comma is
// what deleting the last id leaves behind, and an empty subject would be a
// grant that matches whatever row holds an empty clerk_user_id. Nothing else
// here validates the format: Clerk owns what a subject looks like, and a value
// that matches nobody grants nobody.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
