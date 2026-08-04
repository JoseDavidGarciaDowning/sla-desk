// Package sla is the SLA module's front door.
//
// It owns the sla_policies table, the budget attached to each priority, and
// every deadline calculation in the system (docs/adr/0001). It has no HTTP
// surface: nothing here is exposed to a client directly, and other modules
// reach it through contracts they declare for themselves rather than by
// importing this package. See docs/adr/0005.
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
// rather than a changed signature at every call site.
type Module struct {
	Calculator *application.Calculator
}

// New builds the module against a database handle.
//
// The handle is a DBTX rather than a pool, and that is what lets a caller which
// already owns a transaction build the module on it. Resolving a policy is a
// read of reference data; whether it runs on the pool or inside someone else's
// transaction is the caller's decision, not this module's.
func New(db sladb.DBTX) *Module {
	return &Module{
		Calculator: application.NewCalculator(postgres.NewPolicyRepository(db)),
	}
}
