import type { Label } from "./label";

export type IssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled"
  | "archived";

export type IssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export type IssueAssigneeType = "member" | "agent" | "squad";

export interface IssueReaction {
  id: string;
  issue_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

/**
 * Per-issue metadata is a flat KV map agents use to record pipeline state
 * (PR number, pipeline_status, waiting_on, ...). Values are primitives only —
 * string / number / bool — enforced by both the API and the DB. Always
 * present in responses (empty object when unset) so reads don't need a
 * nil guard on the parent field.
 */
export type IssueMetadataValue = string | number | boolean;
export type IssueMetadata = Record<string, IssueMetadataValue>;

export interface Issue {
  id: string;
  workspace_id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: IssueStatus;
  priority: IssuePriority;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  creator_type: IssueAssigneeType;
  creator_id: string;
  parent_issue_id: string | null;
  project_id: string | null;
  position: number;
  // Ordered barrier group among sibling sub-issues (null = unstaged). The
  // parent assignee is notified/woken only when every sub-issue in a stage
  // finishes; see server/internal/handler/issue_child_done.go.
  stage: number | null;
  // Calendar days as date-only "YYYY-MM-DD" (no time, no timezone). Use the
  // helpers in @multica/core/issues/date to format/compare — never `new Date()`
  // + local formatting, which shifts the day by the viewer's offset.
  start_date: string | null;
  due_date: string | null;
  /**
   * Optional git branch names pinned to this issue. Both fields default to
   * `null` when unset — agents that see `null` should keep their existing
   * branch-derivation behavior.
   *
   * - `git_work_branch` is the dedicated branch the agent should create or
   *   check out and push commits to, and the branch it opens the PR/MR from.
   *   Server-enforced unique across non-terminal issues in the workspace.
   * - `git_base_branch` is the branch the agent should base / rebase the
   *   work branch on, and the branch the PR/MR targets.
   *
   * When either is set, agents MUST follow it (see the
   * `multica-working-on-issues` skill — section "Git Branch Fields").
   */
  git_work_branch: string | null;
  git_base_branch: string | null;
  metadata: IssueMetadata;
  reactions?: IssueReaction[];
  labels?: Label[];
  created_at: string;
  updated_at: string;
}
