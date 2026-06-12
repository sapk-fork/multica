package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/backup"
)

// backupRestoreTestPrefix is namespaced onto every entity the tests create
// so a parallel test run (or a previous failed run that left rows behind)
// cannot collide on the conflict-detection key. The trailing dash is
// included so "tf-skill-1" and "tf-skill-10" sort sensibly.
const backupRestoreTestPrefix = "tf-bkp-"

// newBackupForTest builds a backup.BackupFile with one of every section
// the restore understands, all namespaced under the supplied prefix so
// the test fixture stays isolated from the rest of the test suite. The
// optional mutate hook lets individual tests tweak the payload before
// marshalling (e.g. to inject a conflict or drop a section).
func newBackupForTest(t *testing.T, prefix string, mutate func(*backup.BackupFile)) []byte {
	t.Helper()
	now := time.Now().UTC()
	bf := backup.New()
	bf.Skills = []backup.BackupSkill{
		{ID: "11111111-1111-1111-1111-111111111111", Name: prefix + "skill-1", Description: "tf skill", Content: "# tf skill\n", CreatedAt: now},
	}
	bf.Labels = []backup.BackupLabel{
		{ID: "22222222-2222-2222-2222-222222222222", Name: prefix + "label-1", Color: "#10b981", CreatedAt: now},
	}
	bf.Agents = []backup.BackupAgent{
		{
			ID:           "33333333-3333-3333-3333-333333333333",
			Name:         prefix + "agent-1",
			Description:  "tf agent",
			RuntimeMode:  "cloud",
			SkillIDs:     []string{"11111111-1111-1111-1111-111111111111"},
			Instructions: "do the thing",
			CreatedAt:    now,
		},
	}
	bf.Projects = []backup.BackupProject{
		{ID: "44444444-4444-4444-4444-444444444444", Title: prefix + "project-1", CreatedAt: now, Status: "planned"},
	}
	bf.Issues = []backup.BackupIssue{
		{
			ID:        "55555555-5555-5555-5555-555555555555",
			Number:    4242,
			Title:     prefix + "issue-1",
			Status:    "todo",
			Priority:  "medium",
			ProjectID: "44444444-4444-4444-4444-444444444444",
			LabelIDs:  []string{"22222222-2222-2222-2222-222222222222"},
			CreatedAt: now,
		},
	}
	bf.Squads = []backup.BackupSquad{
		{
			ID:        "66666666-6666-6666-6666-666666666666",
			Name:      prefix + "squad-1",
			CreatedAt: now,
			Leader:    &backup.BackupActor{Type: "agent", ID: "33333333-3333-3333-3333-333333333333"},
			Members: []backup.BackupSquadMember{
				{MemberType: "agent", MemberID: "33333333-3333-3333-3333-333333333333", Role: "leader"},
			},
		},
	}
	bf.Autopilots = []backup.BackupAutopilot{
		{
			ID:            "77777777-7777-7777-7777-777777777777",
			Name:          prefix + "autopilot-1",
			Assignee:      &backup.BackupActor{Type: "agent", ID: "33333333-3333-3333-3333-333333333333"},
			ProjectID:     "44444444-4444-4444-4444-444444444444",
			ExecutionMode: "create_issue",
			CreatedAt:     now,
			Triggers: []backup.BackupAutopilotTrigger{
				{
					Kind:     "schedule",
					Enabled:  true,
					Cron:     "0 * * * *",
					Timezone: "UTC",
				},
			},
		},
	}
	if mutate != nil {
		mutate(bf)
	}
	data, err := backup.Marshal(bf)
	if err != nil {
		t.Fatalf("backup.Marshal: %v", err)
	}
	return data
}

