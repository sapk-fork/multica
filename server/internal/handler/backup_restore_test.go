package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/backup"
)

const (
	backupRestoreTestEmail         = "backup-restore-test@multica.ai"
	backupRestoreTestWorkspaceSlug = "backup-restore-tests"
)

func backupRestoreSetup(t *testing.T) (userID, workspaceID, runtimeID string) {
	t.Helper()

	ctx := context.Background()

	// Defensive cleanup: if a prior run left rows behind, clear them.
	testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, backupRestoreTestWorkspaceSlug)
	testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, backupRestoreTestEmail)

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Backup Restore Test", backupRestoreTestEmail).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE email = $1`, backupRestoreTestEmail)
	})

	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "Backup Restore Tests", backupRestoreTestWorkspaceSlug, "Workspace for restore tests", "BKP").Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE slug = $1`, backupRestoreTestWorkspaceSlug)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, now()) RETURNING id
	`, workspaceID, "Restore Test Runtime", "restore_test_runtime", "Restore test runtime").Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	return userID, workspaceID, runtimeID
}

type backupRestoreRequest struct {
	Backup           json.RawMessage `json:"backup"`
	WorkspaceID      string          `json:"workspace_id"`
	Overwrite        bool            `json:"overwrite"`
	SelectedItems    []string        `json:"selected_items,omitempty"`
	SelectedIDs      []string        `json:"selected_ids,omitempty"`
	IncludeWorkspace bool            `json:"include_workspace"`
}

type backupRestoreResponse struct {
	Items     []backupRestoreItem     `json:"items"`
	Sections  map[string]int          `json:"section_summary"`
	Errors    []string                `json:"errors,omitempty"`
	Workspace *backupRestoreWorkspace `json:"workspace,omitempty"`
}

type backupRestoreWorkspace struct {
	Applied bool                           `json:"applied"`
	Skipped bool                           `json:"skipped"`
	Reason  string                         `json:"reason,omitempty"`
	Changes []backupRestoreWorkspaceChange `json:"changes,omitempty"`
}

type backupRestoreWorkspaceChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type backupRestoreItem struct {
	Type        string                 `json:"type"`
	SourceID    string                 `json:"source_id"`
	Identifier  string                 `json:"identifier"`
	Action      string                 `json:"action"`
	Reason      string                 `json:"reason,omitempty"`
	TargetID    string                 `json:"target_id,omitempty"`
	MissingDeps []backupRestoreMissing `json:"missing_deps,omitempty"`
}

type backupRestoreMissing struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
	Reason     string `json:"reason"`
}

func callRestorePreview(t *testing.T, userID, workspaceID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/backup/restore/preview", body)
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.RestoreBackupPreview(w, req)
	return w
}

func callRestoreExecute(t *testing.T, userID, workspaceID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/backup/restore", body)
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.RestoreBackup(w, req)
	return w
}

func decodeRestoreResponse(t *testing.T, w *httptest.ResponseRecorder) backupRestoreResponse {
	t.Helper()
	var resp backupRestoreResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %q)", err, w.Body.String())
	}
	return resp
}

func buildMinimalBackup() *backup.BackupFile {
	return &backup.BackupFile{
		Metadata: backup.BackupMetadata{
			Version:    backup.FormatVersion,
			ExportedAt: time.Now().UTC(),
		},
		Skills: []backup.BackupSkill{
			{ID: "skill-1", Name: "rust-async", Content: "# Async Rust", Description: "Concurrency in Rust"},
		},
		Labels: []backup.BackupLabel{
			{ID: "label-1", Name: "bug", Color: "#ff0000"},
			{ID: "label-2", Name: "feature", Color: "#00ff00"},
		},
		Agents: []backup.BackupAgent{
			{
				ID:           "agent-1",
				Name:         "Code Reviewer",
				Description:  "Reviews PRs",
				Instructions: "Be a careful reviewer",
				RuntimeMode:  "cloud",
				Visibility:   "workspace",
				SkillIDs:     []string{"skill-1"},
			},
		},
		Projects: []backup.BackupProject{
			{ID: "project-1", Title: "Migration", Description: "Q3 work", Icon: "rocket", Status: "active", Priority: "high"},
		},
		Issues: []backup.BackupIssue{
			{
				ID:          "issue-1",
				Number:      42,
				Title:       "First issue",
				Description: "Do the thing",
				Status:      "todo",
				Priority:    "high",
				ProjectID:   "project-1",
				LabelIDs:    []string{"label-1"},
			},
		},
		Squads: []backup.BackupSquad{
			{
				ID:          "squad-1",
				Name:        "Backend Squad",
				Description: "Backend team",
				Leader:      backup.BackupActor{Type: "agent", ID: "agent-1"},
				Members: []backup.BackupSquadMember{
					{MemberType: "agent", MemberID: "agent-1", Role: "leader"},
				},
			},
		},
		Autopilots: []backup.BackupAutopilot{
			{
				ID:            "ap-1",
				Name:          "Code Watcher",
				Enabled:       true,
				Assignee:      backup.BackupActor{Type: "agent", ID: "agent-1"},
				Status:        "active",
				ExecutionMode: "create_issue",
				ProjectID:     "project-1",
			},
		},
	}
}

