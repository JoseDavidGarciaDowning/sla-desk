package http_test

import (
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// Clerk holds no name for anyone who signed up with an email and a password,
// so an agent queue rendering the name column verbatim would show blanks — rows
// nobody can attribute. The fallback is a display decision and lives here
// rather than in the query.
func TestTheQueueEntryFallsBackToTheEmailWhenThereIsNoName(t *testing.T) {
	cases := []struct {
		name  string
		entry application.QueueEntry
		want  string
	}{
		{
			name: "a name is used as it stands",
			entry: application.QueueEntry{
				RequesterName:  "Ada Lovelace",
				RequesterEmail: "ada@example.test",
			},
			want: "Ada Lovelace",
		},
		{
			name:  "an absent name falls back to the address",
			entry: application.QueueEntry{RequesterEmail: "ada@example.test"},
			want:  "ada@example.test",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tickethttp.NewQueueEntryResponse(tc.entry)
			if got.RequesterName != tc.want {
				t.Errorf("RequesterName = %q, want %q", got.RequesterName, tc.want)
			}
		})
	}
}

// The requester's id must not travel. It is another user's primary key: putting
// it on the wire hands out an identifier to enumerate, which is the reasoning
// that kept actor_id out of the history DTO in T14b.
func TestTheQueueEntryNeverCarriesTheRequestersID(t *testing.T) {
	entry := application.QueueEntry{
		Ticket:         domain.Ticket{Title: "a ticket"},
		RequesterName:  "Ada",
		RequesterEmail: "ada@example.test",
	}

	got := tickethttp.NewQueueEntryResponse(entry)
	if got.RequesterName != "Ada" {
		t.Fatalf("RequesterName = %q", got.RequesterName)
	}
	// The wire type has no field for it, which is what makes this structural
	// rather than a check somebody has to remember to run.
	if got.TicketResponse.Title != "a ticket" {
		t.Errorf("the embedded ticket did not survive the mapping")
	}
}