// cleanupBackupRestoreRows removes every row the test created under the
// supplied prefix. Called from t.Cleanup so a panic in the middle of a
// test does not leave orphans. The deletes are FK-tolerant: child rows
// (agent_skill, issue_to_label, comment, autopilot_trigger, etc.) go
// first, then the parents.
func cleanupBackupRestoreRows(t *testing.T, prefix string) {
	t.Helper()
	ctx := context.Background()
	statements := []struct {
		table string
		where string
	}{
		{"autopilot_trigger", "autopilot_id IN (SELECT id FROM autopilot WHERE title LIKE $1)"},
		{"autopilot", "title LIKE $1"},
		{"squad_member", "squad_id IN (SELECT id FROM squad WHERE name LIKE $1)"},
		{"squad", "name LIKE $1"},
		{"issue_to_label", "issue_id IN (SELECT id FROM issue WHERE title LIKE $1)"},
		{"comment", "issue_id IN (SELECT id FROM issue WHERE title LIKE $1)"},
		{"issue", "title LIKE $1"},
		{"project_resource", "project_id IN (SELECT id FROM project WHERE title LIKE $1)"},
		{"project", "title LIKE $1"},
		{"agent_skill", "agent_id IN (SELECT id FROM agent WHERE name LIKE $1)"},
		{"agent", "name LIKE $1"},
		{"issue_label", "name LIKE $1"},
		{"skill_file", "skill_id IN (SELECT id FROM skill WHERE name LIKE $1)"},
		{"skill", "name LIKE $1"},
	}
	pattern := prefix + "%"
	for _, s := range statements {
		if _, err := testPool.Exec(ctx, "DELETE FROM "+s.table+" WHERE "+s.where, pattern); err != nil {
			t.Logf("cleanup %s: %v", s.table, err)
		}
	}
}