func marshalBackup(t *testing.T, b *backup.BackupFile) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal backup: %v", err)
	}
	return data
}

func TestRestorePreview_NonMutating(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, buildMinimalBackup()),
		WorkspaceID: workspaceID,
	}

	w := callRestorePreview(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("preview returned %d: %s", w.Code, w.Body.String())
	}

	for _, table := range []string{"skill", "issue_label", "agent", "project", "issue", "squad", "autopilot"} {
		var n int
		if err := testPool.QueryRow(context.Background(),
			"SELECT count(*) FROM "+table+" WHERE workspace_id = $1", workspaceID,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("preview leaked a %s row (n=%d)", table, n)
		}
	}
}

func TestRestoreExecute_HappyPath(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, buildMinimalBackup()),
		WorkspaceID: workspaceID,
	}

	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute returned %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", resp.Errors)
	}

	wantCreated := map[string]int{"skill": 1, "label": 2, "agent": 1, "project": 1, "issue": 1, "squad": 1, "autopilot": 1}
	gotCreated := map[string]int{}
	for _, it := range resp.Items {
		if it.Action == "create" {
			gotCreated[it.Type]++
		}
	}
	for k, v := range wantCreated {
		if gotCreated[k] != v {
			t.Errorf("created %s: want %d, got %d (full: %+v)", k, v, gotCreated[k], gotCreated)
		}
	}
}

func TestRestoreExecute_SkillFiles(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	b := buildMinimalBackup()
	b.Skills[0].Files = []backup.BackupSkillFile{
		{Path: "examples/basic.rs", Content: "fn main() {}"},
		{Path: "references/cheatsheet.md", Content: "# Cheatsheet"},
	}
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, b),
		WorkspaceID: workspaceID,
	}

	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute returned %d: %s", w.Code, w.Body.String())
	}

	var skillID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM skill WHERE workspace_id = $1 AND name = $2`,
		workspaceID, "rust-async",
	).Scan(&skillID); err != nil {
		t.Fatalf("skill not restored: %v", err)
	}

	files, err := testHandler.Queries.ListSkillFiles(context.Background(), parseUUID(skillID))
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	wantPaths := map[string]bool{"examples/basic.rs": true, "references/cheatsheet.md": true}
	for _, f := range files {
		if !wantPaths[f.Path] {
			t.Errorf("unexpected file path: %q", f.Path)
		}
	}
}

func TestRestoreExecute_AutopilotRegeneratedWithoutToken(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, buildMinimalBackup()),
		WorkspaceID: workspaceID,
	}

	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute returned %d: %s", w.Code, w.Body.String())
	}

	var autopilotID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM autopilot WHERE workspace_id = $1 AND title = $2`,
		workspaceID, "Code Watcher",
	).Scan(&autopilotID); err != nil {
		t.Fatalf("autopilot not restored: %v", err)
	}
	if autopilotID == "" {
		t.Fatalf("empty autopilot id")
	}
}

func TestRestoreExecute_ConflictSkipByDefault(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, buildMinimalBackup()),
		WorkspaceID: workspaceID,
	}

	if w := callRestoreExecute(t, userID, workspaceID, body); w.Code != http.StatusOK {
		t.Fatalf("first execute: %d %s", w.Code, w.Body.String())
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("second execute: %d %s", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)

	for _, it := range resp.Items {
		if it.Action != "skip" {
			t.Errorf("item %s/%s: want skip, got %s (reason=%q)", it.Type, it.Identifier, it.Action, it.Reason)
		}
	}
}

