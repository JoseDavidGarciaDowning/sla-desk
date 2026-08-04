// Package ticket is the ticket module's front door.
//
// It owns the tickets and ticket_status_history tables and the status machine.
// It imports no other business module: what it needs from the SLA module it
// declares as a contract in its application layer, and the composition root is
// the only place that contract is connected to an implementation.
//
// See docs/adr/0005 for the boundary rules and docs/adr/0008 for why the SLA
// clock is resolved before a transaction rather than inside one.
package ticket

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres"
)

// Module is everything this module offers.
type Module struct {
	Service *application.Service
}

// New builds the module.
//
// It takes a pool rather than a DBTX because the write paths begin and end
// their own transactions — that is the whole reason the repository exists. The
// SLA module takes a handle for the opposite reason: it only reads, and its
// caller decides where.
//
// `sla` is the module's contract, not its dependency: whatever can resolve an
// SLA clock. It names no other module, and supplying it is the composition
// root's job.
func New(pool *pgxpool.Pool, sla application.SLAPolicies) *Module {
	return NewWith(postgres.NewRepository(pool), sla)
}

// NewWith builds the module from its collaborators.
//
// It exists so handlers can be exercised in a test without a database, while
// still running the real use cases.
func NewWith(repo application.Repository, sla application.SLAPolicies) *Module {
	return &Module{Service: application.NewService(repo, sla)}
}
