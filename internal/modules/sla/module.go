// Package sla is the SLA module's front door.
//
// It owns the sla_policies table, the budget attached to each priority, and
// every deadline calculation in the system. It has no HTTP surface: nothing is
// exposed to a client directly, and other modules reach it through the
// contracts they declare for themselves — never by importing this package.
// internal/app is the only place allowed to connect the two ends.
package sla

import (
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres/sladb"
)

// Module is everything this module offers.
//
// One field today. It is a struct rather than a bare *Calculator so that adding
// a second capability — the breach checker in slice 5 — is an added field here
// instead of a changed signature at every call site.
type Module struct {
	Calculator *application.Calculator
}

// New builds the module against a database handle.
//
// The handle is a DBTX rather than a pool: these are reads of reference data,
// and the caller decides whether they run on the pool or inside a transaction
// it already owns.
func New(db sladb.DBTX) *Module {
	return &Module{
		Calculator: application.NewCalculator(postgres.NewPolicyRepository(db)),
	}
}
