package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func inboxRequest(method, path, workspaceID string) *http.Request {
	return testutil.WithHeaders(
		testutil.JSONRequest(method, path, nil),
		"X-User-ID", testUserID,
		"X-Workspace-ID", workspaceID,
	)
}

func inboxWorkspaceHandler(handler http.HandlerFunc) http.HandlerFunc {
	return middleware.RequireWorkspaceMember(testHandler.Queries)(handler).ServeHTTP
}

func TestListInboxProjectsCurrentIssueStatusAndPriority(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Inbox filter projections", "inbox-filter-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	issueID := dbfx.Issue(t, "Filtered issue", testutil.Cols{
		"workspace_id": workspaceID,
		"status":       "in_review",
		"priority":     "high",
	})
	dbfx.Insert(t, "inbox_item", testutil.Cols{
		"workspace_id":   workspaceID,
		"recipient_type": "member",
		"recipient_id":   testUserID,
		"type":           "status_changed",
		"severity":       "info",
		"issue_id":       issueID,
		"title":          "Projected issue",
	})

	var items []InboxItemResponse
	testutil.Call(t, inboxWorkspaceHandler(testHandler.ListInbox),
		inboxRequest(http.MethodGet, "/api/inbox", workspaceID)).
		Want(http.StatusOK).
		JSON(&items)

	if len(items) != 1 {
		t.Fatalf("inbox items = %d, want 1: %+v", len(items), items)
	}
	if items[0].IssueStatus == nil || *items[0].IssueStatus != "in_review" {
		t.Errorf("issue_status = %v, want in_review", items[0].IssueStatus)
	}
	if items[0].IssuePriority == nil || *items[0].IssuePriority != "high" {
		t.Errorf("issue_priority = %v, want high", items[0].IssuePriority)
	}
}

// inboxNotification seeds one notification addressed to the test user and
// returns its id. priority is the priority of the issue it is linked to, or ""
// for a notification with no linked issue — the case whose projection is null
// rather than absent. archived starts the row in the state the archive
// endpoints read.
func inboxNotification(t *testing.T, workspaceID, priority string, archived bool) string {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id":   workspaceID,
		"recipient_type": "member",
		"recipient_id":   testUserID,
		"type":           "status_changed",
		"severity":       "info",
		"title":          "Priority projection",
		"archived":       archived,
	}
	if priority != "" {
		cols["issue_id"] = dbfx.Issue(t, "Priority projection", testutil.Cols{
			"workspace_id": workspaceID,
			"priority":     priority,
		})
	}
	return dbfx.Insert(t, "inbox_item", cols)
}

// inboxItemByID picks one notification out of a list response, failing rather
// than returning a zero value: a row the response dropped is a different
// defect from a row whose field was projected wrong, and the failure messages
// should say which.
func inboxItemByID(t *testing.T, items []InboxItemResponse, id string) InboxItemResponse {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("item %s missing from a response of %d items", id, len(items))
	return InboxItemResponse{}
}

// wantIssuePriority asserts a projected issue_priority, keeping "reported the
// wrong value" and "reported null instead of a value" apart — the client sort
// reads both as no priority, so only the response can tell them.
func wantIssuePriority(t *testing.T, got, want *string) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("issue_priority = %q, want null", *got)
	case want != nil && got == nil:
		t.Errorf("issue_priority = null, want %q", *want)
	case want != nil && *got != *want:
		t.Errorf("issue_priority = %q, want %q", *got, *want)
	}
}

// A notification with no linked issue has no priority to report, so the list
// carries null for it instead of a value carried over from elsewhere. The
// issue-backed row in the same response is the control: a projection that
// dropped the field everywhere would otherwise satisfy a null-only assertion.
// M-42's client-side sort ranks on this field, so a value invented here ranks a
// row that has no priority against rows that do.
func TestListInboxReportsNullPriorityWithoutLinkedIssue(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Inbox null priority", "inbox-null-priority-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	withIssue := inboxNotification(t, workspaceID, "high", false)
	withoutIssue := inboxNotification(t, workspaceID, "", false)

	var items []InboxItemResponse
	testutil.Call(t, inboxWorkspaceHandler(testHandler.ListInbox),
		inboxRequest(http.MethodGet, "/api/inbox", workspaceID)).
		Want(http.StatusOK).
		JSON(&items)

	cases := []struct {
		name string
		id   string
		want *string
	}{
		{"notification without a linked issue reports no priority", withoutIssue, nil},
		{"notification linked to a high-priority issue reports it", withIssue, ptr("high")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantIssuePriority(t, inboxItemByID(t, items, tc.id).IssuePriority, tc.want)
		})
	}
}

