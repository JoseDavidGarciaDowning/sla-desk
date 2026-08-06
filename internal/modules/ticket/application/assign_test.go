package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// assignStubRepo records the write the service asked for, so a test can assert
// on the decision rather than on a row.
type assignStubRepo struct {
	queueStubRepo

	assigned  bool
	gotTicket uuid.UUID
	gotWhom   *uuid.UUID
}

func (r *assignStubRepo) Assign(_ context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error) {
	r.assigned = true
	r.gotTicket = ticketID
	r.gotWhom = assignee
	return domain.Ticket{ID: ticketID, AssigneeID: assignee}, nil
}

// stubDirectory answers whether an id may hold tickets, without the ticket
// module learning what an agent is.
type stubDirectory struct {
	canHold    bool
	err        error
	askedAbout *uuid.UUID
}

func (d *stubDirectory) CanHoldTickets(_ context.Context, id uuid.UUID) (bool, error) {
	d.askedAbout = &id
	return d.canHold, d.err
}

func assignService(repo application.Repository, dir application.AssigneeDirectory) *application.Service {
	return application.NewService(repo, nil, dir)
}

func TestAnAgentCanBeAssignedATicket(t *testing.T) {
	repo := &assignStubRepo{}
	dir := &stubDirectory{canHold: true}
	svc := assignService(repo, dir)

	ticketID, agent := uuid.New(), uuid.New()

	got, err := svc.Assign(context.Background(), ticketID, &agent)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if got.AssigneeID == nil || *got.AssigneeID != agent {
		t.Errorf("AssigneeID = %v, want %s", got.AssigneeID, agent)
	}
	if dir.askedAbout == nil || *dir.askedAbout != agent {
		t.Errorf("the directory was asked about %v, want %s", dir.askedAbout, agent)
	}
}

// assignee_id is a bare foreign key to users: the database will happily put a
// customer there. The ticket module cannot check it — it does not know the
// identity module exists (ADR 0005) — so it declares what it needs and the
// composition root answers.
func TestACustomerCannotBeAssignedATicket(t *testing.T) {
	repo := &assignStubRepo{}
	svc := assignService(repo, &stubDirectory{canHold: false})

	customer := uuid.New()
	_, err := svc.Assign(context.Background(), uuid.New(), &customer)

	if !errors.Is(err, application.ErrNotAssignable) {
		t.Fatalf("error = %v, want ErrNotAssignable", err)
	}
	if repo.assigned {
		t.Error("the ticket was assigned to a customer anyway")
	}
}

// An id that names nobody gets the same answer as a customer's id. Two
// different refusals would let an agent enumerate which uuids name real users.
func TestAnUnknownIDIsRefusedLikeACustomer(t *testing.T) {
	repo := &assignStubRepo{}
	svc := assignService(repo, &stubDirectory{canHold: false})

	_, err := svc.Assign(context.Background(), uuid.New(), ptr(uuid.New()))

	if !errors.Is(err, application.ErrNotAssignable) {
		t.Errorf("error = %v, want ErrNotAssignable — the same answer a customer gets", err)
	}
}

// Unassigning is not a lookup. There is nobody to ask about, and asking anyway
// would make "take this off my plate" fail when the directory is unreachable.
func TestUnassigningDoesNotConsultTheDirectory(t *testing.T) {
	repo := &assignStubRepo{}
	dir := &stubDirectory{canHold: false}
	svc := assignService(repo, dir)

	got, err := svc.Assign(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if dir.askedAbout != nil {
		t.Error("the directory was consulted for an unassignment")
	}
	if !repo.assigned {
		t.Fatal("the unassignment was never written")
	}
	if got.AssigneeID != nil {
		t.Errorf("AssigneeID = %v, want nil", got.AssigneeID)
	}
}

// A directory that cannot answer must not be read as "no". Refusing on a
// transport failure would turn a momentary outage into "this agent does not
// exist", which is a lie the caller would act on.
func TestADirectoryFailureIsNotARefusal(t *testing.T) {
	repo := &assignStubRepo{}
	boom := errors.New("the directory is unreachable")
	svc := assignService(repo, &stubDirectory{canHold: false, err: boom})

	_, err := svc.Assign(context.Background(), uuid.New(), ptr(uuid.New()))

	if errors.Is(err, application.ErrNotAssignable) {
		t.Error("a directory failure was reported as a refusal")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the cause to survive", err)
	}
	if repo.assigned {
		t.Error("the ticket was assigned despite the directory failing")
	}
}

func ptr(id uuid.UUID) *uuid.UUID { return &id }
