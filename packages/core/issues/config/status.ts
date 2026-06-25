import type { IssueStatus } from "../../types";

export const STATUS_ORDER: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
  "archived",
];

export const ALL_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
  "archived",
];

/** Statuses shown as board columns (excludes cancelled/archived). */
export const BOARD_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
];

/**
 * Issue statuses that mark the issue as closed/terminal. Use this set (or
 * {@link isTerminalIssueStatus}) instead of open-coding the comparison at
 * each call site — adding a new terminal status only requires updating this
 * single source of truth.
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

export const STATUS_CONFIG: Record<
  IssueStatus,
  {
    label: string;
    iconColor: string;
    hoverBg: string;
    dividerColor: string;
    columnBg: string;
  }
> = {
  backlog: { label: "Backlog", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
  todo: { label: "Todo", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
  in_progress: { label: "In Progress", iconColor: "text-warning", hoverBg: "hover:bg-warning/10", dividerColor: "bg-warning", columnBg: "bg-warning/5" },
  in_review: { label: "In Review", iconColor: "text-success", hoverBg: "hover:bg-success/10", dividerColor: "bg-success", columnBg: "bg-success/5" },
  done: { label: "Done", iconColor: "text-info", hoverBg: "hover:bg-info/10", dividerColor: "bg-info", columnBg: "bg-info/5" },
  blocked: { label: "Blocked", iconColor: "text-destructive", hoverBg: "hover:bg-destructive/10", dividerColor: "bg-destructive", columnBg: "bg-destructive/5" },
  cancelled: { label: "Cancelled", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
  archived: { label: "Archived", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
};
