package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/backup"
)

// decodeBackup runs BackupExport against the seeded test fixture and returns
// the parsed backup file. It fails the test on a non-200 status or a body that
// does not round-trip through backup.Unmarshal.
func decodeBackup(t *testing.T, query string) *backup.BackupFile {
	t.Helper()
	if testHandler == nil {
		t.Skip("no database configured")
	}
	path := "/api/backup/export"
	if query != "" {
		path += "?" + query
	}
	req := newRequest("POST", path, nil)
	rec := httptest.NewRecorder()
	testHandler.BackupExport(rec, req)
	if rec.Code != 200 {
		t.Fatalf("BackupExport status = %d, body = %s", rec.Code, rec.Body.String())
	}
	file, err := backup.Unmarshal(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("backup.Unmarshal: %v", err)
	}
	return file
}

func TestBackupExportDefaultIncludesWorkspaceAndOwner(t *testing.T) {
	file := decodeBackup(t, "")

	if file.Metadata.Version != backup.FormatVersion {
		t.Fatalf("version = %q, want %q", file.Metadata.Version, backup.FormatVersion)
	}
	if file.Workspace == nil {
		t.Fatal("expected workspace section to be populated")
	}
	if file.Workspace.Slug != handlerTestWorkspaceSlug {
		t.Fatalf("workspace slug = %q, want %q", file.Workspace.Slug, handlerTestWorkspaceSlug)
	}

	// The seeded agent is owned by the test user, so a default export (which
	// includes agents) must surface that user as a referenced member resolved
	// by email — the cross-instance identity key.
	var found bool
	for _, m := range file.Members {
		if m.Email == handlerTestEmail {
			found = true
			if m.Role != "owner" {
				t.Errorf("owner member role = %q, want owner", m.Role)
			}
		}
	}
	if !found {
		t.Errorf("expected referenced member with email %q in %d members", handlerTestEmail, len(file.Members))
	}

	if len(file.Agents) == 0 {
		t.Error("expected at least the seeded agent in the export")
	}
}

func TestBackupExportIncludeTypesSubset(t *testing.T) {
	file := decodeBackup(t, "include_types=labels")

	// Only the labels section was requested; entity sections that were not
	// selected must be absent regardless of what exists in the workspace.
	if len(file.Agents) != 0 {
		t.Errorf("agents should be empty when not requested, got %d", len(file.Agents))
	}
	if len(file.Issues) != 0 {
		t.Errorf("issues should be empty when not requested, got %d", len(file.Issues))
	}
	if file.Workspace == nil {
		t.Error("workspace envelope should always be present")
	}
}

func TestBackupExportRejectsUnknownIncludeType(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database configured")
	}
	req := newRequest("POST", "/api/backup/export?include_types=bogus", nil)
	rec := httptest.NewRecorder()
	testHandler.BackupExport(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400 for unknown include_types; body = %s", rec.Code, rec.Body.String())
	}
}

func TestParseIncludeTypes(t *testing.T) {
	all, err := parseIncludeTypes("")
	if err != nil {
		t.Fatalf("empty include_types: %v", err)
	}
	for _, typ := range backupEntityTypes {
		if !all.has(typ) {
			t.Errorf("empty include_types should select %q", typ)
		}
	}

	subset, err := parseIncludeTypes("issues, squads ,issues")
	if err != nil {
		t.Fatalf("subset include_types: %v", err)
	}
	if !subset.has("issues") || !subset.has("squads") {
		t.Error("expected issues and squads to be selected")
	}
	if subset.has("agents") {
		t.Error("agents must not be selected")
	}

	if _, err := parseIncludeTypes("nope"); err == nil {
		t.Error("expected error for unknown include type")
	}
}

