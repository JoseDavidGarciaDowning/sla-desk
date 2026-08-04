// Package app is the composition root.
//
// It is the only package permitted to import more than one business module, and
// the only place where the contracts a module declares are connected to
// something that satisfies them. Everything it does is wiring: no business rule
// lives here, and an architecture test enforces the import rule in both
// directions — nothing may import this package either.
//
// See docs/adr/0005.
package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/config"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/health"
)

// App is the assembled application.
type App struct {
	Identity *identity.Module
	SLA      *sla.Module
	Ticket   *ticket.Module

	probes map[string]health.Probe
	cfg    config.Config
}

// New builds every module against one database pool and connects them.
//
// The order below is the dependency order, and it is the only place it exists:
// the SLA module knows nothing of tickets, the ticket module knows nothing of
// SLAs or of Clerk, and each is handed an adapter rather than a neighbour.
func New(cfg config.Config, pool *pgxpool.Pool) *App {
	identityModule := identity.New(pool, identity.Config{
		Clerk: clerk.Config{
			SecretKey:       cfg.ClerkSecretKey,
			AuthorizedParty: cfg.ClerkAuthorizedParty,
			APIURL:          cfg.ClerkAPIURL,
		},
		WebhookSecret: cfg.ClerkWebhookSecret,
	})

	slaModule := sla.New(pool)

	// The two adapters are this package's entire reason to exist. Neither
	// argument names a module the ticket module could have imported.
	ticketModule := ticket.New(pool,
		slaPolicies{calculator: slaModule.Calculator},
		callerFromContext,
	)

	return &App{
		Identity: identityModule,
		SLA:      slaModule,
		Ticket:   ticketModule,
		cfg:      cfg,
		// Redis is intentionally not probed: nothing uses it until slice 5, and
		// a health check that fails on an unused dependency would take the
		// service down for no reason.
		probes: map[string]health.Probe{"database": pool.Ping},
	}
}

// NewWith assembles an App from an identity module and the ticket module's two
// collaborators.
//
// It exists so the routing can be exercised without a database: what those
// tests assert is which routes sit behind authentication, and that is a
// property of this package rather than of Postgres.
//
// Note what it does NOT take: the caller adapter. That is wired here, from the
// same unexported function New uses, so a routing test exercises the real
// translation from an authenticated identity to a ticket caller rather than a
// stand-in for it. Passing one in would let a test prove the wiring works by
// supplying the wiring.
func NewWith(cfg config.Config, identityModule *identity.Module,
	repo ticketapp.Repository, sla ticketapp.SLAPolicies,
	probes map[string]health.Probe,
) *App {
	return &App{
		Identity: identityModule,
		Ticket:   ticket.NewWith(repo, sla, callerFromContext),
		cfg:      cfg,
		probes:   probes,
	}
}
