-- Reverse 122_issue_git_branches.up.sql.

DROP INDEX IF EXISTS issue_git_work_branch_active_uidx;

ALTER TABLE issue
    DROP COLUMN IF EXISTS git_work_branch,
    DROP COLUMN IF EXISTS git_base_branch;
