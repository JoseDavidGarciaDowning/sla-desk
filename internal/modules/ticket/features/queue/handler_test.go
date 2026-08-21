package queue_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/queue"
)

// queueStubRepo records the filter the service passed down.
type queueStubRepo struct {
	got    queue.Filter
	called bool
}

func (r *queueStubRepo) ListForQueue(_ context.Context, f queue.Filter) ([]queue.Entry, error) {
	r.called = true
	r.got = f
	return nil, nil
}

// A zero scope is a caller who forgot to set one. Defaulting it to "any" would
// turn forgetting into "return every ticket", on the one query in this module
// with no predicate to fall back on — so it must not reach the repository at
// all.
func TestTheQueueRefusesAFilterWithNoAssigneeScope(t *testing.T) {
	repo := &queueStubRepo{}
	svc := queue.New(repo)

	_, err := svc.Handle(context.Background(), queue.Filter{PageSize: 10})

	if !errors.Is(err, queue.ErrUnsetScope) {
		t.Errorf("error = %v, want ErrUnsetAssigneeScope", err)
	}
	if repo.called {
		t.Error("the repository was asked for the unscoped set anyway")
	}
}

func TestAScopedQueueFilterReachesTheRepository(t *testing.T) {
	repo := &queueStubRepo{}
	svc := queue.New(repo)

	if _, err := svc.Handle(context.Background(),
		queue.Filter{Assignee: queue.Unassigned, PageSize: 10}); err != nil {
		t.Fatalf("Queue: %v", err)
	}

	if !repo.called {
		t.Fatal("the repository was never asked")
	}
	if repo.got.Assignee != queue.Unassigned {
		t.Errorf("Assignee = %q, want it passed through unchanged", repo.got.Assignee)
	}
}
