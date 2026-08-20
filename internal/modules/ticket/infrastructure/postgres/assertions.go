package postgres

import (
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/create"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/get"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/history"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/list"
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
// application.Repository is still here because the agent use cases have not
// moved yet. It shrinks to nothing in the slice that moves them.
var (
	_ create.Tickets         = (*Repository)(nil)
	_ list.Tickets           = (*Repository)(nil)
	_ get.Tickets            = (*Repository)(nil)
	_ history.Tickets        = (*Repository)(nil)
	_ application.Repository = (*Repository)(nil)
)
