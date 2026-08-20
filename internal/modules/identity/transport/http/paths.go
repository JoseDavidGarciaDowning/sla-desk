package http

// The module's paths, in one file, because the route table is only as readable
// as the constants it is written in terms of.
const (
	// MePath is where a caller reads back who our database says they are. It is
	// mounted inside the agent group by the composition root.
	MePath = "/me"

	// AssignablePath is the staff roster, mounted inside the agent group too.
	// It is not reachable from anywhere else.
	AssignablePath = "/assignable"

	// WebhookPath is where Clerk posts user events.
	//
	// It goes straight to the Go API rather than through Next.js, which would
	// add a hop for no reason, and it is the one route that must be mounted
	// outside RequireAuth: Clerk sends a Svix signature, not a session JWT.
	WebhookPath = "/api/webhooks/clerk"
)
