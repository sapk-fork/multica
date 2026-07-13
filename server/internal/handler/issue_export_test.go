package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExportIssueIncludesChildrenCommentsLabelsAndMetadata verifies the M-74
// export endpoint returns a flat issue list with parent_issue_id linkage
// (not nested children), and that each entry carries its comments, labels,
// and metadata. Exercises lookup by human-readable identifier, matching how
// every other issue handler resolves the {id} path param.
func TestExportIssueIncludesChildrenCommentsLabelsAndMetadata(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Export parent issue",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	json.NewDecoder(w.Body).Decode(&parent)
	t.Cleanup(func() {
		req := newRequest("DELETE", "/api/issues/"+parent.ID, nil)
		req = withURLParam(req, "id", parent.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Export child issue",
		"parent_issue_id": parent.ID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var child IssueResponse
	json.NewDecoder(w.Body).Decode(&child)

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+parent.ID+"/comments", map[string]any{
		"content": "Export test comment",
	})
	req = withURLParam(req, "id", parent.ID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+parent.ID+"/metadata/pr_url", map[string]any{
		"value": "https://example.com/pr/1",
	})
	req = withURLParams(req, "id", parent.ID, "key", "pr_url")
	testHandler.SetIssueMetadataKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SetIssueMetadataKey: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/labels", map[string]any{
		"name":  "export-test-label",
		"color": "#22c55e",
	})
	testHandler.CreateLabel(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateLabel: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var label LabelResponse
	json.NewDecoder(w.Body).Decode(&label)
	t.Cleanup(func() {
		req := newRequest("DELETE", "/api/labels/"+label.ID, nil)
		req = withURLParam(req, "id", label.ID)
		testHandler.DeleteLabel(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+parent.ID+"/labels", map[string]any{
		"label_id": label.ID,
	})
	req = withURLParam(req, "id", parent.ID)
	testHandler.AttachLabel(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("AttachLabel: expected 200/201, got %d: %s", w.Code, w.Body.String())
	}

	// Export by human-readable identifier, mirroring how every other
	// {id}-scoped issue handler resolves the path param.
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+parent.Identifier+"/export", nil)
	req = withURLParam(req, "id", parent.Identifier)
	testHandler.ExportIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ExportIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var exported ExportResponse
	if err := json.NewDecoder(w.Body).Decode(&exported); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if len(exported.Issues) != 2 {
		t.Fatalf("ExportIssue: expected 2 flat issues (parent+child), got %d", len(exported.Issues))
	}

	var gotParent, gotChild *ExportIssueResponse
	for i := range exported.Issues {
		switch exported.Issues[i].ID {
		case parent.ID:
			gotParent = &exported.Issues[i]
		case child.ID:
			gotChild = &exported.Issues[i]
		}
	}
	if gotParent == nil || gotChild == nil {
		t.Fatalf("ExportIssue: expected both parent %s and child %s present, got %+v", parent.ID, child.ID, exported.Issues)
	}

	if gotParent.ParentIssueID != nil {
		t.Fatalf("ExportIssue: root parent_issue_id = %v, want nil", *gotParent.ParentIssueID)
	}
	if gotChild.ParentIssueID == nil || *gotChild.ParentIssueID != parent.ID {
		t.Fatalf("ExportIssue: child parent_issue_id = %v, want %s (flat linkage, not nesting)", gotChild.ParentIssueID, parent.ID)
	}

	if len(gotParent.Comments) != 1 || gotParent.Comments[0].Content != "Export test comment" {
		t.Fatalf("ExportIssue: parent comments = %+v, want 1 comment 'Export test comment'", gotParent.Comments)
	}
	if gotParent.Comments[0].Reactions == nil || gotParent.Comments[0].Attachments == nil {
		t.Fatalf("ExportIssue: comment reactions/attachments must be empty arrays, not null: %+v", gotParent.Comments[0])
	}

	if gotParent.Metadata["pr_url"] != "https://example.com/pr/1" {
		t.Fatalf("ExportIssue: parent metadata = %+v, want pr_url", gotParent.Metadata)
	}

	if len(gotParent.Labels) != 1 || gotParent.Labels[0].ID != label.ID {
		t.Fatalf("ExportIssue: parent labels = %+v, want [%s]", gotParent.Labels, label.ID)
	}
}

// TestExportIssueNoCommentsOrLabelsReturnsEmptyArrays guards against nil
// slices leaking into the JSON payload as `null` for a leaf issue with no
// comments/labels/children — consumers on the importing instance would need
// defensive nil checks for every field otherwise.
func TestExportIssueNoCommentsOrLabelsReturnsEmptyArrays(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Export leaf issue with nothing attached",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	t.Cleanup(func() {
		req := newRequest("DELETE", "/api/issues/"+issue.ID, nil)
		req = withURLParam(req, "id", issue.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issue.ID+"/export", nil)
	req = withURLParam(req, "id", issue.ID)
	testHandler.ExportIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ExportIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if strings.Contains(body, `"comments":null`) || strings.Contains(body, `"labels":null`) {
		t.Fatalf("ExportIssue: comments/labels serialized as null, want empty array: %s", body)
	}

	var exported ExportResponse
	if err := json.NewDecoder(w.Body).Decode(&exported); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if len(exported.Issues) != 1 {
		t.Fatalf("ExportIssue: expected 1 issue, got %d", len(exported.Issues))
	}
	if exported.Issues[0].Comments == nil || len(exported.Issues[0].Comments) != 0 {
		t.Fatalf("ExportIssue: comments = %+v, want empty non-nil slice", exported.Issues[0].Comments)
	}
	if exported.Issues[0].Labels == nil || len(exported.Issues[0].Labels) != 0 {
		t.Fatalf("ExportIssue: labels = %+v, want empty non-nil slice", exported.Issues[0].Labels)
	}
}

// TestExportIssueCrossWorkspaceReturns404 guards the workspace boundary on
// the export endpoint specifically: since export walks descendants and
// pulls comments/attachments, a boundary miss here would leak an entire
// issue subtree to a caller scoped to a different workspace, not just a
// single field.
func TestExportIssueCrossWorkspaceReturns404(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Export cross-workspace guard issue",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	t.Cleanup(func() {
		req := newRequest("DELETE", "/api/issues/"+issue.ID, nil)
		req = withURLParam(req, "id", issue.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issue.ID+"/export", nil)
	req.Header.Set("X-Workspace-ID", "00000000-0000-0000-0000-000000000000")
	req = withURLParam(req, "id", issue.ID)
	testHandler.ExportIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ExportIssue cross-workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
