// Client-side git branch-name validation. A faithful TS port of the server's
// `validateBranchName` (server/internal/handler/issue.go) so the issue-detail
// branch editors can block obviously-invalid input before the round-trip.
//
// The SERVER REMAINS AUTHORITATIVE: it additionally enforces the cross-field
// rule (work !== base), the multi-repo guard, and work-branch uniqueness —
// none of which can be decided from a single field value on the client. Those
// rejections still surface through the normal update error toast. This util
// only covers the per-field shape rules, which are cheap and need no server.

/** The two issue-level branch-pin fields (matches the API JSON keys). */
export type BranchField = "git_work_branch" | "git_base_branch";

/**
 * Stable error codes returned by {@link validateBranchName}. The UI maps each
 * to a localized message; keeping the util i18n-free keeps it pure/testable.
 */
export type BranchNameError =
  | "too_long"
  | "invalid_chars"
  | "leading_dash"
  | "dotdot"
  | "at_brace"
  | "head"
  | "work_integration";

// Allowed character set — mirrors the server's branchNameRe
// (git's check-ref-format allowed set minus the wildcard glob markers).
const BRANCH_NAME_RE = /^[A-Za-z0-9._/-]+$/;

const MAX_LEN = 200;

/**
 * Validate a branch-name value for the given field. Returns `null` when valid
 * (empty is valid — it clears the optional field), or a {@link BranchNameError}
 * code otherwise.
 *
 * Rules (identical to the server):
 *   - empty allowed (field is optional / clearing)
 *   - max 200 chars
 *   - allowed chars: A-Za-z0-9._/-
 *   - no leading '-'
 *   - no '..' substring
 *   - no '@{' substring
 *   - not 'HEAD'
 *   - git_work_branch must not be 'main' or 'master' (integration branches)
 */
export function validateBranchName(
  field: BranchField,
  value: string,
): BranchNameError | null {
  if (value === "") return null;
  if (value.length > MAX_LEN) return "too_long";
  if (!BRANCH_NAME_RE.test(value)) return "invalid_chars";
  if (value.startsWith("-")) return "leading_dash";
  if (value.includes("..")) return "dotdot";
  if (value.includes("@{")) return "at_brace";
  if (value === "HEAD") return "head";
  if (field === "git_work_branch" && (value === "main" || value === "master")) {
    return "work_integration";
  }
  return null;
}
