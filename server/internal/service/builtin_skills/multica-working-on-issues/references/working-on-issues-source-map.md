# working-on-issues source map

Evidence layer for `SKILL.md`. Every contract the skill states is traced to a
current `file:line` here. Lines were re-derived against `feat/builtin-skills`
after the latest `main` merge; the prior skill cited pre-merge lines that have
since moved (see the "drifted" column). Re-confirm with the verification command
at the bottom before relying on an exact line.

## `multica issue create` / `update` — git branch pins (MUL-44)

The `git_work_branch` and `git_base_branch` flags were added to
`issue create` and `issue update` in MUL-44. Both fields are optional
issue-level pins; when set on an issue, the working agent must commit
to `git_work_branch` and target the PR at `git_base_branch` — the
contract is loud, not advisory. Lines below were re-derived against
this branch (`feature/m-44-git-work-and-base-branch`); re-confirm with
the verification command at the bottom before depending on an exact
line.

| Behavior | File:line |
|---|---|
| `issue create --git-work-branch` flag registration | `server/cmd/multica/cmd_issue.go:324` |
| `issue create --git-base-branch` flag registration | `server/cmd/multica/cmd_issue.go:325` |
| `issue update --git-work-branch` flag registration | `server/cmd/multica/cmd_issue.go:347` |
| `issue update --git-base-branch` flag registration | `server/cmd/multica/cmd_issue.go:348` |
| Create body maps flag → `body["git_work_branch"]` (only when non-empty) | `server/cmd/multica/cmd_issue.go:761-762` |
| Create body maps flag → `body["git_base_branch"]` (only when non-empty) | `server/cmd/multica/cmd_issue.go:764-765` |
| Update body sends flag via `cmd.Flags().Changed` (empty = clear) | `server/cmd/multica/cmd_issue.go:956-962` |
| CreateIssueRequest.GitWorkBranch / GitBaseBranch (JSON tags `git_work_branch` / `git_base_branch`) | `server/internal/handler/issue.go:2120-2121` |
| UpdateIssueRequest.GitWorkBranch / GitBaseBranch (nullable) | `server/internal/handler/issue.go:2626-2627` |
| `validateBranchName` helper (rules: 200 chars, allowed `A-Za-z0-9._/-`, no leading `-`, no `..`, no `@{`, not `HEAD`, work branch not `main`/`master`) | `server/internal/handler/issue.go:111-135` |
| `branchNameRe` (the character class) | `server/internal/handler/issue.go:90` |
| Create handler — validate each field, run multi-repo guard, run uniqueness check | `server/internal/handler/issue.go:2248-2309` |
| Update handler — same validation path, plus cross-field work != base | `server/internal/handler/issue.go:2633-2714` |
| Create handler — render 409 with `git_work_branch_in_use` code on `service.ErrGitWorkBranchConflict` (race path) | `server/internal/handler/issue.go:2405-2418` |
| IssueService.Create — pre-check `FindActiveIssueByWorkBranch` inside the create tx (the authoritative race guard) | `server/internal/service/issue.go:228-236` |
| `ErrGitWorkBranchConflict` sentinel | `server/internal/service/issue.go:141` |
| sqlc query `FindActiveIssueByWorkBranch` (workspace-scoped, non-terminal only) | `server/pkg/db/queries/issue.sql:140-149` |
| sqlc query `CountGithubRepoResourcesForProject` (multi-repo guard) | `server/pkg/db/queries/project_resource.sql:46-52` |
| DB partial unique index `issue_git_work_branch_active_uidx` (last-resort safety net for races) | `server/migrations/122_issue_git_branches.up.sql:26-29` |
| DB columns `issue.git_work_branch` / `issue.git_base_branch` (TEXT, CHECK length ≤ 200) | `server/migrations/122_issue_git_branches.up.sql:13-16` |
| Generated `db.Issue.GitWorkBranch` / `GitBaseBranch` (pgtype.Text) | `server/pkg/db/generated/models.go:394-395` |
| Generated `FindActiveIssueByWorkBranch` Go method | `server/pkg/db/generated/issue.sql.go:491-499` |
| Generated `CountGithubRepoResourcesForProject` Go method | `server/pkg/db/generated/project_resource.sql.go:13-23` |
| Daemon brief section `## Git Branch` (loud must-follow) | `server/internal/daemon/execenv/runtime_config.go:578-606` |

The create and update paths share the same `validateBranchName` rules
(handler/issue.go:111). The cross-field check (work != base when both
are set) is computed in the handler, not the service, because the
service only sees the GitWorkBranch / GitBaseBranch fields one at a
time and has no easy way to know whether the caller meant the existing
row's value or the new one.

