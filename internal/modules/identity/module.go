// Package identity is the identity module's front door.
//
// It owns the users table, provisioning from Clerk, and the answer to "who is
// making this request". Other modules never import it: a module that needs to
// know who the caller is declares a contract for that, and the composition root
// connects the two ends. See docs/adr/0005.
package identity

import (
	nethttp "net/http"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres"
	identitydb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres/generated"
	transporthttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
)

// Config is what this module needs from the environment.
//
// It is the module's own shape rather than the application-wide config, so the
// module can be built in a test without one, and so adding an unrelated setting
// elsewhere does not touch this.
type Config struct {
	Clerk         clerk.Config
	WebhookSecret string

	// AgentClerkUserIDs and AdminClerkUserIDs are the Clerk subjects the
	// operator grants a privileged role to. Empty means nobody, which is how
	// slice 1 ran.
	//
	// They arrive as raw lists rather than as a built domain.RoleGrants so that
	// the composition root does not have to know this module's domain types,
	// and so a contradictory pair fails inside the module that owns the rule.
	AgentClerkUserIDs []string
	AdminClerkUserIDs []string
}

// Module is everything this module offers.
type Module struct {
	Service *application.Service

	grants domain.RoleGrants
	cfg    Config
}

// New builds the module against a database handle. This is the production path.
func New(db identitydb.DBTX, cfg Config) (*Module, error) {
	return NewWith(postgres.NewUserRepository(db), clerk.NewIdentityProvider(cfg.Clerk), cfg)
}

// NewWith builds the module from its collaborators.
//
// It exists so a router can be assembled in a test without a database or a live
// Clerk instance, while still running the real middleware and the real service.
// A test that swapped those out would stop proving that a route is protected.
//
// It returns an error because a subject listed as both an agent and an admin
// has no defensible answer, and resolving it by map iteration order would make
// a deployed role depend on nothing anyone can read. Same rule as an unusable
// webhook secret: a configuration that cannot be obeyed stops the process here,
// not later and not per request.
func NewWith(users application.UserRepository, ids application.IdentityProvider, cfg Config) (*Module, error) {
	grants, err := domain.NewRoleGrants(cfg.AgentClerkUserIDs, cfg.AdminClerkUserIDs)
	if err != nil {
		return nil, err
	}

	return &Module{
		Service: application.NewService(users, ids, grants),
		grants:  grants,
		cfg:     cfg,
	}, nil
}

// GrantedCounts is how many subjects hold each privileged role, for a startup
// log line.
//
// Counts and not identifiers: a log naming who the agents are is an inventory
// of privileged accounts, and logs travel further than the database does.
func (m *Module) GrantedCounts() (agents, admins int) {
	return m.grants.CountOf(domain.RoleAgent), m.grants.CountOf(domain.RoleAdmin)
}

// Authenticate is the middleware chain every protected route must sit behind.
//
// Two middlewares, in this order and both required. The first verifies the
// token and attaches claims but rejects nothing; the second is what turns an
// unauthenticated request into a 401 and resolves the caller into one of our
// users. Returning them as one chain is what stops a caller mounting only the
// first and leaving the route open (docs/spec.md §4.3).
func (m *Module) Authenticate(next nethttp.Handler) nethttp.Handler {
	return clerk.Middleware(m.cfg.Clerk)(transporthttp.RequireAuth(m.Service)(next))
}

// WebhookRoute is the path and handler Clerk posts user events to.
//
// It returns an error because an unusable signing secret has to stop the
// process at startup. A route that answers 500 to every delivery, in an
// endpoint nobody watches, is the kind of failure discovered weeks later by a
// user who was never provisioned.
func (m *Module) WebhookRoute() (string, nethttp.Handler, error) {
	h, err := transporthttp.WebhookHandler(m.cfg.WebhookSecret, m.Service)
	if err != nil {
		return "", nil, err
	}
	return transporthttp.WebhookPath, h, nil
}
