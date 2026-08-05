package config

import (
	"errors"
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
	if got != (Config{}) {
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
			if got != (Config{}) {
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
