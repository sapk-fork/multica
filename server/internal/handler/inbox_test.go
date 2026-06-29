package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// inboxRowToResponse propagates issue_priority from the SQL join into InboxItemResponse
func TestInboxRowToResponse_IssuePriority(t *testing.T) {
	ts := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	cases := []struct {
		name     string
		priority pgtype.Text
		want     *string
	}{
		{
			name:     "priority from joined issue",
			priority: pgtype.Text{String: "high", Valid: true},
			want:     strPtr("high"),
		},
		{
			name:     "null priority when inbox item has no linked issue",
			priority: pgtype.Text{Valid: false},
			want:     nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := db.ListInboxItemsRow{
				RecipientType: "member",
				Type:          "notification",
				Severity:      "info",
				Title:         "test",
				Details:       []byte("{}"),
				IssuePriority: tc.priority,
				CreatedAt:     ts,
			}
			got := inboxRowToResponse(row)
			if tc.want == nil {
				if got.IssuePriority != nil {
					t.Errorf("want nil, got %q", *got.IssuePriority)
				}
				return
			}
			if got.IssuePriority == nil {
				t.Errorf("want %q, got nil", *tc.want)
				return
			}
			if *got.IssuePriority != *tc.want {
				t.Errorf("want %q, got %q", *tc.want, *got.IssuePriority)
			}
		})
	}
}

// inboxItemFixture creates an issue with the given priority and a linked inbox item.
// Registers t.Cleanup to remove both rows. Returns (issueID, itemID) as UUID strings.
func inboxItemFixture(t *testing.T, ctx context.Context, priority string) (issueID, itemID string) {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "inbox-test " + t.Name() + " " + priority,
		"priority": priority,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}

	queries := db.New(testPool)
	item, err := queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   parseUUID(testWorkspaceID),
		RecipientType: "member",
		RecipientID:   parseUUID(testUserID),
		Type:          "notification",
		Severity:      "info",
		IssueID:       parseUUID(issue.ID),
		Title:         "notification for " + priority,
		Details:       []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateInboxItem: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issue.ID)
	})
	return issue.ID, uuidToString(item.ID)
}

// inboxItemFixtureNoIssue creates an inbox item with no linked issue.
// Registers t.Cleanup to remove the row. Returns the item UUID string.
func inboxItemFixtureNoIssue(t *testing.T, ctx context.Context) string {
	t.Helper()

	queries := db.New(testPool)
	item, err := queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   parseUUID(testWorkspaceID),
		RecipientType: "member",
		RecipientID:   parseUUID(testUserID),
		Type:          "notification",
		Severity:      "info",
		Title:         "no-issue notification",
		Details:       []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateInboxItem (no issue): %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM inbox_item WHERE id = $1`, uuidToString(item.ID))
	})
	return uuidToString(item.ID)
}

// newInboxRequest builds a request that ListInbox can use: X-User-ID header plus workspace
// ID injected into context (ListInbox reads from context, not the header).
func newInboxRequest(method, path string) *http.Request {
	req := newRequest(method, path, nil)
	ctx := middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{})
	return req.WithContext(ctx)
}

// ListInbox returns issue_priority from the SQL join; null for items with no linked issue
func TestListInboxItems_IssuePriority(t *testing.T) {
	ctx := context.Background()

	_, itemWithIssueID := inboxItemFixture(t, ctx, "urgent")
	itemNoIssueID := inboxItemFixtureNoIssue(t, ctx)

	w := httptest.NewRecorder()
	testHandler.ListInbox(w, newInboxRequest("GET", "/api/inbox"))
	if w.Code != http.StatusOK {
		t.Fatalf("ListInbox: %d %s", w.Code, w.Body.String())
	}

	var items []InboxItemResponse
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	sawWithIssue, sawNoIssue := false, false
	for _, item := range items {
		switch item.ID {
		case itemWithIssueID:
			sawWithIssue = true
			if item.IssuePriority == nil {
				t.Errorf("item linked to urgent issue: got nil issue_priority, want %q", "urgent")
			} else if *item.IssuePriority != "urgent" {
				t.Errorf("item linked to urgent issue: got %q, want %q", *item.IssuePriority, "urgent")
			}
		case itemNoIssueID:
			sawNoIssue = true
			if item.IssuePriority != nil {
				t.Errorf("item with no linked issue: got issue_priority %q, want nil", *item.IssuePriority)
			}
		}
	}
	if !sawWithIssue {
		t.Error("item linked to issue not found in ListInbox response")
	}
	if !sawNoIssue {
		t.Error("item with no linked issue not found in ListInbox response")
	}
}

// enrichInboxResponse populates issue_priority on the mark-read path, matching ListInbox
func TestMarkInboxRead_EnrichesIssuePriority(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		itemID   func() string
		wantPrio *string
	}{
		{
			name:     "item linked to issue returns issue_priority (WEB-M42)",
			itemID:   func() string { _, id := inboxItemFixture(t, ctx, "high"); return id },
			wantPrio: strPtr("high"),
		},
		{
			name:     "item with no linked issue returns null issue_priority",
			itemID:   func() string { return inboxItemFixtureNoIssue(t, ctx) },
			wantPrio: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			itemID := tc.itemID()

			w := httptest.NewRecorder()
			req := withURLParam(newRequest("POST", "/api/inbox/"+itemID+"/read", nil), "id", itemID)
			testHandler.MarkInboxRead(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("MarkInboxRead: %d %s", w.Code, w.Body.String())
			}

			var resp InboxItemResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.wantPrio == nil {
				if resp.IssuePriority != nil {
					t.Errorf("want nil, got %q", *resp.IssuePriority)
				}
				return
			}
			if resp.IssuePriority == nil {
				t.Errorf("want %q, got nil", *tc.wantPrio)
				return
			}
			if *resp.IssuePriority != *tc.wantPrio {
				t.Errorf("want %q, got %q", *tc.wantPrio, *resp.IssuePriority)
			}
		})
	}
}

// enrichInboxResponse populates issue_priority on the archive path, matching ListInbox
func TestArchiveInboxItem_EnrichesIssuePriority(t *testing.T) {
	ctx := context.Background()
	_, itemID := inboxItemFixture(t, ctx, "medium")

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/inbox/"+itemID+"/archive", nil), "id", itemID)
	testHandler.ArchiveInboxItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ArchiveInboxItem: %d %s", w.Code, w.Body.String())
	}

	var resp InboxItemResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IssuePriority == nil {
		t.Error("ArchiveInboxItem: want issue_priority \"medium\", got nil")
		return
	}
	if *resp.IssuePriority != "medium" {
		t.Errorf("ArchiveInboxItem: want %q, got %q", "medium", *resp.IssuePriority)
	}
}
