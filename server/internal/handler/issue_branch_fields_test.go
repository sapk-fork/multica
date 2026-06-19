package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateIssueGitBranchFields covers the happy path of creating an
// issue with both fields populated. The branches should round-trip
// through the response so the agent brief can surface them.
func TestCreateIssueGitBranchFields(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "MUL-44: branch fields round-trip",
		"git_work_branch": "feature/mul-44-branch-fields",
		"git_base_branch": "main",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { deleteTestIssue(t, issue.ID) })
	if issue.GitWorkBranch == nil || *issue.GitWorkBranch != "feature/mul-44-branch-fields" {
		t.Errorf("git_work_branch = %v, want %q", issue.GitWorkBranch, "feature/mul-44-branch-fields")
	}
	if issue.GitBaseBranch == nil || *issue.GitBaseBranch != "main" {
		t.Errorf("git_base_branch = %v, want %q", issue.GitBaseBranch, "main")
	}
}

// TestCreateIssueGitWorkBranchConflict covers the 409 path: the second
// create with the same git_work_branch must fail with the
// git_work_branch_in_use code and surface the conflicting issue's
// identifier.
func TestCreateIssueGitWorkBranchConflict(t *testing.T) {
	const workBranch = "feature/mul-44-conflict-target"

	first := createTestIssue(t, "MUL-44: first claim", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, first) })

	// Update the first issue to claim the work branch.
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+first, map[string]any{
		"git_work_branch": workBranch,
	})
	req = withURLParam(req, "id", first)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set work branch on first issue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Second create with the same work branch must 409.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "MUL-44: colliding claim",
		"git_work_branch": workBranch,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for work branch conflict, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "git_work_branch_in_use") {
		t.Errorf("expected code git_work_branch_in_use, got: %s", body)
	}
	if !strings.Contains(body, workBranch) {
		t.Errorf("expected body to mention %q, got: %s", workBranch, body)
	}
}

// TestCreateIssueGitBranchValidation covers the bad-input paths: HEAD,
// main/master on work branch, work == base, and invalid characters.
// All must return 400 with a clear message.
func TestCreateIssueGitBranchValidation(t *testing.T) {
	cases := []struct {
		name    string
		body    map[string]any
		wantSub string
	}{
		{"HEAD forbidden for work", map[string]any{"title": "x", "git_work_branch": "HEAD"}, "HEAD"},
		{"HEAD forbidden for base", map[string]any{"title": "x", "git_base_branch": "HEAD"}, "HEAD"},
		{"main forbidden for work", map[string]any{"title": "x", "git_work_branch": "main"}, "integration branch"},
		{"master forbidden for work", map[string]any{"title": "x", "git_work_branch": "master"}, "integration branch"},
		{"work and base equal", map[string]any{"title": "x", "git_work_branch": "feat/a", "git_base_branch": "feat/a"}, "must be different"},
		{"invalid char in work", map[string]any{"title": "x", "git_work_branch": "feat branch"}, "invalid characters"},
		{"invalid char in base", map[string]any{"title": "x", "git_base_branch": "feat branch"}, "invalid characters"},
		{"leading dash on work", map[string]any{"title": "x", "git_work_branch": "-bad"}, "start with"},
		{"double dot in work", map[string]any{"title": "x", "git_work_branch": "feat..branch"}, ".."},
		{"main ok for base", map[string]any{"title": "x", "git_base_branch": "main"}, ""}, // 201, body ignored below
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, tt.body)
			testHandler.CreateIssue(w, req)
			if tt.wantSub == "" {
				// 201 case (main ok for base). Capture id for cleanup.
				if w.Code != http.StatusCreated {
					t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
				}
				var issue IssueResponse
				if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
					t.Fatalf("decode: %v", err)
				}
				t.Cleanup(func() { deleteTestIssue(t, issue.ID) })
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if body := w.Body.String(); !strings.Contains(body, tt.wantSub) {
				t.Errorf("expected body to contain %q, got: %s", tt.wantSub, body)
			}
		})
	}
}

// TestUpdateIssueGitBranchSetClear covers the update path: set on
// issue without an existing value, then clear with explicit null in
// JSON. The CLI sends an empty string to clear, but the handler treats
// explicit null / explicit empty the same way at the SQL layer.
func TestUpdateIssueGitBranchSetClear(t *testing.T) {
	issueID := createTestIssue(t, "MUL-44: set+clear", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	// Set both fields.
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"git_work_branch": "feature/mul-44-set-clear",
		"git_base_branch": "main",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode set: %v", err)
	}
	if resp.GitWorkBranch == nil || *resp.GitWorkBranch != "feature/mul-44-set-clear" {
		t.Errorf("set: git_work_branch = %v", resp.GitWorkBranch)
	}
	if resp.GitBaseBranch == nil || *resp.GitBaseBranch != "main" {
		t.Errorf("set: git_base_branch = %v", resp.GitBaseBranch)
	}

	// Clear both with explicit null. The handler's UpdateIssue treats
	// explicit null the same as empty string at the SQL layer.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"git_work_branch": nil,
		"git_base_branch": nil,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp = IssueResponse{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if resp.GitWorkBranch != nil {
		t.Errorf("clear: git_work_branch = %v, want nil", *resp.GitWorkBranch)
	}
	if resp.GitBaseBranch != nil {
		t.Errorf("clear: git_base_branch = %v, want nil", *resp.GitBaseBranch)
	}
}

// TestUpdateIssueGitBranchWorkEqualBaseRejected covers the
// cross-field check on update: setting both fields to the same value
// in one update must 400.
func TestUpdateIssueGitBranchWorkEqualBaseRejected(t *testing.T) {
	issueID := createTestIssue(t, "MUL-44: work=base", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"git_work_branch": "feat/same",
		"git_base_branch": "feat/same",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "must be different") {
		t.Errorf("expected body to mention 'must be different', got: %s", body)
	}
}