func TestRestoreExecute_OverwriteReplaces(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	first := backupRestoreRequest{
		Backup:      marshalBackup(t, buildMinimalBackup()),
		WorkspaceID: workspaceID,
	}
	if w := callRestoreExecute(t, userID, workspaceID, first); w.Code != http.StatusOK {
		t.Fatalf("first execute: %d %s", w.Code, w.Body.String())
	}

	// Second pass keeps the names (so the conflict detector finds
	// the existing rows) but flips non-name fields. This exercises
	// the "update" path for every section, including label (which
	// matches by name case-insensitively).
	b := buildMinimalBackup()
	b.Skills[0].Description = "updated description"
	b.Agents[0].Description = "updated description"
	b.Agents[0].Instructions = "updated instructions"
	b.Labels[0].Color = "#123456"
	b.Projects[0].Description = "updated description"
	b.Issues[0].Description = "updated description"
	b.Squads[0].Description = "updated description"

	second := backupRestoreRequest{
		Backup:      marshalBackup(t, b),
		WorkspaceID: workspaceID,
		Overwrite:   true,
	}
	w := callRestoreExecute(t, userID, workspaceID, second)
	if w.Code != http.StatusOK {
		t.Fatalf("overwrite execute: %d %s", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)

	updateByType := map[string]int{}
	for _, it := range resp.Items {
		if it.Action == "update" {
			updateByType[it.Type]++
		}
	}
	for _, tp := range []string{"skill", "agent", "label", "project", "issue", "squad", "autopilot"} {
		if updateByType[tp] == 0 {
			t.Errorf("expected at least one %s update, got %d (full: %+v)", tp, updateByType[tp], updateByType)
		}
	}
}

func TestRestoreExecute_SelectedItemsFilter(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:        marshalBackup(t, buildMinimalBackup()),
		WorkspaceID:   workspaceID,
		SelectedItems: []string{"skills"},
	}

	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}

	for _, table := range []string{"issue_label", "agent", "project", "issue", "squad", "autopilot"} {
		var n int
		if err := testPool.QueryRow(context.Background(),
			"SELECT count(*) FROM "+table+" WHERE workspace_id = $1", workspaceID,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("section filter leaked into %s (n=%d)", table, n)
		}
	}

	var skills int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM skill WHERE workspace_id = $1`, workspaceID,
	).Scan(&skills); err != nil {
		t.Fatalf("count skill: %v", err)
	}
	if skills != 1 {
		t.Errorf("expected 1 skill, got %d", skills)
	}
}

func TestRestorePreview_MissingDeps(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:        marshalBackup(t, buildMinimalBackup()),
		WorkspaceID:   workspaceID,
		SelectedItems: []string{"agents"},
	}

	w := callRestorePreview(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)

	var agent *backupRestoreItem
	for i := range resp.Items {
		if resp.Items[i].Type == "agent" {
			agent = &resp.Items[i]
			break
		}
	}
	if agent == nil {
		t.Fatalf("no agent item in preview: %+v", resp.Items)
	}
	if len(agent.MissingDeps) == 0 {
		t.Fatalf("expected missing_deps on agent, got none")
	}
	foundSkillDep := false
	for _, dep := range agent.MissingDeps {
		if dep.Type == "skill" && dep.Identifier == "skill-1" {
			foundSkillDep = true
		}
	}
	if !foundSkillDep {
		t.Errorf("missing_deps did not include skill/skill-1: %+v", agent.MissingDeps)
	}
}

func TestRestoreExecute_IssueCrossSectionRefs(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, buildMinimalBackup()),
		WorkspaceID: workspaceID,
	}

	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}

	var projectID, issueID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM project WHERE workspace_id = $1 AND title = $2`, workspaceID, "Migration",
	).Scan(&projectID); err != nil {
		t.Fatalf("query project: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM issue WHERE workspace_id = $1 AND title = $2`, workspaceID, "First issue",
	).Scan(&issueID); err != nil {
		t.Fatalf("query issue: %v", err)
	}

	var projectOnIssue pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT project_id FROM issue WHERE id = $1`, issueID,
	).Scan(&projectOnIssue); err != nil {
		t.Fatalf("query issue.project_id: %v", err)
	}
	if !projectOnIssue.Valid || projectOnIssue.String() != projectID {
		t.Errorf("issue.project_id = %q (valid=%v), want %q", projectOnIssue.String(), projectOnIssue.Valid, projectID)
	}

	var labelAttached int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_to_label WHERE issue_id = $1`, issueID,
	).Scan(&labelAttached); err != nil {
		t.Fatalf("query issue_to_label: %v", err)
	}
	if labelAttached != 1 {
		t.Errorf("expected 1 issue_to_label row, got %d", labelAttached)
	}
}