The unique-index contract: `issue_git_work_branch_active_uidx` is a
PARTIAL unique index scoped to `status NOT IN ('done', 'cancelled')`,
so a closed issue releases its work-branch slot and a follow-up issue
can reuse the name. The handler's pre-check + service's in-tx check
both come ahead of the index so concurrent creates get a structured
409 instead of a Postgres constraint violation bubbling up as a 500.

The multi-repo guard: `CountGithubRepoResourcesForProject` returns
> 1 → 422 with `"cannot set git_work_branch or git_base_branch when
multiple github_repo resources are bound to the project"`. 0 or 1
github_repo resources permit the fields. The per-repo-id disambiguation
syntax (`git_work_branch:<repo-id>`) is explicitly out of scope for
MUL-44 — see issue body.

## `multica issue pull-requests` — read PR links from Multica

| Behavior | File:line | Drifted from |
|---|---|---|
| CLI command `pull-requests <id>` (alias `prs`) | `server/cmd/multica/cmd_issue.go:105` | `:104` |
| `runIssuePullRequests` handler | `server/cmd/multica/cmd_issue.go:507` | new citation |
| Calls `GET /api/issues/<id>/pull-requests` | `server/cmd/multica/cmd_issue.go:522` | `:522` (unchanged) |
| API route registration | `server/cmd/server/router.go:480` | `:480` (unchanged) |
| Handler `ListPullRequestsForIssue` → `Queries.ListPullRequestsByIssue` | `server/internal/handler/github.go:466,471` | `:466` (unchanged) |
| Row → response mapper `issuePullRequestRowToResponse` | `server/internal/handler/github.go:149` | new citation |

The CLI resolves the issue ref, GETs the endpoint, and (for `--output json`)
prints the raw `{"pull_requests": [...]}` body. Only `--output` is accepted; the
default `table` shows `NUMBER STATE TITLE URL`.

## PR response shape

`GitHubPullRequestResponse` struct: `server/internal/handler/github.go:51`. JSON
fields the agent can read off each element of `pull_requests`:

- `number` (`json:"number"`, line 56)
- `html_url` (`json:"html_url"`, line 59)
- `title` (`json:"title"`, line 57)
- `state` (`json:"state"`, line 58) — the folded lifecycle enum (see below)
- `merged_at` (`json:"merged_at"`, line 63), `closed_at` (line 64)
- `mergeable_state` (`json:"mergeable_state"`, line 70) — mirrors GitHub; UI only
  surfaces `clean`/`dirty`, other values round-trip as unknown
- `checks_conclusion` (`json:"checks_conclusion"`, line 74) — aggregated
  `"passed"`/`"failed"`/`"pending"` or `null` (no observed suite)
- `checks_passed` / `checks_failed` / `checks_pending` (lines 78-80) — per-suite
  counts; `aggregateChecksConclusion` (line 183) folds them into
  `checks_conclusion`

There is **no** standalone `draft` or `merged` boolean in the response. The
PR lifecycle is encoded in the single `state` string by `derivePRState`
(`server/internal/handler/github.go:994`):

```
merged   → if PullRequest.Merged
closed   → else if PullRequest.State == "closed"
draft    → else if PullRequest.Draft
open     → otherwise
```

`derivePRState` is called when the webhook upserts the row
(`server/internal/handler/github.go:682`), so `state` is what the list endpoint
returns. "Is it merged?" = `state == "merged"` (or `merged_at != null`); "is it a
draft?" = `state == "draft"`. Combine with `checks_conclusion` for CI status.

## Two distinct webhook paths: link vs close-intent

Both run inside the `pull_request` webhook handler, gated by the workspace
auto-link flag (`workspaceAutoLinkPRsEnabled`, `github.go:1074`).

### Path 1 — link (title OR body OR branch)

- `extractIdentifiers` regex helper: `server/internal/handler/github.go:1028`
- driving regex `identifierRe` (`\b([a-z][a-z0-9]{1,9})-(\d+)\b`, case-insensitive):
  `server/internal/handler/github.go:490`
- call site: `server/internal/handler/github.go:727` —
  `extractIdentifiers(p.PullRequest.Title, p.PullRequest.Body, p.PullRequest.Head.Ref)`

Every `PREFIX-NUMBER` mention in **title, body, or branch** resolves to an issue
in the workspace and writes a link row (`LinkIssueToPullRequest`, ~`github.go:762`).
This is what `multica issue pull-requests` later reads back.

