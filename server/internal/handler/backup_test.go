package handler

import (
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
