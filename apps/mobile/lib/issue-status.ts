/**
 * Mirror of the BOARD_STATUSES order + status labels from
 * packages/core/issues/config/status.ts.
 *
 * Mirrored, not imported: the source file co-exports `STATUS_CONFIG` with
 * web colour tokens (Tailwind v4 syntax) that mobile must not pull in.
 * Keeping this list owned by mobile keeps the import boundary clean.
 *
 * If web ever reorders BOARD_STATUSES or adds/removes a status, this file
 * must be updated to keep the "Counts and visibility must agree" rule
 * (apps/mobile/CLAUDE.md) intact.
 */
import type { IssuePriority, IssueStatus } from "@multica/core/types";

/** Statuses surfaced in list/board views (matches web — `cancelled`/`archived` excluded). */
export const BOARD_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
];

export const STATUS_LABEL: Record<IssueStatus, string> = {
  backlog: "Backlog",
  todo: "Todo",
  in_progress: "In Progress",
  in_review: "In Review",
  done: "Done",
  blocked: "Blocked",
  cancelled: "Cancelled",
  archived: "Archived",
};

/**
 * Issue statuses that mark the issue as closed/terminal. Mirrors web's
 * `TERMINAL_ISSUE_STATUSES` (packages/core/issues/config/status.ts) — kept
 * in sync manually because mobile mirrors the whole status surface (see
 * file header) and does not import web's helper.
 */
export const TERMINAL_ISSUE_STATUSES: ReadonlySet<IssueStatus> = new Set<IssueStatus>([
  "done",
  "cancelled",
  "archived",
]);

/** Returns true if the given status is terminal (closed: done/cancelled/archived). */
export function isTerminalIssueStatus(status: IssueStatus | string | null | undefined): boolean {
  if (!status) return false;
  return TERMINAL_ISSUE_STATUSES.has(status as IssueStatus);
}

export const PRIORITY_LABEL: Record<IssuePriority, string> = {
  none: "No priority",
  low: "Low",
  medium: "Medium",
  high: "High",
  urgent: "Urgent",
};
