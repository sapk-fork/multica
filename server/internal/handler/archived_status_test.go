package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestArchivedIssueSkippedByChildDoneNotification verifies that an archived
// parent does not receive a child-done system comment — same guarantee as the
// existing done/cancelled tests, extended to the new terminal status.
func TestArchivedIssueSkippedByChildDoneNotification(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}

	fx := newChildDoneFixture(t, "archived")

	updateChildStatus(t, fx.child.ID, "done")

	if got := countSystemCommentsOn(t, fx.parent.ID); got != 0 {
		t.Errorf("parent at 'archived' should not receive child-done notification, got %d comments", got)
	}
}

// TestSearchExcludesArchivedWhenIncludeClosedFalse verifies the search handler
// hides archived issues from default results but surfaces them when
// include_closed=true.
func TestSearchExcludesArchivedWhenIncludeClosedFalse(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()

	uniqueTitle := "archived-search-sentinel-" + time.Now().Format(time.RFC3339Nano)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  uniqueTitle,
		"status": "archived",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	// Default search (include_closed=false) should NOT return the archived issue.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/issues/search?workspace_id="+testWorkspaceID+"&q="+uniqueTitle, nil)
	testHandler.SearchIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SearchIssues (default): expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var closed struct {
		Issues []SearchIssueResponse `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&closed); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	for _, issue := range closed.Issues {
		if issue.IssueResponse.ID == created.ID {
			t.Errorf("archived issue should be excluded from search when include_closed=false")
		}
	}

	// Search with include_closed=true SHOULD return the archived issue.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/issues/search?workspace_id="+testWorkspaceID+"&q="+uniqueTitle+"&include_closed=true", nil)
	testHandler.SearchIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SearchIssues (include_closed): expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var open struct {
		Issues []SearchIssueResponse `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&open); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	var found bool
	for _, issue := range open.Issues {
		if issue.IssueResponse.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("archived issue should appear in search when include_closed=true")
	}
}

// TestArchivingCancelsActiveTasks verifies that updating an issue to
// "archived" cancels any active tasks linked to it — same behaviour as
// setting the status to "cancelled".
func TestArchivingCancelsActiveTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()

	// Create a dedicated test agent so we don't collide with the
	// (issue_id, agent_id) unique index on agent_task_queue that other
	// fixture-driven tests rely on.
	agentID := createHandlerTestAgent(t, "ArchiveCancelsTasksAgent", []byte("[]"))
	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load runtime: %v", err)
	}

	// Create a fresh in_progress issue for the agent. The handler auto-enqueues
	// a task when an agent-assigned issue enters in_progress, so the test
	// reuses that task (and updates it to 'queued' to make the assertion's
	// "before" state explicit).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "archiving-cancels-tasks-" + time.Now().Format(time.RFC3339Nano),
		"status":        "in_progress",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issue.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issue.ID)
	})

	// Find the auto-enqueued task for this issue+agent.
	var taskID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 LIMIT 1`,
		issue.ID, agentID,
	).Scan(&taskID); err != nil {
		t.Fatalf("find auto-enqueued task: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'queued' WHERE id = $1`, taskID,
	); err != nil {
		t.Fatalf("mark task queued: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	_ = runtimeID // loaded above for parity; the auto-enqueued task already uses the agent's runtime.

	// Archive the issue via the handler.
	w = httptest.NewRecorder()
	reqUpdate := newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{"status": "archived"})
	reqUpdate = withURLParam(reqUpdate, "id", issue.ID)
	testHandler.UpdateIssue(w, reqUpdate)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue (archived): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The task should now be cancelled.
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Errorf("task should be 'cancelled' after issue archived, got %q", got)
	}
}

// TestWebhook_MergedPR_PreservesArchived verifies that a merged PR's
// close-intent does NOT advance an already-archived issue back to "done" —
// the same guarantee as the existing cancelled test.
func TestWebhook_MergedPR_PreservesArchived(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	secret := "archived-pr-secret"
	t.Setenv("GITHUB_WEBHOOK_SECRET", secret)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Already archived",
		"status": "archived",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue_pull_request WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM github_pull_request WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM github_installation WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	const installationID int64 = 77889900
	if _, err := testHandler.Queries.CreateGitHubInstallation(ctx, db.CreateGitHubInstallationParams{
		WorkspaceID:    parseUUID(testWorkspaceID),
		InstallationID: installationID,
		AccountLogin:   "archived-acct",
		AccountType:    "User",
	}); err != nil {
		t.Fatalf("CreateGitHubInstallation: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"action": "closed",
		"pull_request": map[string]any{
			"number": 99, "html_url": "https://x", "title": "Closes " + created.Identifier,
			"state": "closed", "merged": true, "draft": false,
			"merged_at": "2026-06-01T00:00:00Z", "closed_at": "2026-06-01T00:00:00Z",
			"created_at": "2026-05-31T00:00:00Z", "updated_at": "2026-06-01T00:00:00Z",
			"head": map[string]any{"ref": "x"}, "user": map[string]any{"login": "u"},
		},
		"repository":   map[string]any{"name": "r", "owner": map[string]any{"login": "o"}},
		"installation": map[string]any{"id": installationID},
	})
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	w = httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/webhooks/github", bytes.NewReader(body))
	req2.Header.Set("X-GitHub-Event", "pull_request")
	req2.Header.Set("X-Hub-Signature-256", sig)
	testHandler.HandleGitHubWebhook(w, req2)

	updated, err := testHandler.Queries.GetIssue(ctx, parseUUID(created.ID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.Status != "archived" {
		t.Errorf("expected status to remain 'archived' after merged PR, got %q", updated.Status)
	}
}

// TestArchivedStatusSortRankIsNextToCancelled verifies the search SQL builder
// assigns "archived" the same sort tier as "cancelled" so archived issues don't
// incorrectly sort above active ones in search results.
func TestArchivedStatusSortRankIsNextToCancelled(t *testing.T) {
	query, _ := buildSearchQuery("test", []string{"test"}, 0, false, true)

	// Both "cancelled" and "archived" must appear in the CASE and share a THEN value.
	if !strings.Contains(query, "WHEN 'archived'") {
		t.Error("status-rank CASE should include a WHEN 'archived' arm")
	}
	// They must sort at the same rank (both should appear with THEN 6 in the default config).
	cancelledIdx := strings.Index(query, "'cancelled' THEN 6")
	archivedIdx := strings.Index(query, "'archived' THEN 6")
	if cancelledIdx < 0 || archivedIdx < 0 {
		t.Errorf("both 'cancelled' and 'archived' should map to rank 6 in the status CASE (cancelled=%d, archived=%d)", cancelledIdx, archivedIdx)
	}
}
