-- MUL-44: add optional git_work_branch and git_base_branch to the issue table.
-- Both fields are optional and additive; existing behavior is preserved when
-- they are unset. The uniqueness index below is partial (status NOT IN
-- ('done', 'cancelled')) so a closed issue releases its work branch slot
-- and a follow-up issue can reuse the same branch name.
--
-- Enforcement layering:
--   1. CHECK constraints in this migration (length, format basics) catch the
--      cheapest mistakes at the DB layer.
--   2. The handler's validateBranchName helper (handler/issue.go) catches the
--      full format rules (HEAD, .., leading -, forbidden work-branch names,
--      work != base, etc.) so a 400 with a clear message is returned before
--      any DB round trip.
--   3. The partial unique index below is the last-resort safety net for the
--      "two concurrent creates for the same work branch" race: the handler
--      also runs FindActiveIssueByWorkBranch inside the same tx, so most
--      conflicts are caught with a structured 409 instead of this index
--      raising a constraint violation. Both layers are intentional.

ALTER TABLE issue
    ADD COLUMN git_work_branch TEXT
        CHECK (char_length(git_work_branch) <= 200),
    ADD COLUMN git_base_branch TEXT
        CHECK (char_length(git_base_branch) <= 200);

-- One work branch per workspace for any non-terminal issue. Done and
-- cancelled issues release the slot so a follow-up issue can reuse the
-- branch name; partial index keeps the constraint scoped to active work.
CREATE UNIQUE INDEX issue_git_work_branch_active_uidx
    ON issue (workspace_id, git_work_branch)
    WHERE git_work_branch IS NOT NULL
      AND status NOT IN ('done', 'cancelled');
