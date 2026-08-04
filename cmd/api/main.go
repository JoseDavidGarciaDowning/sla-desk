// Command api serves the SLA Desk HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/app"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/config"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket"
)

// startupPingTimeout bounds the one connectivity check made at boot.
const startupPingTimeout = 5 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("server stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("configuring the database pool: %w", err)
	}
	defer pool.Close()

	// pgxpool connects lazily, so reaching the database once at boot turns a
	// wrong connection string into a loud log line at deploy time rather than a
	// surprise on the first request.
	//
	// It is not fatal. A database that is briefly unreachable should not stop
	// the container from starting: /health reports the state and the platform
	// decides whether to route traffic here.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), startupPingTimeout)
	if err := pool.Ping(pingCtx); err != nil {
		slog.Error("database unreachable at startup; serving anyway, /health will report degraded",
			"error", err)
	} else {
		slog.Info("database reachable")
	}
	cancelPing()

	// Redis is intentionally not probed: nothing uses it until slice 5, and a
	// health check that fails on an unused dependency would take the service
	// down for no reason.
	probes := map[string]app.Probe{
		"database": pool.Ping,
	}

	// The identity module builds its own repository and Clerk client from the
	// handle and its own config. This is the only place that knows both exist.
	identityModule := identity.New(pool, identity.Config{
		Clerk: clerk.Config{
			SecretKey:       cfg.ClerkSecretKey,
			AuthorizedParty: cfg.ClerkAuthorizedParty,
			APIURL:          cfg.ClerkAPIURL,
		},
		WebhookSecret: cfg.ClerkWebhookSecret,
	})

	// The SLA module reads reference data on the pool. The ticket module gets
	// it as the contract it declared, never as the module itself: it is handed
	// something that can resolve a clock, and does not learn where from.
	slaModule := sla.New(pool)
	ticketModule := ticket.New(pool, app.SLAPolicies{Calculator: slaModule.Calculator})

	handler, err := app.NewRouter(cfg, app.Deps{
		Probes:   probes,
		Identity: identityModule,
		Tickets:  ticketModule,
	})
	if err != nil {
		// Configuration the router cannot work with, most likely a malformed
		// Clerk webhook secret. Failing here rather than serving an endpoint
		// that rejects every delivery.
		return fmt.Errorf("building the router: %w", err)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
		// Without this a slow client can hold a connection open indefinitely
		// while dribbling out headers.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Cloud Run sends SIGTERM before stopping a container. Draining in-flight
	// requests instead of dropping them is the difference between a deploy that
	// is invisible to users and one that returns errors during every rollout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)

		<-ctx.Done()
		slog.Info("shutdown signal received, draining connections")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}()

	slog.Info("listening", "port", cfg.Port)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	<-shutdownDone
	slog.Info("stopped cleanly")

	return nil
}
