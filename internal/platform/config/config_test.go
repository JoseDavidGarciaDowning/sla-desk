package config

import (
	"errors"
	"reflect"
	"testing"
)

func TestLoad_ReadsEveryValueFromTheEnvironment(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/sladesk")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("CORS_ALLOWED_ORIGIN", "https://sla-desk.vercel.app")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("CLERK_AUTHORIZED_PARTY", "https://explicit.example.com")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}

	if got.Port != "9090" {
		t.Errorf("Port = %q, want %q", got.Port, "9090")
	}
	if want := "postgres://user:pass@localhost:5432/sladesk"; got.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", got.DatabaseURL, want)
	}
	if want := "redis://localhost:6379"; got.RedisURL != want {
		t.Errorf("RedisURL = %q, want %q", got.RedisURL, want)
	}
	if want := "https://sla-desk.vercel.app"; got.CORSAllowedOrigin != want {
		t.Errorf("CORSAllowedOrigin = %q, want %q", got.CORSAllowedOrigin, want)
	}
	if got.ClerkSecretKey != "sk_test_key" {
		t.Errorf("ClerkSecretKey = %q", got.ClerkSecretKey)
	}
	if got.ClerkWebhookSecret != "whsec_test" {
		t.Errorf("ClerkWebhookSecret = %q", got.ClerkWebhookSecret)
	}
	if want := "https://explicit.example.com"; got.ClerkAuthorizedParty != want {
		t.Errorf("ClerkAuthorizedParty = %q, want the explicit value to win", got.ClerkAuthorizedParty)
	}
}

// Cloud Run injects PORT and the process must bind to it. Locally there is no
// PORT, so a default keeps `make api` working without a full environment.
func TestLoad_DefaultsPortWhenUnset(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}

	if got.Port != DefaultPort {
		t.Errorf("Port = %q, want the default %q", got.Port, DefaultPort)
	}
}

// A missing database URL must stop the process at startup. Booting without one
// and failing later, per request, turns a configuration mistake into an
// intermittent runtime error.
func TestLoad_RefusesToStartWithoutADatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	got, err := Load()

	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("error = %v, want one wrapping %v", err, ErrMissingRequired)
	}
	if !reflect.DeepEqual(got, Config{}) {
		t.Errorf("config = %+v, want the zero value — a rejected config must yield nothing usable", got)
	}
}

// Redis is not used until slice 5 and CORS is only needed once a browser calls
// the API from another origin. Neither may block startup today.
func TestLoad_TreatsRedisAndCORSAsOptional(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("REDIS_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGIN", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}
	if got.RedisURL != "" || got.CORSAllowedOrigin != "" {
		t.Errorf("optional values should stay empty, got RedisURL=%q CORS=%q",
			got.RedisURL, got.CORSAllowedOrigin)
	}
}

// Every route but the health check needs a verified session, so an API that
// booted without these could only answer /health. Failing at startup is the
// whole point of loading configuration up front.
func TestLoad_RefusesToStartWithoutTheClerkSecrets(t *testing.T) {
	for _, missing := range []string{"CLERK_SECRET_KEY", "CLERK_WEBHOOK_SECRET"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
			t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
			t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
			t.Setenv(missing, "")

			got, err := Load()

			if !errors.Is(err, ErrMissingRequired) {
				t.Fatalf("error = %v, want one wrapping %v", err, ErrMissingRequired)
			}
			if !reflect.DeepEqual(got, Config{}) {
				t.Errorf("config = %+v, want the zero value", got)
			}
		})
	}
}

// An operator who sets the CORS origin and forgets the authorized party would
// otherwise silently lose the check that ties a token to our frontend, because
// jwt.Verify only validates the shape of the issuer.
func TestLoad_AuthorizedPartyFallsBackToTheCORSOrigin(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("CORS_ALLOWED_ORIGIN", "https://sla-desk.vercel.app")
	t.Setenv("CLERK_AUTHORIZED_PARTY", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://sla-desk.vercel.app"; got.ClerkAuthorizedParty != want {
		t.Errorf("ClerkAuthorizedParty = %q, want it to fall back to %q", got.ClerkAuthorizedParty, want)
	}
}

// The two grant lists are how someone becomes an agent (docs/spec.md §12.4).
// They are read from the environment rather than seeded by a migration, because
// the subject differs between Clerk's development and production instances and
// the users row does not exist until that person signs up.
func TestLoad_ReadsTheRoleGrantLists(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("AGENT_CLERK_USER_IDS", "user_2agent,user_3agent")
	t.Setenv("ADMIN_CLERK_USER_IDS", "user_2admin")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}

	if want := []string{"user_2agent", "user_3agent"}; !equalStrings(got.AgentClerkUserIDs, want) {
		t.Errorf("AgentClerkUserIDs = %q, want %q", got.AgentClerkUserIDs, want)
	}
	if want := []string{"user_2admin"}; !equalStrings(got.AdminClerkUserIDs, want) {
		t.Errorf("AdminClerkUserIDs = %q, want %q", got.AdminClerkUserIDs, want)
	}
}

// An unconfigured deploy must have no agents. Empty is the safe answer, not an
// error, because the API is fully functional without a single agent — slice 1
// shipped that way.
func TestLoad_RoleGrantListsAreEmptyByDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("AGENT_CLERK_USER_IDS", "")
	t.Setenv("ADMIN_CLERK_USER_IDS", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}

	if len(got.AgentClerkUserIDs) != 0 {
		t.Errorf("AgentClerkUserIDs = %q, want empty", got.AgentClerkUserIDs)
	}
	if len(got.AdminClerkUserIDs) != 0 {
		t.Errorf("AdminClerkUserIDs = %q, want empty", got.AdminClerkUserIDs)
	}
}

// Whitespace around a comma is what a human types, and a trailing comma is what
// is left behind after deleting the last entry. Neither may become a subject
// that matches nobody — or worse, an empty string that matches a row.
func TestLoad_RoleGrantListsTolerateHumanFormatting(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/sladesk")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_key")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("AGENT_CLERK_USER_IDS", "  user_2agent ,, user_3agent  ,  ")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}

	if want := []string{"user_2agent", "user_3agent"}; !equalStrings(got.AgentClerkUserIDs, want) {
		t.Errorf("AgentClerkUserIDs = %q, want %q", got.AgentClerkUserIDs, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