// TestBackupRestore_PreviewDoesNotWrite verifies the preview path is
// strictly read-only: it returns a plan with all "create" actions but
// no row is inserted. The execute path is exercised separately.
func TestBackupRestore_PreviewDoesNotWrite(t *testing.T) {
	prefix := backupRestoreTestPrefix + "preview-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	data := newBackupForTest(t, prefix, nil)

	body, _ := json.Marshal(BackupRestoreBody{Backup: string(data), Options: BackupRestoreOptions{}})
	req := newRequest("POST", "/api/backup/restore/preview", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestorePreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp BackupRestorePreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Every section should report "new" (no conflict) on a fresh
	// workspace.
	for kind, items := range resp.Plan.Sections {
		for _, it := range items {
			if it.Identifier == "" {
				continue
			}
			if it.Status != "new" {
				t.Errorf("section %s item %q: expected status=new, got %q", kind, it.Identifier, it.Status)
			}
		}
	}

	// No rows should exist on disk.
	assertNoRowsByName(t, "skill", prefix+"skill-1")
	assertNoRowsByName(t, "agent", prefix+"agent-1")
	assertNoRowsByName(t, "issue_label", prefix+"label-1")
}

// TestBackupRestore_DependencyOrder verifies the plan's dependency_order
// is exactly the canonical restore ordering and that the sections map
// is keyed in the same order. This is the contract the UI relies on
// when rendering the restore progress bar.
func TestBackupRestore_DependencyOrder(t *testing.T) {
	prefix := backupRestoreTestPrefix + "order-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	data := newBackupForTest(t, prefix, nil)
	body, _ := json.Marshal(BackupRestoreBody{Backup: string(data), Options: BackupRestoreOptions{}})
	req := newRequest("POST", "/api/backup/restore/preview", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestorePreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var resp BackupRestorePreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []string{"skills", "labels", "agents", "projects", "issues", "squads", "autopilots"}
	if len(resp.Plan.DependencyOrder) != len(want) {
		t.Fatalf("dependency_order length: want %d, got %d", len(want), len(resp.Plan.DependencyOrder))
	}
	for i, k := range want {
		if resp.Plan.DependencyOrder[i] != k {
			t.Errorf("dependency_order[%d]: want %q, got %q", i, k, resp.Plan.DependencyOrder[i])
		}
	}
	// Sections map must include every kind in dependency_order.
	for _, k := range want {
		if _, ok := resp.Plan.Sections[k]; !ok {
			t.Errorf("missing section %q in plan", k)
		}
	}
}

// TestBackupRestore_ExecuteWritesAndRemapsCrossSection is the smoke test
// for the execute path: every section creates the expected row, the
// cross-section references (issue.project_id → project.ID,
// autopilot.assignee_id → agent.ID) resolve to the new target IDs, and
// the summary counters add up.
func TestBackupRestore_ExecuteWritesAndRemapsCrossSection(t *testing.T) {
	prefix := backupRestoreTestPrefix + "exec-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	data := newBackupForTest(t, prefix, nil)
	body, _ := json.Marshal(BackupRestoreBody{Backup: string(data), Options: BackupRestoreOptions{}})
	req := newRequest("POST", "/api/backup/restore", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp BackupRestoreExecuteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// One item per section, all created.
	if got := len(resp.Plan.Sections["skills"]); got != 1 {
		t.Errorf("skills: want 1, got %d", got)
	}
	for _, k := range []string{"labels", "agents", "projects", "squads", "autopilots"} {
		if got := len(resp.Plan.Sections[k]); got != 1 {
			t.Errorf("%s: want 1, got %d", k, got)
		}
	}
	if got := len(resp.Plan.Sections["issues"]); got != 1 {
		t.Errorf("issues: want 1, got %d", got)
	}
	if resp.Plan.Summary.Created != 7 {
		t.Errorf("summary.created: want 7, got %d", resp.Plan.Summary.Created)
	}
	if resp.Plan.Summary.Overwritten != 0 || resp.Plan.Summary.Skipped != 0 || resp.Plan.Summary.Errored != 0 {
		t.Errorf("expected no other summary counters, got %+v", resp.Plan.Summary)
	}

	// Verify each row exists.
	assertHasRowByName(t, "skill", prefix+"skill-1")
	assertHasRowByName(t, "agent", prefix+"agent-1")
	assertHasRowByName(t, "issue_label", prefix+"label-1")
	assertHasRowByTitle(t, "issue", prefix+"issue-1")
	assertHasRowByTitle(t, "project", prefix+"project-1")
	assertHasRowByName(t, "squad", prefix+"squad-1")
	assertHasRowByTitle(t, "autopilot", prefix+"autopilot-1")

	// Verify the cross-section remap took effect. The new issue's
	// project_id must point at the new project (not the source
	// "44444444-...").
	var newProjectID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM project WHERE title = $1`, prefix+"project-1",
	).Scan(&newProjectID); err != nil {
		t.Fatalf("load new project id: %v", err)
	}
	var issueProjectID pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT project_id FROM issue WHERE title = $1`, prefix+"issue-1",
	).Scan(&issueProjectID); err != nil {
		t.Fatalf("load new issue project: %v", err)
	}
	if uuidToString(issueProjectID) != newProjectID {
		t.Errorf("issue.project_id remap: want %s, got %s", newProjectID, uuidToString(issueProjectID))
	}

	// Verify autopilot's assignee_id remaps to the new agent.
	var newAgentID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent WHERE name = $1`, prefix+"agent-1",
	).Scan(&newAgentID); err != nil {
		t.Fatalf("load new agent id: %v", err)
	}
	var autopilotAssigneeID pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT assignee_id FROM autopilot WHERE title = $1`, prefix+"autopilot-1",
	).Scan(&autopilotAssigneeID); err != nil {
		t.Fatalf("load new autopilot assignee: %v", err)
	}
	if uuidToString(autopilotAssigneeID) != newAgentID {
		t.Errorf("autopilot.assignee_id remap: want %s, got %s", newAgentID, uuidToString(autopilotAssigneeID))
	}

	// Verify autopilot's schedule produced a trigger row.
	var triggerCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM autopilot_trigger WHERE autopilot_id = (SELECT id FROM autopilot WHERE title = $1)`,
		prefix+"autopilot-1",
	).Scan(&triggerCount); err != nil {
		t.Fatalf("count autopilot_trigger: %v", err)
	}
	if triggerCount != 1 {
		t.Errorf("autopilot_trigger rows: want 1, got %d", triggerCount)
	}
}

// TestBackupRestore_ConflictSkipsByDefault seeds an existing entity in
// the target workspace and verifies the restore flags the backup's
// matching entity as a conflict (skipped, not overwritten).
func TestBackupRestore_ConflictSkipsByDefault(t *testing.T) {
	prefix := backupRestoreTestPrefix + "conflict-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	// Pre-seed a label with the same name the backup will try to
	// restore. The seed lives in the shared test workspace so the
	// planner sees it in loadExisting.
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO issue_label (workspace_id, name, color) VALUES ($1, $2, $3)`,
		parseUUID(testWorkspaceID), prefix+"label-1", "#ff00ff",
	); err != nil {
		t.Fatalf("seed label: %v", err)
	}

	data := newBackupForTest(t, prefix, nil)
	body, _ := json.Marshal(BackupRestoreBody{Backup: string(data), Options: BackupRestoreOptions{}})
	req := newRequest("POST", "/api/backup/restore", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	var resp BackupRestoreExecuteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	labels := resp.Plan.Sections["labels"]
	if len(labels) != 1 {
		t.Fatalf("expected 1 label item, got %d", len(labels))
	}
	if labels[0].Status != "skipped" {
		t.Errorf("label status: want skipped, got %q (reason=%q)", labels[0].Status, labels[0].Reason)
	}
	if resp.Plan.Summary.Skipped != 1 {
		t.Errorf("summary.skipped: want 1, got %d", resp.Plan.Summary.Skipped)
	}

	// The label colour on disk should still be the seed colour
	// (untouched, because the conflict was skipped).
	var color string
	if err := testPool.QueryRow(context.Background(),
		`SELECT color FROM issue_label WHERE workspace_id = $1 AND name = $2`,
		parseUUID(testWorkspaceID), prefix+"label-1",
	).Scan(&color); err != nil {
		t.Fatalf("read seed label: %v", err)
	}
	if color != "#ff00ff" {
		t.Errorf("seed label color mutated: want #ff00ff, got %q", color)
	}
}

// TestBackupRestore_OverwriteReplaces runs the same conflict as the
// previous test but with Overwrite=true. The label is updated to the
// backup's colour; the plan reports overwritten/skipped counts as
// expected.
func TestBackupRestore_OverwriteReplaces(t *testing.T) {
	prefix := backupRestoreTestPrefix + "overwrite-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO issue_label (workspace_id, name, color) VALUES ($1, $2, $3)`,
		parseUUID(testWorkspaceID), prefix+"label-1", "#ff00ff",
	); err != nil {
		t.Fatalf("seed label: %v", err)
	}

	data := newBackupForTest(t, prefix, nil)
	body, _ := json.Marshal(BackupRestoreBody{Backup: string(data), Options: BackupRestoreOptions{Overwrite: true}})
	req := newRequest("POST", "/api/backup/restore", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	var resp BackupRestoreExecuteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	labels := resp.Plan.Sections["labels"]
	if len(labels) != 1 || labels[0].Status != "overwritten" {
		t.Errorf("label status: want overwritten, got %+v", labels)
	}
	if resp.Plan.Summary.Overwritten != 1 {
		t.Errorf("summary.overwritten: want 1, got %d", resp.Plan.Summary.Overwritten)
	}

	// The label colour on disk should now be the backup's colour
	// ("#10b981"), because the overwrite updated it.
	var color string
	if err := testPool.QueryRow(context.Background(),
		`SELECT color FROM issue_label WHERE workspace_id = $1 AND name = $2`,
		parseUUID(testWorkspaceID), prefix+"label-1",
	).Scan(&color); err != nil {
		t.Fatalf("read overwritten label: %v", err)
	}
	if color != "#10b981" {
		t.Errorf("label color not overwritten: want #10b981, got %q", color)
	}
}

// TestBackupRestore_SelectedItemsFilter restores a subset of the
// backup. Only the listed source-UUIDs make it into the target; the
// rest are absent from the plan entirely (the planner skips them
// before they hit conflict detection).
func TestBackupRestore_SelectedItemsFilter(t *testing.T) {
	prefix := backupRestoreTestPrefix + "selected-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	data := newBackupForTest(t, prefix, nil)
	body, _ := json.Marshal(BackupRestoreBody{
		Backup:        string(data),
		Options:       BackupRestoreOptions{},
		SelectedItems: []string{"22222222-2222-2222-2222-222222222222"}, // labels only
	})
	req := newRequest("POST", "/api/backup/restore", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	var resp BackupRestoreExecuteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := len(resp.Plan.Sections["labels"]); got != 1 {
		t.Errorf("labels: want 1, got %d", got)
	}
	for _, k := range []string{"skills", "agents", "projects", "issues", "squads", "autopilots"} {
		if got := len(resp.Plan.Sections[k]); got != 0 {
			t.Errorf("selected-only %s: want 0, got %d", k, got)
		}
	}
	if resp.Plan.Summary.Created != 1 {
		t.Errorf("summary.created: want 1, got %d", resp.Plan.Summary.Created)
	}
}

// TestBackupRestore_RejectsBadBackupBytes sends a non-backup body and
// verifies the endpoint returns 400, not 500. A wrong-type error here
// is what would crash a CLI tool that mis-formats the upload.
func TestBackupRestore_RejectsBadBackupBytes(t *testing.T) {
	body, _ := json.Marshal(BackupRestoreBody{Backup: "this is not json", Options: BackupRestoreOptions{}})
	req := newRequest("POST", "/api/backup/restore/preview", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestorePreview(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid backup file") {
		t.Errorf("expected invalid-backup error, got %s", w.Body.String())
	}
}

// TestBackupRestore_RejectsMissingBackup verifies an empty payload is
// rejected with 400. The handler must not silently treat the empty
// string as a no-op.
func TestBackupRestore_RejectsMissingBackup(t *testing.T) {
	body, _ := json.Marshal(BackupRestoreBody{Backup: "", Options: BackupRestoreOptions{}})
	req := newRequest("POST", "/api/backup/restore/preview", json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.BackupRestorePreview(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestBackupRestore_PlanMatchesDependencyOrder walks the same backup
// twice and asserts the plans' dependency_order slices are byte-equal.
// The contract is that the order is a function of the section kind
// only, not the per-run input.
func TestBackupRestore_PlanMatchesDependencyOrder(t *testing.T) {
	prefix := backupRestoreTestPrefix + "order-twice-"
	t.Cleanup(func() { cleanupBackupRestoreRows(t, prefix) })

	data := newBackupForTest(t, prefix, nil)
	mk := func() []string {
		body, _ := json.Marshal(BackupRestoreBody{Backup: string(data), Options: BackupRestoreOptions{}})
		req := newRequest("POST", "/api/backup/restore/preview", json.RawMessage(body))
		w := httptest.NewRecorder()
		testHandler.BackupRestorePreview(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
		var resp BackupRestorePreviewResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.Plan.DependencyOrder
	}
	a := mk()
	b := mk()
	if fmt.Sprintf("%v", a) != fmt.Sprintf("%v", b) {
		t.Errorf("dependency_order not stable across runs:\n  first:  %v\n  second: %v", a, b)
	}
}

// assertNoRowsByName fails the test if any row in `table` matches the
// given name. Used to prove the preview path is read-only.
func assertNoRowsByName(t *testing.T, table, name string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM "+table+" WHERE name = $1", name,
	).Scan(&n); err != nil {
		t.Fatalf("count %s by name: %v", table, err)
	}
	if n != 0 {
		t.Errorf("expected no rows in %s named %q, found %d", table, name, n)
	}
}

// assertHasRowByName fails the test if no row in `table` matches the
// given name. Used to prove the execute path actually wrote.
func assertHasRowByName(t *testing.T, table, name string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM "+table+" WHERE name = $1", name,
	).Scan(&n); err != nil {
		t.Fatalf("count %s by name: %v", table, err)
	}
	if n == 0 {
		t.Errorf("expected at least 1 row in %s named %q, found 0", table, name)
	}
}

// assertHasRowByTitle is the title-column twin of assertHasRowByName. The
// issue and project tables use `title` as the human-readable key.
func assertHasRowByTitle(t *testing.T, table, title string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM "+table+" WHERE title = $1", title,
	).Scan(&n); err != nil {
		t.Fatalf("count %s by title: %v", table, err)
	}
	if n == 0 {
		t.Errorf("expected at least 1 row in %s titled %q, found 0", table, title)
	}
}