Drifted from the prior skill's `github.go:727` citation, which pointed at the old
call-site location for the link logic.

### Path 2 — close intent (title OR body only, keyword-adjacent)

- `extractClosingIdentifiers` regex helper: `server/internal/handler/github.go:1051`
- driving regex `closingIdentifierRe`
  (`\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)[:\s]+([a-z][a-z0-9]{1,9})-(\d+)\b`):
  `server/internal/handler/github.go:501`
- call site: `server/internal/handler/github.go:736` —
  `extractClosingIdentifiers(p.PullRequest.Title, p.PullRequest.Body)` (no branch arg)

Only a `PREFIX-NUMBER` immediately after a closing keyword
(`Closes`/`Fixes`/`Resolves`, optional `:` then whitespace) sets the link row's
`close_intent` flag — the gate that auto-advances the issue to `done` on merge.
`Fix MUL-1` closes; `Fix login MUL-1` does not (adjacency). Branch names are
deliberately excluded (function doc, `github.go:1044-1050`): a branch like
`mul-1/fix-login` links but must never declare close intent.

Drifted from the prior skill's `github.go:736` citation.

Net: a bare title prefix (`MUL-2759: ...`) or a branch ref links only;
`Closes MUL-2759` links **and** records close intent.

## Status side effects (enqueue contracts)

| Behavior | File:line | Drifted from |
|---|---|---|
| Create-time: agent-assigned, non-backlog issue enqueues immediately | `server/internal/handler/issue.go:2263-2264` | new citation |
| `shouldEnqueueAgentTask` returns false for `backlog` (parking lot) | `server/internal/handler/issue.go:2644-2648` | new citation |
| Backlog → non-backlog (not done/cancelled) enqueues on update | `server/internal/handler/issue.go:2537-2540` | `:2523` |
| Same contract in batch update | `server/internal/handler/issue.go:3021-3024` | new citation |
| Child → `done` posts a system comment on the parent | `server/internal/handler/issue_child_done.go:51` (`notifyParentOfChildDone`; doc comment at `:15`) | func def `:51` |

Creation with `--status todo` (or any non-backlog status) on an agent-assigned
issue fires the agent immediately; `--status backlog` parks it with the assignee
set but no trigger. Promoting `backlog → todo` later fires it then (update path,
line 2537).

## Metadata CLI

| Behavior | File:line |
|---|---|
| `multica issue metadata set <issue-id> --key --value [--type]` | `server/cmd/multica/cmd_issue_metadata.go:80,109-111` |
| `multica issue metadata delete <issue-id> --key` | `server/cmd/multica/cmd_issue_metadata.go:93,113` |
| API routes (PUT/DELETE `/metadata/{key}`) | `server/cmd/server/router.go:478-479` |

`--value` is JSON-parsed by default (bool/number sniff); `--type` forces
`string`/`number`/`bool`.

## Verification command

Re-derive any line above before depending on it:

```bash
cd server
grep -n 'pull-requests <id>'                 cmd/multica/cmd_issue.go
grep -n 'ListPullRequestsForIssue'           cmd/server/router.go internal/handler/github.go
grep -n 'func issuePullRequestRowToResponse\|type GitHubPullRequestResponse struct\|func derivePRState\|func extractIdentifiers\|func extractClosingIdentifiers\|closingIdentifierRe' internal/handler/github.go
grep -n 'extractIdentifiers(\|extractClosingIdentifiers(\|derivePRState(' internal/handler/github.go
grep -n 'prevIssue.Status == "backlog"\|func (h \*Handler) shouldEnqueueAgentTask' internal/handler/issue.go
grep -n 'func notifyParentOfChildDone'       internal/handler/issue_child_done.go

# MUL-44 git branch pin lines:
grep -n '"git-work-branch"\|"git-base-branch"' cmd/multica/cmd_issue.go
grep -n 'func validateBranchName\|var branchNameRe' internal/handler/issue.go
grep -n 'ErrGitWorkBranchConflict'           internal/service/issue.go
grep -n 'FindActiveIssueByWorkBranch\|CountGithubRepoResourcesForProject' pkg/db/queries/issue.sql pkg/db/queries/project_resource.sql
grep -n 'GitWorkBranch:\|GitBaseBranch:'     pkg/db/generated/models.go pkg/db/generated/issue.sql.go pkg/db/generated/project_resource.sql.go
grep -n '## Git Branch'                      internal/daemon/execenv/runtime_config.go
```
