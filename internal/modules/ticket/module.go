// Package ticket is the ticket module's front door.
//
// It owns the tickets and ticket_status_history tables and the status machine.
// It imports no other business module: what it needs from the SLA module and
// from identity it declares as contracts of its own, and the composition root
// is the only place those contracts are connected to an implementation.
//
// This is also where the module is assembled — the features are constructed
// here and handed to the route table already built. That direction is what
// keeps a feature from importing its neighbours, and this package from being
// something a feature could reach back into.
//
// See docs/adr/0005 for the boundary rules, docs/adr/0008 for why the SLA clock
// is resolved before a transaction rather than inside one, and docs/adr/0012
// for why the postgres adapter names the feature packages.
package ticket

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/create"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/get"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/history"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/list"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
	transporthttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// Store is everything this module needs from persistence.
//
// The union of the narrow ports each feature declares for itself, and it exists
// for exactly one reason: the composition root supplies one adapter, and a test
// supplies one fake. No feature holds this — create.Handler holds create.Tickets
// and can reach one method. A handler given this interface could reach all of
// them, which is the coupling the per-feature ports removed.
type Store interface {
	create.Tickets
	list.Tickets
	get.Tickets
	history.Tickets
	application.Repository
}

// Module is everything this module offers.
//
// The customer use cases are their own handlers. Service is what remains of the
// agent's, and it goes away in the slice that moves them.
type Module struct {
	Create  *create.Handler
	List    *list.Handler
	Get     *get.Handler
	History *history.Handler

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
// root's job. `dir` is the second contract: whatever can answer whether a user
// may hold tickets. Like `sla` it names no other module.
func New(pool *pgxpool.Pool, sla ports.SLAPolicies, dir application.AssigneeDirectory) *Module {
	return NewWith(postgres.NewRepository(pool), sla, dir)
}

// NewWith builds the module from its collaborators.
//
// It exists so handlers can be exercised in a test without a database, while
// still running the real use cases.
func NewWith(store Store, sla ports.SLAPolicies, dir application.AssigneeDirectory) *Module {
	return &Module{
		Create:  create.New(store, sla),
		List:    list.New(store),
		Get:     get.New(store),
		History: history.New(store),

		Service: application.NewService(store, sla, dir),
	}
}

// HTTPHandlers builds the module's customer endpoints, ready to be mounted.
//
// The resolver is a parameter because where a caller comes from is the
// composition root's business, not this module's (docs/adr/0005). Returning
// built handlers rather than exposing the features is what lets the route table
// stay free of any feature import — see the package comment in transport/http.
func (m *Module) HTTPHandlers(resolve ports.CallerResolver) transporthttp.Handlers {
	return transporthttp.Handlers{
		Create:  create.HTTP(m.Create, resolve),
		List:    list.HTTP(m.List, resolve),
		Get:     get.HTTP(m.Get, resolve),
		History: history.HTTP(m.History, resolve),
	}
}
