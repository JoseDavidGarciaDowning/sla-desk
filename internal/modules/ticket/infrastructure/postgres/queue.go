package postgres

import (
	"context"
	"fmt"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

// ticketFrom maps a row onto the domain entity.
//
// Deliberately not returning ticketdb.Ticket. Nothing above this package should
// depend on the shape of the table: adding a column must not change the type
// every handler reads.
// ListForQueue reads every ticket, in deadline order.
//
// There is no requester predicate and that is the design, not an omission: an
// agent reads tickets that are not theirs, so the guarantee that used to live
// in the SQL now lives in where the handler is mounted (tasks/slice-2/plan.md
// decisions B and C). The customer's ListForRequester is untouched and still
// carries its own.
//
// The cursor's deadline is coalesced here rather than in the query's parameter,
// so the value compared is the one the ORDER BY produced. A paused ticket's
// position is 'infinity', and passing its bare NULL would make the row
// comparison NULL and drop every paused ticket from the next page.
func (r *Repository) ListForQueue(ctx context.Context, f application.QueueFilter) ([]application.QueueEntry, error) {
	rows, err := r.q.ListTicketsForQueue(ctx, ticketdb.ListTicketsForQueueParams{
		Status:         (*string)(f.Status),
		Priority:       (*string)(f.Priority),
		AssigneeFilter: string(f.Assignee),
		AssigneeID:     f.AssigneeID,
		AfterDueAt:     f.AfterDueAt,
		AfterID:        f.AfterID,
		PageSize:       f.PageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("listing the queue: %w", err)
	}

	out := make([]application.QueueEntry, len(rows))
	for i, row := range rows {
		name := ""
		if row.RequesterName != nil {
			name = *row.RequesterName
		}

		// Both are carried as stored. Clerk holds no name for someone who
		// signed up with an email and a password, so one of them is often
		// empty — which of the two to show is a display decision and is made
		// in the DTO, not here.
		out[i] = application.QueueEntry{
			Ticket:         ticketFrom(queueRowToTicket(row)),
			RequesterName:  name,
			RequesterEmail: row.RequesterEmail,
		}
	}
	return out, nil
}

// queueRowToTicket drops the joined columns so the ticket itself is mapped by
// the same function every other read uses. Two mappings for one table would
// drift, and the one that drifted would be the one used by a single caller.
func queueRowToTicket(row ticketdb.ListTicketsForQueueRow) ticketdb.Ticket {
	return ticketdb.Ticket{
		ID:                row.ID,
		RequesterID:       row.RequesterID,
		AssigneeID:        row.AssigneeID,
		Title:             row.Title,
		Description:       row.Description,
		Category:          row.Category,
		Priority:          row.Priority,
		Status:            row.Status,
		SlaPolicyID:       row.SlaPolicyID,
		SlaConsumedMicros: row.SlaConsumedMicros,
		SlaClockStartedAt: row.SlaClockStartedAt,
		SlaDueAt:          row.SlaDueAt,
		SlaBreachedAt:     row.SlaBreachedAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