func TestRestoreExecute_Comments(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	b := buildMinimalBackup()
	// Use the fixture's user email so the cross-instance
	// member-by-email remap resolves to the workspace owner.
	b.Members = []backup.BackupMember{{ID: "mem-1", Email: backupRestoreTestEmail, Name: "Backup Restore Test"}}
	b.Issues[0].Creator = backup.BackupActor{Type: "member", ID: "mem-1"}
	b.Issues[0].Comments = []backup.BackupComment{
		{ID: "comment-root", Author: backup.BackupActor{Type: "member", ID: "mem-1"}, Content: "Root comment"},
		{ID: "comment-reply", Author: backup.BackupActor{Type: "member", ID: "mem-1"}, Content: "Reply to root", ParentID: "comment-root"},
	}
	b.Issues[0].Comments[0].Reactions = []backup.BackupReaction{
		{Actor: backup.BackupActor{Type: "member", ID: "mem-1"}, Emoji: "👍"},
	}

	body := backupRestoreRequest{
		Backup:      marshalBackup(t, b),
		WorkspaceID: workspaceID,
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}

	var issueID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM issue WHERE workspace_id = $1 AND title = $2`, workspaceID, "First issue",
	).Scan(&issueID); err != nil {
		t.Fatalf("query issue: %v", err)
	}

	var commentCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1`, issueID,
	).Scan(&commentCount); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if commentCount != 2 {
		t.Errorf("expected 2 comments, got %d", commentCount)
	}

	var reactionCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment_reaction WHERE workspace_id = $1`, workspaceID,
	).Scan(&reactionCount); err != nil {
		t.Fatalf("count reactions: %v", err)
	}
	if reactionCount != 1 {
		t.Errorf("expected 1 reaction, got %d", reactionCount)
	}

	var rootID, replyID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM comment WHERE issue_id = $1 AND parent_id IS NULL LIMIT 1`, issueID,
	).Scan(&rootID); err != nil {
		t.Fatalf("query root: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM comment WHERE issue_id = $1 AND parent_id IS NOT NULL LIMIT 1`, issueID,
	).Scan(&replyID); err != nil {
		t.Fatalf("query reply: %v", err)
	}
	var parentID pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT parent_id FROM comment WHERE id = $1`, replyID,
	).Scan(&parentID); err != nil {
		t.Fatalf("query reply.parent_id: %v", err)
	}
	if !parentID.Valid || parentID.String() != rootID {
		t.Errorf("reply.parent_id = %q (valid=%v), want %q", parentID.String(), parentID.Valid, rootID)
	}
}

func TestRestoreExecute_WorkspaceOptIn(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)

	b := buildMinimalBackup()
	b.Workspace = &backup.BackupWorkspace{
		ID:           "ws-source",
		Name:         "Source Workspace",
		Slug:         "source-ws",
		Description:  "NEW DESCRIPTION FROM BACKUP",
		IssuePrefix:  "ZZZ",
		IssueCounter: int32Ptr(99),
	}
	backupData := marshalBackup(t, b)

	body := backupRestoreRequest{Backup: backupData, WorkspaceID: workspaceID}
	if w := callRestoreExecute(t, userID, workspaceID, body); w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	var desc string
	if err := testPool.QueryRow(context.Background(),
		`SELECT description FROM workspace WHERE id = $1`, workspaceID,
	).Scan(&desc); err != nil {
		t.Fatalf("read description: %v", err)
	}
	if desc == "NEW DESCRIPTION FROM BACKUP" {
		t.Errorf("workspace description was overwritten without opt-in")
	}

	body.IncludeWorkspace = true
	if w := callRestoreExecute(t, userID, workspaceID, body); w.Code != http.StatusOK {
		t.Fatalf("opt-in execute: %d %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT description FROM workspace WHERE id = $1`, workspaceID,
	).Scan(&desc); err != nil {
		t.Fatalf("read description: %v", err)
	}
	if desc != "NEW DESCRIPTION FROM BACKUP" {
		t.Errorf("workspace description not applied with opt-in: %q", desc)
	}
}

