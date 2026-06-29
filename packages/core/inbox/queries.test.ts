import { describe, expect, it } from "vitest";
import type { InboxItem, InboxWorkspaceUnread } from "../types";
import {
  deduplicateInboxItems,
  hasOtherWorkspaceUnread,
  inboxKeys,
  sortInboxItems,
  unreadWorkspaceIds,
} from "./queries";

function item(overrides: Partial<InboxItem>): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    title: "Issue title",
    body: null,
    issue_status: null,
    issue_priority: null,
    read: false,
    archived: false,
    created_at: "2026-06-15T08:00:00Z",
    details: null,
    ...overrides,
  };
}

describe("deduplicateInboxItems", () => {
  it("keeps the newest issue row while preserving an older comment anchor", () => {
    const merged = deduplicateInboxItems([
      item({
        id: "comment-notification",
        type: "new_comment",
        created_at: "2026-06-15T08:00:00Z",
        details: { comment_id: "comment-1" },
      }),
      item({
        id: "status-notification",
        type: "status_changed",
        created_at: "2026-06-15T08:01:00Z",
        details: { from: "in_progress", to: "in_review" },
      }),
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      id: "status-notification",
      type: "status_changed",
      details: {
        from: "in_progress",
        to: "in_review",
        comment_id: "comment-1",
      },
    });
  });

  it("preserves the newest row's own comment anchor", () => {
    const merged = deduplicateInboxItems([
      item({
        id: "older-comment",
        created_at: "2026-06-15T08:00:00Z",
        details: { comment_id: "comment-1" },
      }),
      item({
        id: "newer-comment",
        created_at: "2026-06-15T08:02:00Z",
        details: { comment_id: "comment-2" },
      }),
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]?.id).toBe("newer-comment");
    expect(merged[0]?.details?.comment_id).toBe("comment-2");
  });
});

describe("hasOtherWorkspaceUnread", () => {
  const summary = (entries: InboxWorkspaceUnread[]) => entries;

  it("is true when a workspace other than the active one has unread", () => {
    expect(
      hasOtherWorkspaceUnread(
        summary([{ workspace_id: "ws-2", count: 3 }]),
        "ws-1",
      ),
    ).toBe(true);
  });

  it("excludes the active workspace's own unread", () => {
    expect(
      hasOtherWorkspaceUnread(
        summary([{ workspace_id: "ws-1", count: 5 }]),
        "ws-1",
      ),
    ).toBe(false);
  });

  it("ignores other workspaces whose count is zero", () => {
    expect(
      hasOtherWorkspaceUnread(
        summary([{ workspace_id: "ws-2", count: 0 }]),
        "ws-1",
      ),
    ).toBe(false);
  });

  it("is true when at least one non-active workspace has unread", () => {
    expect(
      hasOtherWorkspaceUnread(
        summary([
          { workspace_id: "ws-1", count: 4 },
          { workspace_id: "ws-2", count: 1 },
        ]),
        "ws-1",
      ),
    ).toBe(true);
  });

  it("is false for an empty summary", () => {
    expect(hasOtherWorkspaceUnread([], "ws-1")).toBe(false);
  });

  it("counts every workspace as 'other' when there is no active workspace", () => {
    expect(
      hasOtherWorkspaceUnread(
        summary([{ workspace_id: "ws-1", count: 2 }]),
        null,
      ),
    ).toBe(true);
  });
});

describe("unreadWorkspaceIds", () => {
  it("collects only workspaces with a non-zero count", () => {
    const ids = unreadWorkspaceIds([
      { workspace_id: "ws-1", count: 0 },
      { workspace_id: "ws-2", count: 3 },
      { workspace_id: "ws-3", count: 1 },
    ]);
    expect(ids.has("ws-1")).toBe(false);
    expect(ids.has("ws-2")).toBe(true);
    expect(ids.has("ws-3")).toBe(true);
    expect(ids.size).toBe(2);
  });

  it("returns an empty set for an empty summary", () => {
    expect(unreadWorkspaceIds([]).size).toBe(0);
  });
});

describe("inboxKeys.unreadSummary", () => {
  it("is a stable account-level key independent of any workspace", () => {
    expect(inboxKeys.unreadSummary()).toEqual(["inbox", "unread-summary"]);
  });
});

describe("sortInboxItems", () => {
  const ids = (items: InboxItem[]) => items.map((i) => i.id);

  it("does not mutate the input array", () => {
    const input = [
      item({ id: "a", created_at: "2026-06-15T08:00:00Z" }),
      item({ id: "b", created_at: "2026-06-15T09:00:00Z" }),
    ];
    const snapshot = ids(input);
    sortInboxItems(input, "date", "desc");
    expect(ids(input)).toEqual(snapshot);
  });

  it("date desc puts newest first", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "old", created_at: "2026-06-15T08:00:00Z" }),
        item({ id: "new", created_at: "2026-06-15T10:00:00Z" }),
        item({ id: "mid", created_at: "2026-06-15T09:00:00Z" }),
      ],
      "date",
      "desc",
    );
    expect(ids(sorted)).toEqual(["new", "mid", "old"]);
  });

  it("date asc puts oldest first", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "new", created_at: "2026-06-15T10:00:00Z" }),
        item({ id: "old", created_at: "2026-06-15T08:00:00Z" }),
      ],
      "date",
      "asc",
    );
    expect(ids(sorted)).toEqual(["old", "new"]);
  });

  it("priority desc orders urgent→none and sinks items with no linked issue", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "low", issue_priority: "low" }),
        item({ id: "none", issue_priority: "none" }),
        item({ id: "no-issue", issue_id: null, issue_priority: null }),
        item({ id: "urgent", issue_priority: "urgent" }),
        item({ id: "high", issue_priority: "high" }),
        item({ id: "medium", issue_priority: "medium" }),
      ],
      "priority",
      "desc",
    );
    expect(ids(sorted)).toEqual([
      "urgent",
      "high",
      "medium",
      "low",
      "none",
      "no-issue",
    ]);
  });

  it("priority asc reverses the order", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "urgent", issue_priority: "urgent" }),
        item({ id: "low", issue_priority: "low" }),
        item({ id: "no-issue", issue_id: null, issue_priority: null }),
      ],
      "priority",
      "asc",
    );
    expect(ids(sorted)).toEqual(["no-issue", "low", "urgent"]);
  });

  it("priority desc breaks ties by newest first", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "high-old", issue_priority: "high", created_at: "2026-06-15T08:00:00Z" }),
        item({ id: "high-new", issue_priority: "high", created_at: "2026-06-15T10:00:00Z" }),
      ],
      "priority",
      "desc",
    );
    expect(ids(sorted)).toEqual(["high-new", "high-old"]);
  });

  it("unread desc puts unread first, newest within each group", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "read-new", read: true, created_at: "2026-06-15T10:00:00Z" }),
        item({ id: "unread-old", read: false, created_at: "2026-06-15T08:00:00Z" }),
        item({ id: "unread-new", read: false, created_at: "2026-06-15T09:00:00Z" }),
        item({ id: "read-old", read: true, created_at: "2026-06-15T07:00:00Z" }),
      ],
      "unread",
      "desc",
    );
    expect(ids(sorted)).toEqual(["unread-new", "unread-old", "read-new", "read-old"]);
  });

  it("unread asc puts read first", () => {
    const sorted = sortInboxItems(
      [
        item({ id: "unread", read: false }),
        item({ id: "read", read: true }),
      ],
      "unread",
      "asc",
    );
    expect(ids(sorted)).toEqual(["read", "unread"]);
  });
});
