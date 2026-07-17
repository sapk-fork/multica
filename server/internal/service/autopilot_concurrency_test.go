package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/dispatch"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestConcurrencyLimitReached pins the pure admission arithmetic for the
// max_concurrent_runs gate (M-87): a positive limit blocks once active runs
// meet or exceed it, and a non-positive limit never blocks (unlimited).
func TestConcurrencyLimitReached(t *testing.T) {
	cases := []struct {
		name   string
		limit  int32
		active int64
		want   bool
	}{
		{"unlimited zero never blocks", 0, 0, false},
		{"unlimited zero ignores active runs", 0, 5, false},
		{"negative treated as unlimited", -1, 5, false},
		{"under limit admits", 3, 2, false},
		{"at limit blocks", 3, 3, true},
		{"over limit blocks", 3, 4, true},
		{"limit one first run admits", 1, 0, false},
		{"limit one blocks second", 1, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := concurrencyLimitReached(tc.limit, tc.active); got != tc.want {
				t.Fatalf("concurrencyLimitReached(%d, %d) = %v, want %v", tc.limit, tc.active, got, tc.want)
			}
		})
	}
}

// TestShouldSkipDispatchConcurrencyGate is the DB-backed acceptance test for the
// gate: with a positive cap already met by in-flight runs, dispatch is skipped
// with ReasonConcurrencyLimit BEFORE the leader/readiness gates run (proving the
// cap takes precedence), while an unlimited (0) autopilot never skips for
// concurrency regardless of how many runs are active. Skips without a DB, per
// the repo's integration-test convention.
func TestShouldSkipDispatchConcurrencyGate(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, creatorID, agentID, _ := seedAttributionFixture(t, pool)
	svc := &AutopilotService{Queries: q}

	seedAP := func(t *testing.T, maxConcurrent int) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO autopilot (workspace_id, title, assignee_type, assignee_id, status, execution_mode, created_by_type, created_by_id, max_concurrent_runs)
			VALUES ($1, 'concurrency ap', 'agent', $2, 'active', 'run_only', 'member', $3, $4) RETURNING id`,
			workspaceID, agentID, creatorID, maxConcurrent).Scan(&id); err != nil {
			t.Fatalf("seed autopilot: %v", err)
		}
		// No FK cascades in this schema, so drop the run rows explicitly —
		// leftover active runs would pollute cross-test global/per-autopilot
		// counts on the shared test DB.
		t.Cleanup(func() {
			pool.Exec(context.Background(), `DELETE FROM autopilot_run WHERE autopilot_id = $1`, id)
			pool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, id)
		})
		return id
	}
	seedRun := func(t *testing.T, autopilotID, status string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO autopilot_run (autopilot_id, source, status) VALUES ($1, 'manual', $2)`,
			autopilotID, status); err != nil {
			t.Fatalf("seed autopilot run: %v", err)
		}
	}

	// Capped at 1 with one active (running) run → gate fires.
	cappedID := seedAP(t, 1)
	seedRun(t, cappedID, "running")
	capped, err := q.GetAutopilot(ctx, util.MustParseUUID(cappedID))
	if err != nil {
		t.Fatalf("get capped autopilot: %v", err)
	}
	reason, code, skip := svc.shouldSkipDispatch(ctx, capped, pgtype.UUID{})
	if !skip {
		t.Fatalf("dispatch should be skipped when active runs meet the cap")
	}
	if code != dispatch.ReasonConcurrencyLimit {
		t.Errorf("skip reason_code = %q, want %q", code, dispatch.ReasonConcurrencyLimit)
	}
	if !strings.Contains(reason, "max concurrent runs reached (1/1)") {
		t.Errorf("skip reason = %q, want it to contain the (active/limit) count", reason)
	}

	// A completed run does not hold a slot, so the same cap admits again — the
	// gate must not fire on it (it falls through to the later gates instead).
	freeID := seedAP(t, 1)
	seedRun(t, freeID, "completed")
	free, err := q.GetAutopilot(ctx, util.MustParseUUID(freeID))
	if err != nil {
		t.Fatalf("get free autopilot: %v", err)
	}
	_, code, _ = svc.shouldSkipDispatch(ctx, free, pgtype.UUID{})
	if code == dispatch.ReasonConcurrencyLimit {
		t.Errorf("a completed run must not count toward the cap, but the concurrency gate fired")
	}

	// Unlimited (0) never skips for concurrency however many runs are active.
	unlimitedID := seedAP(t, 0)
	seedRun(t, unlimitedID, "running")
	seedRun(t, unlimitedID, "issue_created")
	unlimited, err := q.GetAutopilot(ctx, util.MustParseUUID(unlimitedID))
	if err != nil {
		t.Fatalf("get unlimited autopilot: %v", err)
	}
	_, code, _ = svc.shouldSkipDispatch(ctx, unlimited, pgtype.UUID{})
	if code == dispatch.ReasonConcurrencyLimit {
		t.Errorf("unlimited (max_concurrent_runs=0) autopilot must never skip for concurrency")
	}
}
