package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// assertConflictBodyShape decodes the 409 body and asserts that the
// "issue" key has the same JSON keys as IssueResponse. Both
// pre-check paths (create, update) and the post-race path
// (service.ErrGitWorkBranchConflict → handler render) must produce a
// body with the same `issue` shape — without this lock-in, a future
// refactor can silently re-introduce the drift where the pre-check
// returns a raw db.Issue (sqlc internals leak) while the post-race
// path returns an IssueResponse.
//
// Note: we don't require *every* IssueResponse field to be present in
// the body — some fields are `omitempty` (e.g. assignee_id, description,
// git_work_branch) and a zero value legitimately drops them. What we
// lock in is the *opposite* direction: no field in the body may be one
// that IssueResponse doesn't know about. That's the failure mode
// pre-check could produce (sqlc internal field names like
// `first_executed_at` or `origin_id`).
func assertConflictBodyShape(t *testing.T, body []byte) {
	t.Helper()
	var parsed struct {
		Code  string         `json:"code"`
		Error string         `json:"error"`
		Issue map[string]any `json:"issue"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode 409 body: %v\nbody: %s", err, string(body))
	}
	if parsed.Code != "git_work_branch_in_use" {
		t.Errorf("code = %q, want %q", parsed.Code, "git_work_branch_in_use")
	}
	if parsed.Error == "" {
		t.Errorf("error is empty")
	}
	if parsed.Issue == nil {
		t.Fatalf("issue is missing in body: %s", string(body))
	}

	// Build the set of known IssueResponse JSON keys via reflection
	// (so the test stays in sync with struct changes automatically).
	known := issueResponseJSONKeys(t)
	for k := range parsed.Issue {
		if _, ok := known[k]; !ok {
			t.Errorf("conflict body has key %q that is not in IssueResponse — sqlc-internal field leak (present keys: %v)", k, mapKeys(parsed.Issue))
		}
	}
}

// issueResponseJSONKeys returns the set of JSON keys IssueResponse
// marshals, derived from struct field tags. We use reflection so the
// test stays in sync with IssueResponse changes without maintaining a
// hardcoded key list.
func issueResponseJSONKeys(t *testing.T) map[string]struct{} {
	t.Helper()
	rt := reflect.TypeOf(IssueResponse{})
	known := make(map[string]struct{}, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := f.Tag.Get("json")
		// strip ",omitempty" and similar
		name := tag
		for j, c := range tag {
			if c == ',' {
				name = tag[:j]
				break
			}
		}
		if name == "-" || name == "" {
			// unexported or no json tag — skip
			continue
		}
		known[name] = struct{}{}
	}
	return known
}

// TestCreateIssueGitWorkBranchConflictBody locks in the body shape for
// the create pre-check 409 path. The body must surface the
// `git_work_branch_in_use` code and an `issue` object whose keys match
// IssueResponse — same as the post-race path that fires only on
// concurrent creates.
func TestCreateIssueGitWorkBranchConflictBody(t *testing.T) {
	const workBranch = "feature/mul-44-conflict-body"

	first := createTestIssue(t, "MUL-44: conflict-body first", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, first) })

	// Update the first issue to claim the work branch.
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+first, map[string]any{
		"git_work_branch": workBranch,
	})
	req = withURLParam(req, "id", first)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set work branch on first: %d %s", w.Code, w.Body.String())
	}

	// Second create with the same work branch — pre-check 409 path.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "MUL-44: conflict-body second",
		"git_work_branch": workBranch,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	assertConflictBodyShape(t, w.Body.Bytes())
}

// TestUpdateIssueGitWorkBranchConflictBody locks in the body shape for
// the update pre-check 409 path. The body must surface the
// `git_work_branch_in_use` code and an `issue` object whose keys match
// IssueResponse — same as the create pre-check path and the post-race
// path. Without this lock-in, a future refactor can silently re-introduce
// the drift.
func TestUpdateIssueGitWorkBranchConflictBody(t *testing.T) {
	const workBranch = "feature/mul-44-conflict-body-update"

	first := createTestIssue(t, "MUL-44: update-conflict first", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, first) })

	// Update the first issue to claim the work branch.
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+first, map[string]any{
		"git_work_branch": workBranch,
	})
	req = withURLParam(req, "id", first)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set work branch on first: %d %s", w.Code, w.Body.String())
	}

	// Second issue attempts to claim the same work branch via update.
	second := createTestIssue(t, "MUL-44: update-conflict second", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, second) })

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+second, map[string]any{
		"git_work_branch": workBranch,
	})
	req = withURLParam(req, "id", second)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	assertConflictBodyShape(t, w.Body.Bytes())
}

// mapKeys returns the sorted keys of a map[string]any for stable error
// messages.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// small insertion sort — for the handful of keys we ever print in
	// test failure messages, this beats pulling in `sort`.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
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