// TestBackupExportForbiddenForNonAdmin verifies the owner/admin authz gate:
// a plain workspace member must receive 403.
func TestBackupExportForbiddenForNonAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database configured")
	}
	ctx := context.Background()

	var plainUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Backup Plain Member', 'backup-plain-member@multica.ai') RETURNING id
	`).Scan(&plainUserID); err != nil {
		t.Fatalf("create plain member user: %v", err)
	}
	t.Cleanup(func() {
		// Deleting the user cascades to the member row.
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, plainUserID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, plainUserID); err != nil {
		t.Fatalf("add plain member to workspace: %v", err)
	}

	req := newRequest("POST", "/api/backup/export", nil)
	req.Header.Set("X-User-ID", plainUserID)
	rec := httptest.NewRecorder()
	testHandler.BackupExport(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for plain member, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestBackupExportIncludesArchivedAgentsAndSquads verifies that archived agents
// and squads appear in the export even though regular list calls exclude them.
func TestBackupExportIncludesArchivedAgentsAndSquads(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database configured")
	}
	ctx := context.Background()

	var archivedAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id, archived_at
		)
		VALUES ($1, 'Archived Export Agent', '', 'cloud', '{}'::jsonb, $2, 'workspace', 1, $3, now())
		RETURNING id
	`, testWorkspaceID, testRuntimeID, testUserID).Scan(&archivedAgentID); err != nil {
		t.Fatalf("create archived agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, archivedAgentID)
	})

	// Use the pre-existing non-archived test agent as squad leader.
	var leaderID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 AND archived_at IS NULL ORDER BY created_at ASC LIMIT 1
	`, testWorkspaceID).Scan(&leaderID); err != nil {
		t.Fatalf("load leader agent: %v", err)
	}

	var archivedSquadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id, archived_at)
		VALUES ($1, 'Archived Export Squad', '', $2, $3, now())
		RETURNING id
	`, testWorkspaceID, leaderID, testUserID).Scan(&archivedSquadID); err != nil {
		t.Fatalf("create archived squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, archivedSquadID)
	})

	file := decodeBackup(t, "include_types=agents,squads")

	var foundAgent, foundSquad bool
	for _, a := range file.Agents {
		if a.ID == archivedAgentID {
			foundAgent = true
			if a.ArchivedAt == nil {
				t.Error("archived agent: ArchivedAt should be non-nil in backup")
			}
		}
	}
	for _, s := range file.Squads {
		if s.ID == archivedSquadID {
			foundSquad = true
			if s.ArchivedAt == nil {
				t.Error("archived squad: ArchivedAt should be non-nil in backup")
			}
		}
	}
	if !foundAgent {
		t.Errorf("archived agent %s not found in export (%d agents total)", archivedAgentID, len(file.Agents))
	}
	if !foundSquad {
		t.Errorf("archived squad %s not found in export (%d squads total)", archivedSquadID, len(file.Squads))
	}
}

// TestBackupExportMemberDeduplication verifies that the same human referenced
// from multiple fields (creator and assignee) produces exactly one BackupMember row.
func TestBackupExportMemberDeduplication(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database configured")
	}
	ctx := context.Background()

	// The test user appears as both creator and assignee — two distinct member
	// references that must collapse to a single BackupMember row.
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, description, status, priority,
			creator_type, creator_id, assignee_type, assignee_id
		)
		VALUES ($1, 'Member dedup test', '', 'todo', 'medium',
		        'member', $2, 'member', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	file := decodeBackup(t, "include_types=issues")

	count := 0
	for _, m := range file.Members {
		if m.Email == handlerTestEmail {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 BackupMember for %q, got %d", handlerTestEmail, count)
	}
}

// TestBackupExportSkipsUnresolvableMemberRef verifies that a member actor whose
// user row does not exist is silently dropped, not emitted as an identity-less row.
// creator_id has no FK constraint, so a synthetic UUID can be planted directly.
func TestBackupExportSkipsUnresolvableMemberRef(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database configured")
	}
	ctx := context.Background()

	// A deterministic UUID with no matching "user" row — GetUser will return
	// pgx.ErrNoRows and the export must skip it rather than emit a member
	// with empty Email.
	const ghostUserID = "00000000-dead-beef-0000-000000000001"

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, description, status, priority,
			creator_type, creator_id
		)
		VALUES ($1, 'Ghost creator issue', '', 'todo', 'medium', 'member', $2)
		RETURNING id
	`, testWorkspaceID, ghostUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue with ghost creator: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	file := decodeBackup(t, "include_types=issues")

	for _, m := range file.Members {
		if m.ID == ghostUserID {
			t.Errorf("unresolvable member ref %s appeared in BackupFile.Members", ghostUserID)
		}
		if m.Email == "" {
			t.Errorf("BackupMember with empty email found (ID=%s) — identity-less row leaked", m.ID)
		}
	}
}
