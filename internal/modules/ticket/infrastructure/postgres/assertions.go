package postgres

import (
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/assign"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/create"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/get"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/history"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/list"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/queue"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/transition"
)

// The ports this adapter satisfies, asserted here rather than beside each
// method, so the whole set is one list a reader can check against the features
// directory.
//
// Each is declared by the feature that needs it, and each is narrow: creating a
// ticket names one method, not ten. That is why the assertions are worth having
// — nothing else forces this type to keep satisfying a port whose only other
// mention is in a package that imports it.
//
// Nine entries and no Repository. The ten-method interface these replaced was
// one type every handler could reach through; now the only thing that sees all
// nine is the module's front door, which is the one place that should.
var (
	_ create.Tickets       = (*Repository)(nil)
	_ list.Tickets         = (*Repository)(nil)
	_ get.Tickets          = (*Repository)(nil)
	_ get.AgentTickets     = (*Repository)(nil)
	_ history.Tickets      = (*Repository)(nil)
	_ history.AgentTickets = (*Repository)(nil)
	_ queue.Tickets        = (*Repository)(nil)
	_ assign.Tickets       = (*Repository)(nil)
	_ transition.Tickets   = (*Repository)(nil)
)
