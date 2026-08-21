// Package identity is the identity module's front door.
//
// It owns the users table, provisioning from Clerk, and the answer to "who is
// making this request". Other modules never import it: a module that needs to
// know who the caller is declares a contract for that, and the composition root
// connects the two ends. See docs/adr/0005.
//
// This is also where the module is assembled — the features are constructed
// here and handed to the route table already built, the same arrangement the
// ticket module uses and for the same reason.
package identity

import (
	nethttp "net/http"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/features/assignable"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/features/me"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/features/provision"
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

// Store is everything this module needs from persistence: the union of the
// narrow ports each feature declares for itself.
//
// It exists so the composition root can supply one adapter and a test one fake.
// No feature holds it — provision.Handler holds provision.Users and cannot read
// the roster.
type Store interface {
	provision.Users
	assignable.Users
}

// Module is everything this module offers: one handler per use case.
//
// Provision is exported because the middleware needs it — it is what satisfies
// transport's UserSource — and because the composition root builds the webhook
// route from it. Assignable is exported because the ticket module's assignee
// contract is satisfied by it, through an adapter in internal/app.
type Module struct {
	Provision  *provision.Handler
	Assignable *assignable.Handler

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
// Clerk instance, while still running the real middleware and the real use
// cases. A test that swapped those out would stop proving that a route is
// protected.
//
// It returns an error because a subject listed as both an agent and an admin
// has no defensible answer, and resolving it by map iteration order would make
// a deployed role depend on nothing anyone can read. Same rule as an unusable
// webhook secret: a configuration that cannot be obeyed stops the process here,
// not later and not per request.
func NewWith(store Store, ids provision.Identities, cfg Config) (*Module, error) {
	grants, err := domain.NewRoleGrants(cfg.AgentClerkUserIDs, cfg.AdminClerkUserIDs)
	if err != nil {
		return nil, err
	}

	return &Module{
		Provision:  provision.New(store, ids, grants),
		Assignable: assignable.New(store),
		grants:     grants,
		cfg:        cfg,
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
	return clerk.Middleware(m.cfg.Clerk)(transporthttp.RequireAuth(m.Provision)(next))
}

// WebhookRoute is the path and handler Clerk posts user events to.
//
// It returns an error because an unusable signing secret has to stop the
// process at startup. A route that answers 500 to every delivery, in an
// endpoint nobody watches, is the kind of failure discovered weeks later by a
// user who was never provisioned.
//
// It is returned on its own rather than in AgentHandlers because it is mounted
// outside the authenticated group: Clerk sends a Svix signature, not a session
// JWT, so RequireAuth would reject every delivery.
func (m *Module) WebhookRoute() (string, nethttp.Handler, error) {
	h, err := provision.WebhookHTTP(m.cfg.WebhookSecret, m.Provision)
	if err != nil {
		return "", nil, err
	}
	return transporthttp.WebhookPath, h, nil
}

// AgentHTTPHandlers builds the module's agent-group endpoints, ready to be
// mounted behind a role check.
func (m *Module) AgentHTTPHandlers() transporthttp.AgentHandlers {
	return transporthttp.AgentHandlers{
		Me:         me.HTTP(),
		Assignable: assignable.HTTP(m.Assignable),
	}
}
