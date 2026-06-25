package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestWillEnqueueRun_BacklogToTerminalDoesNotEnqueue locks the rule that moving
// an issue out of backlog into a terminal status (done / cancelled / archived)
// must NOT start an agent run. Archiving a backlog issue is the regression that
// motivated routing this case through util.IsTerminalIssueStatus: before that,
// the guard only excluded done/cancelled, so backlog→archived wrongly enqueued.
//
// These cases short-circuit at the switch's default arm before any DB access,
// so a zero-value IssueService (nil Queries) is sufficient.
func TestWillEnqueueRun_BacklogToTerminalDoesNotEnqueue(t *testing.T) {
	assignedIssue := func(status string) db.Issue {
		return db.Issue{
			Status:       status,
			AssigneeType: pgtype.Text{String: "agent", Valid: true},
			AssigneeID:   pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		}
	}

	s := &IssueService{}

	for _, terminal := range []string{"done", "cancelled", "archived"} {
		t.Run("backlog_to_"+terminal, func(t *testing.T) {
			_, ok := s.WillEnqueueRun(context.Background(), IssueTriggerInput{
				Issue:         assignedIssue(terminal),
				PrevStatus:    "backlog",
				StatusChanged: true,
			}, IssueTriggerProbe{})
			if ok {
				t.Fatalf("backlog→%s must not enqueue a run, but WillEnqueueRun returned ok=true", terminal)
			}
		})
	}
}
