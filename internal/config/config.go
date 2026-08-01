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
}

// Load reads configuration from the environment, returning the zero Config
// alongside any error so a caller that ignores the error gets nothing usable.
func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("%w: DATABASE_URL", ErrMissingRequired)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = DefaultPort
	}

	return Config{
		Port:              port,
		DatabaseURL:       databaseURL,
		RedisURL:          os.Getenv("REDIS_URL"),
		CORSAllowedOrigin: os.Getenv("CORS_ALLOWED_ORIGIN"),
	}, nil
}
