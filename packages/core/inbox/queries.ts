import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { InboxItem, InboxWorkspaceUnread } from "../types";
import type { InboxSortDirection, InboxSortField } from "./store";

export const inboxKeys = {
  all: (wsId: string) => ["inbox", wsId] as const,
  list: (wsId: string) => [...inboxKeys.all(wsId), "list"] as const,
  // Account-level (not workspace-scoped): a single shared cache entry that
  // holds unread counts for every workspace the user belongs to.
  unreadSummary: () => ["inbox", "unread-summary"] as const,
};

export function inboxListOptions(wsId: string) {
  return queryOptions({
    queryKey: inboxKeys.list(wsId),
    queryFn: () => api.listInbox(),
  });
}

/**
 * Cross-workspace unread inbox summary. One cache entry shared across all
 * workspaces — the data is account-level, so switching workspaces does not
 * refetch it; only the derived "is this for another workspace" view changes.
 */
export function inboxUnreadSummaryOptions() {
  return queryOptions({
    queryKey: inboxKeys.unreadSummary(),
    queryFn: () => api.getInboxUnreadSummary(),
  });
}

/**
 * Whether any workspace OTHER than `currentWsId` has unread inbox items.
 * Drives the workspace-switcher dot: the active workspace's own unread is
 * already surfaced by the Inbox nav count, so it is excluded here to avoid a
 * duplicate signal.
 */
export function hasOtherWorkspaceUnread(
  summary: InboxWorkspaceUnread[],
  currentWsId: string | null | undefined,
): boolean {
  return summary.some((s) => s.workspace_id !== currentWsId && s.count > 0);
}

/**
 * Set of workspace ids that have unread inbox items. Lets the workspace
 * switcher dropdown mark WHICH workspace a pending message lives in (the
 * aggregate switcher dot only says "somewhere else"). Workspaces with a zero
 * count are excluded.
 */
export function unreadWorkspaceIds(summary: InboxWorkspaceUnread[]): Set<string> {
  return new Set(summary.filter((s) => s.count > 0).map((s) => s.workspace_id));
}

/**
 * Unread inbox count for the given workspace, aligned with what the inbox
 * list UI renders: archived items excluded, then deduplicated by issue so a
 * single issue with three unread notifications counts once.
 */
export function useInboxUnreadCount(wsId: string | null | undefined): number {
  const { data } = useQuery({
    queryKey: inboxKeys.list(wsId ?? ""),
    queryFn: () => api.listInbox(),
    enabled: !!wsId,
    select: (items: InboxItem[]) =>
      deduplicateInboxItems(items).filter((i) => !i.read).length,
  });
  return data ?? 0;
}

/**
 * Deduplicate inbox items by issue_id (one entry per issue, Linear-style).
 * Exported for consumers to use in useMemo — not in queryOptions select
 * (to avoid new array references on every cache update).
 */
export function deduplicateInboxItems(items: InboxItem[]): InboxItem[] {
  const active = items.filter((i) => !i.archived);
  const groups = new Map<string, InboxItem[]>();
  for (const item of active) {
    const key = item.issue_id ?? item.id;
    const group = groups.get(key) ?? [];
    group.push(item);
    groups.set(key, group);
  }
  const merged: InboxItem[] = [];
  for (const group of groups.values()) {
    group.sort(
      (a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    );
    const newest = group[0];
    if (!newest) continue;

    const commentId =
      newest.details?.comment_id ??
      group.find((item) => item.details?.comment_id)?.details?.comment_id;

    if (commentId && newest.details?.comment_id !== commentId) {
      merged.push({
        ...newest,
        details: { ...(newest.details ?? {}), comment_id: commentId },
      });
      continue;
    }

    merged.push(newest);
  }
  return merged.sort(
    (a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
}

// Higher rank sorts first under the default (desc) direction. Items with no
// linked issue have a null priority and rank below "none", so they land at the
// bottom of a priority-desc list.
const INBOX_PRIORITY_RANK: Record<string, number> = {
  urgent: 5,
  high: 4,
  medium: 3,
  low: 2,
  none: 1,
};

function inboxDateMillis(item: InboxItem): number {
  return new Date(item.created_at).getTime();
}

// Per-field score where a HIGHER value sorts first under "desc" (the default
// direction for every field). "asc" inverts the comparison.
function inboxSortScore(item: InboxItem, field: InboxSortField): number {
  switch (field) {
    case "priority":
      return item.issue_priority
        ? (INBOX_PRIORITY_RANK[item.issue_priority] ?? 0)
        : 0;
    case "unread":
      // Unread (read === false) outranks read.
      return item.read ? 0 : 1;
    default:
      return inboxDateMillis(item);
  }
}

/**
 * Sort the (already deduplicated) inbox list by the chosen field/direction.
 * Pure and non-mutating — returns a new array. Date is the secondary key for
 * every field, so within an equal priority/read group items stay newest-first.
 */
export function sortInboxItems(
  items: InboxItem[],
  field: InboxSortField,
  direction: InboxSortDirection,
): InboxItem[] {
  const factor = direction === "asc" ? -1 : 1;
  return [...items].sort((a, b) => {
    const primary =
      factor * (inboxSortScore(b, field) - inboxSortScore(a, field));
    if (primary !== 0) return primary;
    // Stable secondary key: newest first.
    return inboxDateMillis(b) - inboxDateMillis(a);
  });
}
