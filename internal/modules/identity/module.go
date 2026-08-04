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
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres/identitydb"
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
}

// Module is everything this module offers.
type Module struct {
	Service *application.Service

	cfg Config
}

// New builds the module against a database handle. This is the production path.
func New(db identitydb.DBTX, cfg Config) *Module {
	return NewWith(postgres.NewUserRepository(db), clerk.NewIdentityProvider(cfg.Clerk), cfg)
}

// NewWith builds the module from its collaborators.
//
// It exists so a router can be assembled in a test without a database or a live
// Clerk instance, while still running the real middleware and the real service.
// A test that swapped those out would stop proving that a route is protected.
func NewWith(users application.UserRepository, ids application.IdentityProvider, cfg Config) *Module {
	return &Module{
		Service: application.NewService(users, ids),
		cfg:     cfg,
	}
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