// Marking read answers with the same projection the list carries: the response
// is enriched from the issue rather than joined, so a path that stopped
// copying issue_priority would leave the client sorting a row it just mutated
// on a field only the list ever set.
func TestMarkInboxReadEnrichesIssuePriority(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Mark read priority", "mark-read-priority-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	withIssue := inboxNotification(t, workspaceID, "high", false)
	withoutIssue := inboxNotification(t, workspaceID, "", false)

	cases := []struct {
		name string
		id   string
		want *string
	}{
		{"item linked to a high-priority issue reports it", withIssue, ptr("high")},
		{"item without a linked issue reports no priority", withoutIssue, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp InboxItemResponse
			testutil.Call(t, inboxWorkspaceHandler(testHandler.MarkInboxRead),
				withURLParam(inboxRequest(http.MethodPost, "/api/inbox/"+tc.id+"/read", workspaceID), "id", tc.id)).
				Want(http.StatusOK).
				JSON(&resp)
			wantIssuePriority(t, resp.IssuePriority, tc.want)
		})
	}
}

// Archiving mutates the row and answers from the mutated row, so its priority
// comes off the same enrich path the other single-item responses use rather
// than off the list query — the field the response is written from is the one
// the sort leans on after the row moves to the archive.
func TestArchiveInboxItemEnrichesIssuePriority(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Archive item priority", "archive-item-priority-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	withIssue := inboxNotification(t, workspaceID, "high", false)
	withoutIssue := inboxNotification(t, workspaceID, "", false)

	cases := []struct {
		name string
		id   string
		want *string
	}{
		{"item linked to a high-priority issue reports it", withIssue, ptr("high")},
		{"item without a linked issue reports no priority", withoutIssue, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp InboxItemResponse
			testutil.Call(t, inboxWorkspaceHandler(testHandler.ArchiveInboxItem),
				withURLParam(inboxRequest(http.MethodPost, "/api/inbox/"+tc.id+"/archive", workspaceID), "id", tc.id)).
				Want(http.StatusOK).
				JSON(&resp)
			wantIssuePriority(t, resp.IssuePriority, tc.want)
		})
	}
}

// The archived page builds its response from the embedded inbox row and fills
// the projections separately, so it is a second place the field is set. Its
// priority filter is computed in SQL from the same joined column: a response
// that dropped the field would list a row the archive then cannot re-filter by
// the priority shown next to it.
func TestListArchivedInboxPageProjectsIssuePriority(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Archived page priority", "archive-page-priority-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	withIssue := inboxNotification(t, workspaceID, "high", true)
	withoutIssue := inboxNotification(t, workspaceID, "", true)

	var page archivedInboxPageResponse
	testutil.Call(t, inboxWorkspaceHandler(testHandler.ListArchivedInboxPage),
		inboxRequest(http.MethodGet, "/api/inbox/archived/page", workspaceID)).
		Want(http.StatusOK).
		JSON(&page)

	cases := []struct {
		name string
		id   string
		want *string
	}{
		{"archived notification without a linked issue reports no priority", withoutIssue, nil},
		{"archived notification linked to a high-priority issue reports it", withIssue, ptr("high")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantIssuePriority(t, inboxItemByID(t, page.Items, tc.id).IssuePriority, tc.want)
		})
	}
}