func TestRestoreExecute_UnsupportedVersion(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	b := buildMinimalBackup()
	b.Metadata.Version = "2.0"
	body := backupRestoreRequest{
		Backup:      marshalBackup(t, b),
		WorkspaceID: workspaceID,
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestRestoreExecute_IssueMissingProjectDep(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	// Build a backup whose issue references a project that is
	// NOT in the workspace and NOT in the selected items. The
	// planner should report the missing project dep and refuse
	// to write the issue. The agent section can still succeed
	// (no skill deps in this slim backup).
	b := buildMinimalBackup()
	b.Skills = nil
	b.Projects = nil
	b.Labels = nil
	b.Issues[0].ProjectID = "project-missing"
	body := backupRestoreRequest{
		Backup:        marshalBackup(t, b),
		WorkspaceID:   workspaceID,
		SelectedItems: []string{"agents", "issues"},
		Overwrite:     false,
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with partial failure surfaced, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)
	var issueItem *backupRestoreItem
	for i := range resp.Items {
		if resp.Items[i].Type == "issue" {
			issueItem = &resp.Items[i]
			break
		}
	}
	if issueItem == nil {
		t.Fatalf("no issue item in response: %+v", resp.Items)
	}
	if issueItem.Action != "skip" && issueItem.Action != "error" {
		t.Errorf("expected issue skip or error, got %s", issueItem.Action)
	}
}

func TestRestoreExecute_MalformedBackup(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	body := backupRestoreRequest{
		Backup:      json.RawMessage(`{"metadata":{}}`),
		WorkspaceID: workspaceID,
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestRestoreExecute_PartialRestoreResolvesMemberLead covers the
// blocker the code review caught: when restoring ONLY projects
// (selected_items=["projects"]) and the project's lead is a member
// that exists in the target workspace, the project must end up
// with a non-null lead_id pointing at the workspace's owner. The
// member remap must run before the projects section so the lead
// ref is resolvable.
func TestRestoreExecute_PartialRestoreResolvesMemberLead(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	b := buildMinimalBackup()
	// Strip everything except the project, and give the project a
	// member lead that matches the fixture's user.
	b.Skills = nil
	b.Labels = nil
	b.Agents = nil
	b.Issues = nil
	b.Squads = nil
	b.Autopilots = nil
	b.Projects[0].Lead = backup.BackupActor{Type: "member", ID: "mem-1"}
	b.Members = []backup.BackupMember{{
		ID:    "mem-1",
		Email: backupRestoreTestEmail,
		Name:  "Backup Restore Test",
	}}

	body := backupRestoreRequest{
		Backup:        marshalBackup(t, b),
		WorkspaceID:   workspaceID,
		SelectedItems: []string{"projects"},
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", resp.Errors)
	}

	var projectID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM project WHERE workspace_id = $1 AND title = $2`,
		workspaceID, "Migration",
	).Scan(&projectID); err != nil {
		t.Fatalf("project not restored: %v", err)
	}
	var leadType pgtype.Text
	var leadID pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT lead_type, lead_id FROM project WHERE id = $1`, projectID,
	).Scan(&leadType, &leadID); err != nil {
		t.Fatalf("read project lead: %v", err)
	}
	if !leadType.Valid || leadType.String != "member" {
		t.Errorf("project.lead_type = %q (valid=%v), want %q", leadType.String, leadType.Valid, "member")
	}
	if !leadID.Valid {
		t.Fatalf("project.lead_id is NULL; member remap did not run before the projects section")
	}
	// lead_id should point at the workspace owner user. We
	// verify by checking the resolved user is a member of the
	// workspace.
	var memberExists int
	if err := testPool.QueryRow(context.Background(),
		`SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, leadID,
	).Scan(&memberExists); err != nil {
		t.Errorf("project.lead_id = %q does not point at any workspace member: %v", leadID.String(), err)
	}
	_ = userID
}

// TestRestoreExecute_WorkspaceChangesDiff: with the workspace
// opt-in, the response must list which fields the restore
// overwrote, including IssuePrefix.
func TestRestoreExecute_WorkspaceChangesDiff(t *testing.T) {
	userID, workspaceID, _ := backupRestoreSetup(t)
	b := buildMinimalBackup()
	b.Workspace = &backup.BackupWorkspace{
		ID:          "ws-source",
		Name:        "Source Workspace",
		Slug:        "source-ws",
		Description: "NEW DESCRIPTION",
		Context:     "NEW CONTEXT",
		IssuePrefix: "ZZZ",
		AvatarURL:   "https://example.com/avatar.png",
	}
	body := backupRestoreRequest{
		Backup:           marshalBackup(t, b),
		WorkspaceID:      workspaceID,
		IncludeWorkspace: true,
	}
	w := callRestoreExecute(t, userID, workspaceID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	resp := decodeRestoreResponse(t, w)
	if resp.Workspace == nil || !resp.Workspace.Applied {
		t.Fatalf("expected workspace.applied=true, got %+v", resp.Workspace)
	}
	wantFields := map[string]bool{"description": true, "context": true, "issue_prefix": true, "avatar_url": true}
	gotFields := map[string]bool{}
	for _, c := range resp.Workspace.Changes {
		gotFields[c.Field] = true
		if c.Before == c.After {
			t.Errorf("change %q has equal before/after: %q", c.Field, c.Before)
		}
	}
	for k := range wantFields {
		if !gotFields[k] {
			t.Errorf("workspace.changes missing field %q (got: %+v)", k, gotFields)
		}
	}
	_ = userID
}

func int32Ptr(v int32) *int32 { return &v }
