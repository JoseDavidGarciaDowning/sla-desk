package application

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
)

// stubRepository records what the service asked it to write, so a test can
// assert on the decision rather than on the row it produced.
type stubRepository struct {
	existing *domain.User

	upsertedRole *domain.Role
	grantedRole  *domain.Role
	grantCalls   int
}

func (s *stubRepository) ByClerkID(_ context.Context, _ string) (domain.User, error) {
	if s.existing == nil {
		return domain.User{}, ErrNoSuchUser
	}
	return *s.existing, nil
}

func (s *stubRepository) Upsert(_ context.Context, clerkUserID string, id domain.Identity, role domain.Role) (domain.User, error) {
	s.upsertedRole = &role
	return domain.User{ID: uuid.New(), ClerkUserID: clerkUserID, Email: id.Email, Name: id.Name, Role: role}, nil
}

func (s *stubRepository) GrantRole(_ context.Context, clerkUserID string, role domain.Role) (domain.User, error) {
	s.grantCalls++
	s.grantedRole = &role
	return domain.User{ID: uuid.New(), ClerkUserID: clerkUserID, Role: role}, nil
}

type stubIdentityProvider struct{}

func (stubIdentityProvider) FetchIdentity(_ context.Context, _ string) (domain.Identity, error) {
	return domain.Identity{Email: "someone@example.com", Name: "Someone"}, nil
}

func grantsFor(t *testing.T, agents, admins []string) domain.RoleGrants {
	t.Helper()
	g, err := domain.NewRoleGrants(agents, admins)
	if err != nil {
		t.Fatalf("NewRoleGrants: %v", err)
	}
	return g
}

func TestAnUngrantedSubjectIsProvisionedAsACustomer(t *testing.T) {
	repo := &stubRepository{}
	svc := NewService(repo, stubIdentityProvider{}, grantsFor(t, []string{"user_2agent"}, nil))

	user, err := svc.EnsureUser(context.Background(), "user_2someone")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	if user.Role != domain.RoleCustomer {
		t.Errorf("Role = %q, want %q", user.Role, domain.RoleCustomer)
	}
	if repo.upsertedRole == nil || *repo.upsertedRole != domain.RoleCustomer {
		t.Errorf("Upsert wrote %v, want %q", repo.upsertedRole, domain.RoleCustomer)
	}
}

// The grant has to land on the very first write. Provisioning them as a
// customer and promoting on a later request would leave a window where a listed
// agent signs in and is refused the routes they were granted.
func TestAGrantedSubjectIsProvisionedWithTheGrantedRole(t *testing.T) {
	repo := &stubRepository{}
	svc := NewService(repo, stubIdentityProvider{}, grantsFor(t, []string{"user_2agent"}, []string{"user_2admin"}))

	for _, c := range []struct {
		subject string
		want    domain.Role
	}{
		{"user_2agent", domain.RoleAgent},
		{"user_2admin", domain.RoleAdmin},
	} {
		repo.upsertedRole = nil

		user, err := svc.EnsureUser(context.Background(), c.subject)
		if err != nil {
			t.Fatalf("EnsureUser(%s): %v", c.subject, err)
		}

		if user.Role != c.want {
			t.Errorf("Role = %q, want %q", user.Role, c.want)
		}
		if repo.upsertedRole == nil || *repo.upsertedRole != c.want {
			t.Errorf("Upsert wrote %v, want %q", repo.upsertedRole, c.want)
		}
	}
}

// Someone added to the list after they already signed up is the ordinary case:
// you cannot know a Clerk subject until that person exists. EnsureUser runs on
// every authenticated request, so the promotion lands on their next one.
func TestAnAlreadyProvisionedCustomerIsPromotedOnTheNextRequest(t *testing.T) {
	repo := &stubRepository{existing: &domain.User{
		ID: uuid.New(), ClerkUserID: "user_2agent", Role: domain.RoleCustomer,
	}}
	svc := NewService(repo, stubIdentityProvider{}, grantsFor(t, []string{"user_2agent"}, nil))

	user, err := svc.EnsureUser(context.Background(), "user_2agent")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	if user.Role != domain.RoleAgent {
		t.Errorf("Role = %q, want %q", user.Role, domain.RoleAgent)
	}
	if repo.grantedRole == nil || *repo.grantedRole != domain.RoleAgent {
		t.Errorf("GrantRole wrote %v, want %q", repo.grantedRole, domain.RoleAgent)
	}
}

// EnsureUser runs on every authenticated request. Writing the same role back
// each time would turn every request an agent makes into a database write.
func TestAnAgentAlreadyHoldingTheGrantedRoleIsNotWrittenAgain(t *testing.T) {
	repo := &stubRepository{existing: &domain.User{
		ID: uuid.New(), ClerkUserID: "user_2agent", Role: domain.RoleAgent,
	}}
	svc := NewService(repo, stubIdentityProvider{}, grantsFor(t, []string{"user_2agent"}, nil))

	if _, err := svc.EnsureUser(context.Background(), "user_2agent"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	if repo.grantCalls != 0 {
		t.Errorf("GrantRole was called %d times, want 0 — the role already matches", repo.grantCalls)
	}
}

// Removing an id from the list must not strip the role. A typo in an
// environment variable would otherwise demote an agent mid-shift, silently, on
// their next request. Taking a role away is an explicit action and belongs to
// the admin surface in slice 9.
func TestRemovingASubjectFromTheListDoesNotDemoteThem(t *testing.T) {
	repo := &stubRepository{existing: &domain.User{
		ID: uuid.New(), ClerkUserID: "user_2agent", Role: domain.RoleAgent,
	}}
	svc := NewService(repo, stubIdentityProvider{}, grantsFor(t, nil, nil))

	user, err := svc.EnsureUser(context.Background(), "user_2agent")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	if user.Role != domain.RoleAgent {
		t.Errorf("Role = %q, want the role to survive an empty grant list", user.Role)
	}
	if repo.grantCalls != 0 {
		t.Errorf("GrantRole was called %d times, want 0", repo.grantCalls)
	}
}

// The webhook is the primary provisioning path (docs/spec.md §4.5). It has to
// reach the same decision as the lazy fallback, or which one arrived first
// would determine whether a listed agent got their role.
func TestProvisionAppliesTheSameGrantAsEnsureUser(t *testing.T) {
	repo := &stubRepository{}
	svc := NewService(repo, stubIdentityProvider{}, grantsFor(t, []string{"user_2agent"}, nil))

	user, err := svc.Provision(context.Background(), "user_2agent", domain.Identity{Email: "a@example.com"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if user.Role != domain.RoleAgent {
		t.Errorf("Role = %q, want %q", user.Role, domain.RoleAgent)
	}
}