func TestListArchivedInboxLimitsIssueGroupsNotRows(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Archived inbox groups", "archived-groups-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	noisyIssueID := dbfx.Issue(t, "Noisy archived issue", testutil.Cols{"workspace_id": workspaceID})
	olderIssueID := dbfx.Issue(t, "Older archived issue", testutil.Cols{"workspace_id": workspaceID})

	base := time.Now().UTC().Add(-time.Minute)
	// 200 rows in one statement. The oldest (i = 199) carries the comment
	// anchor: the bounded response keeps it as the group's comment anchor, even
	// though the newest status row is the one the UI renders.
	dbfx.Exec(t, `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, archived, created_at, details)
		SELECT $1, 'member', $2, 'status_changed', 'info', $3, 'noisy-' || lpad(i::text, 3, '0'), true,
		       $4::timestamptz - i * interval '1 millisecond',
		       CASE WHEN i = 199 THEN '{"comment_id":"comment-1"}'::jsonb ELSE '{}'::jsonb END
		FROM generate_series(0, 199) AS i
	`, workspaceID, testUserID, noisyIssueID, base)
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE workspace_id = $1`, workspaceID)
	dbfx.Insert(t, "inbox_item", testutil.Cols{
		"workspace_id":   workspaceID,
		"recipient_type": "member",
		"recipient_id":   testUserID,
		"type":           "new_comment",
		"severity":       "info",
		"issue_id":       olderIssueID,
		"title":          "older-group",
		"archived":       true,
		"created_at":     base.Add(-time.Hour),
	})

	var items []InboxItemResponse
	testutil.Call(t, inboxWorkspaceHandler(testHandler.ListArchivedInbox),
		inboxRequest(http.MethodGet, "/api/inbox/archived", workspaceID)).
		Want(http.StatusOK).
		JSON(&items)

	var noisyRows int
	var sawNoisyNewest, sawCommentAnchor, sawOlderGroup bool
	for _, item := range items {
		switch {
		case item.IssueID != nil && *item.IssueID == noisyIssueID:
			noisyRows++
			sawNoisyNewest = sawNoisyNewest || item.Title == "noisy-000"
			sawCommentAnchor = sawCommentAnchor || strings.Contains(string(item.Details), `"comment_id":"comment-1"`)
		case item.IssueID != nil && *item.IssueID == olderIssueID:
			sawOlderGroup = true
		}
	}
	if noisyRows != 2 || !sawNoisyNewest || !sawCommentAnchor {
		t.Fatalf("noisy group rows = %d, newest=%v anchor=%v; items=%+v",
			noisyRows, sawNoisyNewest, sawCommentAnchor, items)
	}
	if !sawOlderGroup {
		t.Fatal("raw-row limit let one issue hide another archived issue group")
	}
}

func TestArchiveAllReadInboxUsesNewestIssueRow(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Archive read groups", "archive-read-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	readIssueID := dbfx.Issue(t, "Newest row is read", testutil.Cols{"workspace_id": workspaceID})
	unreadIssueID := dbfx.Issue(t, "Newest row is unread", testutil.Cols{"workspace_id": workspaceID})

	insert := func(issueID, title string, read bool, createdAt testutil.Raw) {
		t.Helper()
		dbfx.Insert(t, "inbox_item", testutil.Cols{
			"workspace_id":   workspaceID,
			"recipient_type": "member",
			"recipient_id":   testUserID,
			"type":           "status_changed",
			"severity":       "info",
			"issue_id":       issueID,
			"title":          title,
			"read":           read,
			"archived":       false,
			"created_at":     createdAt,
		})
	}
	insert(readIssueID, "older unread", false, "now() - interval '2 minutes'")
	insert(readIssueID, "newest read", true, "now() - interval '1 minute'")
	insert(unreadIssueID, "older read", true, "now() - interval '2 minutes'")
	insert(unreadIssueID, "newest unread", false, "now() - interval '1 minute'")

	testutil.Call(t, inboxWorkspaceHandler(testHandler.ArchiveAllReadInbox),
		inboxRequest(http.MethodPost, "/api/inbox/archive-all-read", workspaceID)).
		Want(http.StatusOK)

	if got := dbfx.Count(t,
		"SELECT count(*) FROM inbox_item WHERE issue_id = $1 AND archived = true", readIssueID); got != 2 {
		t.Fatalf("archived rows in read issue = %d, want the whole two-row group", got)
	}
	if got := dbfx.Count(t,
		"SELECT count(*) FROM inbox_item WHERE issue_id = $1 AND archived = true", unreadIssueID); got != 0 {
		t.Fatalf("archived rows in unread issue = %d, want the whole group untouched", got)
	}
}

func TestArchiveCompletedInboxExpandsCustomTerminalStatuses(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Archive custom completed", "archive-custom-completed-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": workspaceID,
		"key":          "verified_complete",
		"name":         "Verified complete",
		"category":     "done",
		"color":        "#22c55e",
		"is_system":    false,
		"position":     1,
	})
	completedIssueID := dbfx.Issue(t, "Custom completed issue", testutil.Cols{
		"workspace_id": workspaceID,
		"status":       "verified_complete",
	})
	openIssueID := dbfx.Issue(t, "Open issue", testutil.Cols{
		"workspace_id": workspaceID,
		"status":       "todo",
	})
	for _, issueID := range []string{completedIssueID, openIssueID} {
		dbfx.Insert(t, "inbox_item", testutil.Cols{
			"workspace_id":   workspaceID,
			"recipient_type": "member",
			"recipient_id":   testUserID,
			"type":           "status_changed",
			"severity":       "info",
			"issue_id":       issueID,
			"title":          "Status changed",
			"archived":       false,
		})
	}

	testutil.Call(t, inboxWorkspaceHandler(testHandler.ArchiveCompletedInbox),
		inboxRequest(http.MethodPost, "/api/inbox/archive-completed", workspaceID)).
		Want(http.StatusOK)

	if got := dbfx.Count(t,
		"SELECT count(*) FROM inbox_item WHERE issue_id = $1 AND archived = true", completedIssueID); got != 1 {
		t.Fatalf("archived rows for custom completed issue = %d, want 1", got)
	}
	if got := dbfx.Count(t,
		"SELECT count(*) FROM inbox_item WHERE issue_id = $1 AND archived = true", openIssueID); got != 0 {
		t.Fatalf("archived rows for open issue = %d, want 0", got)
	}
}

func TestInboxListBodyPreview(t *testing.T) {
	issue := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	text := func(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
	long := strings.Repeat("a", 5000)
	// Every CJK character is three bytes in UTF-8: a byte-based cut would land
	// mid-character and produce invalid UTF-8.
	longCJK := strings.Repeat("评论内容", 500)

	cases := []struct {
		name      string
		notifType string
		issueID   pgtype.UUID
		body      pgtype.Text
		want      *string
	}{
		{"null body stays null", "new_comment", issue, pgtype.Text{}, nil},
		{"short comment is untouched", "new_comment", issue, text("looks good"), ptr("looks good")},
		{"exactly at the limit is untouched", "new_comment", issue,
			text(strings.Repeat("a", inboxListBodyPreviewLimit)),
			ptr(strings.Repeat("a", inboxListBodyPreviewLimit))},
		{"one past the limit is cut, ellipsis included", "new_comment", issue,
			text(strings.Repeat("a", inboxListBodyPreviewLimit+1)),
			ptr(strings.Repeat("a", inboxListBodyPreviewLimit-1) + "…")},
		{"long comment is cut to the limit", "new_comment", issue, text(long),
			ptr(strings.Repeat("a", inboxListBodyPreviewLimit-1) + "…")},
		// Issue-less notifications render their body in the detail pane from
		// the list cache, so shortening them would lose content.
		{"comment without an issue keeps its full body", "new_comment", pgtype.UUID{}, text(long), ptr(long)},
		// Other types are out of scope even when issue-backed: their body is
		// not merely a preview of something the issue page shows.
		{"other types keep their full body", "task_failed", issue, text(long), ptr(long)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inboxListBody(tc.notifType, tc.issueID, tc.body)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("body = %q, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("body = nil, want %d characters", utf8.RuneCountInString(*tc.want))
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("body = %d characters %q…, want %d characters",
					utf8.RuneCountInString(*got), truncateForLog(*got),
					utf8.RuneCountInString(*tc.want))
			}
		})
	}

	t.Run("multi-byte text is cut on a character boundary", func(t *testing.T) {
		got := inboxListBody("new_comment", issue, text(longCJK))
		if got == nil {
			t.Fatal("body = nil")
		}
		if !utf8.ValidString(*got) {
			t.Fatalf("preview is not valid UTF-8: %q", truncateForLog(*got))
		}
		if n := utf8.RuneCountInString(*got); n != inboxListBodyPreviewLimit {
			t.Fatalf("preview = %d characters, want %d", n, inboxListBodyPreviewLimit)
		}
		if !strings.HasSuffix(*got, "…") {
			t.Fatalf("preview does not end with an ellipsis: %q", truncateForLog(*got))
		}
	})
}

// Both inbox lists ship the preview, the stored row keeps the whole comment,
// and a notification that needs its full body still gets it.
func TestInboxListsShipCommentPreviewNotFullComment(t *testing.T) {
	workspaceID := dbfx.Workspace(t, "Inbox body preview", "inbox-preview-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	activeIssue := dbfx.Issue(t, "Active comment issue", testutil.Cols{"workspace_id": workspaceID})
	archivedIssue := dbfx.Issue(t, "Archived comment issue", testutil.Cols{"workspace_id": workspaceID})
	fullComment := strings.Repeat("A long agent reply. ", 300)
	issueLessBody := strings.Repeat("An issue-less notice. ", 300)

	insert := func(cols testutil.Cols) {
		base := testutil.Cols{
			"workspace_id":   workspaceID,
			"recipient_type": "member",
			"recipient_id":   testUserID,
			"severity":       "info",
			"title":          "Notification",
		}
		for k, v := range cols {
			base[k] = v
		}
		dbfx.Insert(t, "inbox_item", base)
	}
	insert(testutil.Cols{"type": "new_comment", "issue_id": activeIssue, "body": fullComment})
	insert(testutil.Cols{"type": "new_comment", "issue_id": archivedIssue, "body": fullComment, "archived": true})
	insert(testutil.Cols{"type": "autopilot_paused", "body": issueLessBody})

	wantPreview := string([]rune(fullComment)[:inboxListBodyPreviewLimit-1]) + "…"
	bodyOf := func(items []InboxItemResponse, issueID string) string {
		t.Helper()
		for _, item := range items {
			if item.IssueID != nil && *item.IssueID == issueID {
				if item.Body == nil {
					t.Fatalf("item for issue %s has no body", issueID)
				}
				return *item.Body
			}
		}
		t.Fatalf("no item for issue %s in %d items", issueID, len(items))
		return ""
	}

	var active []InboxItemResponse
	testutil.Call(t, inboxWorkspaceHandler(testHandler.ListInbox),
		inboxRequest(http.MethodGet, "/api/inbox", workspaceID)).
		Want(http.StatusOK).
		JSON(&active)
	if got := bodyOf(active, activeIssue); got != wantPreview {
		t.Errorf("main list body = %d characters, want the %d-character preview",
			utf8.RuneCountInString(got), inboxListBodyPreviewLimit)
	}
	var issueLess *string
	for _, item := range active {
		if item.IssueID == nil {
			issueLess = item.Body
		}
	}
	if issueLess == nil || *issueLess != issueLessBody {
		t.Errorf("issue-less notification lost its full body (its detail pane renders it)")
	}

	var archived []InboxItemResponse
	testutil.Call(t, inboxWorkspaceHandler(testHandler.ListArchivedInbox),
		inboxRequest(http.MethodGet, "/api/inbox/archived", workspaceID)).
		Want(http.StatusOK).
		JSON(&archived)
	if got := bodyOf(archived, archivedIssue); got != wantPreview {
		t.Errorf("archived list body = %d characters, want the %d-character preview",
			utf8.RuneCountInString(got), inboxListBodyPreviewLimit)
	}

	// Only the response is shortened; the stored comment is whole.
	if got := dbfx.Count(t,
		`SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'new_comment' AND body = $2`,
		workspaceID, fullComment); got != 2 {
		t.Errorf("stored full comments = %d, want 2", got)
	}
}

func truncateForLog(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40])
	}
	return s
}
