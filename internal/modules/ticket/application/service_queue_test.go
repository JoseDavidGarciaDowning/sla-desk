package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// queueStubRepo records the filter the service passed down.
type queueStubRepo struct {
	got    application.QueueFilter
	called bool
}

func (r *queueStubRepo) ListForQueue(_ context.Context, f application.QueueFilter) ([]application.QueueEntry, error) {
	r.called = true
	r.got = f
	return nil, nil
}

func (r *queueStubRepo) Create(context.Context, application.NewTicket, application.SLAClock) (domain.Ticket, error) {
	return domain.Ticket{}, nil
}

func (r *queueStubRepo) Transition(context.Context, application.StatusChange, application.SLAClock) (domain.Ticket, error) {
	return domain.Ticket{}, nil
}
func (r *queueStubRepo) PolicyIDOf(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (r *queueStubRepo) ListForRequester(context.Context, application.ListFilter) ([]domain.Ticket, error) {
	return nil, nil
}

func (r *queueStubRepo) OneForRequester(context.Context, uuid.UUID, uuid.UUID) (domain.Ticket, error) {
	return domain.Ticket{}, nil
}

func (r *queueStubRepo) HistoryForRequester(context.Context, uuid.UUID, uuid.UUID) ([]domain.HistoryEntry, error) {
	return nil, nil
}

func (r *queueStubRepo) OneByID(context.Context, uuid.UUID) (domain.Ticket, error) {
	return domain.Ticket{}, nil
}

func (r *queueStubRepo) Timeline(context.Context, uuid.UUID) ([]domain.HistoryEntry, error) {
	return nil, nil
}

// A zero scope is a caller who forgot to set one. Defaulting it to "any" would
// turn forgetting into "return every ticket", on the one query in this module
// with no predicate to fall back on — so it must not reach the repository at
// all.
func TestTheQueueRefusesAFilterWithNoAssigneeScope(t *testing.T) {
	repo := &queueStubRepo{}
	svc := application.NewService(repo, nil)

	_, err := svc.Queue(context.Background(), application.QueueFilter{PageSize: 10})

	if !errors.Is(err, application.ErrUnsetAssigneeScope) {
		t.Errorf("error = %v, want ErrUnsetAssigneeScope", err)
	}
	if repo.called {
		t.Error("the repository was asked for the unscoped set anyway")
	}
}

func TestAScopedQueueFilterReachesTheRepository(t *testing.T) {
	repo := &queueStubRepo{}
	svc := application.NewService(repo, nil)

	if _, err := svc.Queue(context.Background(),
		application.QueueFilter{Assignee: application.AssigneeUnassigned, PageSize: 10}); err != nil {
		t.Fatalf("Queue: %v", err)
	}

	if !repo.called {
		t.Fatal("the repository was never asked")
	}
	if repo.got.Assignee != application.AssigneeUnassigned {
		t.Errorf("Assignee = %q, want it passed through unchanged", repo.got.Assignee)
	}
}
