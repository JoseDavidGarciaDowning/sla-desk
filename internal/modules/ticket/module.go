// Package ticket is the ticket module's front door.
//
// It owns the tickets and ticket_status_history tables, the status machine, and
// the HTTP endpoints customers and agents use. It imports no other business
// module: what it needs from the SLA module and from identity it declares as
// contracts in its application and transport layers, and internal/app is the
// only place those contracts are connected to implementations.
//
// See docs/adr/0005 for the boundary rules and docs/adr/0006 for why the SLA
// clock is resolved before a transaction rather than inside one.
package ticket

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres"
	transporthttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// Module is everything this module offers.
type Module struct {
	Service *application.Service

	caller transporthttp.CallerResolver
}

// New builds the module.
//
// It takes a pool rather than a DBTX because the write paths begin and end
// their own transactions — that is the whole reason the repository exists.
//
// The two collaborators are the module's contracts, not its dependencies: `sla`
// is whatever can resolve an SLA clock, and `caller` is whatever can say who is
// making a request. Neither names another module, and supplying them is
// internal/app's job.
func New(pool *pgxpool.Pool, sla application.SLAPolicies, caller transporthttp.CallerResolver) *Module {
	return NewWith(postgres.NewRepository(pool), sla, caller)
}

// NewWith builds the module from its collaborators.
//
// It exists so a router can be assembled in a test without a database, while
// still mounting the real handlers behind the real middleware. A test that
// swapped those out would stop proving that a route is protected.
func NewWith(repo application.Repository, sla application.SLAPolicies, caller transporthttp.CallerResolver) *Module {
	return &Module{
		Service: application.NewService(repo, sla),
		caller:  caller,
	}
}

// Routes mounts the ticket endpoints on a router that already carries
// authentication. See transporthttp.Routes.
func (m *Module) Routes(r chi.Router) {
	transporthttp.Routes(r, m.Service, m.caller)
}
