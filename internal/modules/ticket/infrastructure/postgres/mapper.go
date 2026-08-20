// Mapping between the generated row types and the domain.
//
// One mapping per table, shared by every read, because two mappings for one
// table drift — and the one that drifts is the one used by a single caller.
package postgres

import (
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

func ticketFrom(row ticketdb.Ticket) domain.Ticket {
	return domain.Ticket{
		ID:          row.ID,
		RequesterID: row.RequesterID,
		AssigneeID:  row.AssigneeID,

		Title:       row.Title,
		Description: row.Description,
		Category:    row.Category,
		Priority:    row.Priority,
		Status:      row.Status,

		SLAPolicyID: row.SlaPolicyID,

		// Stored in microseconds rather than minutes so the reconstruction and
		// the cache can be compared exactly. See docs/spec.md §4.2.
		SLAConsumed:       time.Duration(row.SlaConsumedMicros) * time.Microsecond,
		SLAClockStartedAt: row.SlaClockStartedAt,
		SLADueAt:          row.SlaDueAt,
		SLABreachedAt:     row.SlaBreachedAt,

		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func historyFrom(rows []ticketdb.TicketStatusHistory) []domain.HistoryEntry {
	out := make([]domain.HistoryEntry, len(rows))
	for i, row := range rows {
		out[i] = domain.HistoryEntry{
			FromStatus: row.FromStatus,
			ToStatus:   row.ToStatus,
			ActorID:    row.ActorID,
			ActorRole:  row.ActorRole,
			Reason:     row.Reason,
			CreatedAt:  row.CreatedAt,
		}
	}
	return out
}
