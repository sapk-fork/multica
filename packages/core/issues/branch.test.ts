import { describe, it, expect } from "vitest";
import { validateBranchName, type BranchField } from "./branch";

// Client-side mirror of the server's validateBranchName
// (server/internal/handler/issue.go). The server stays authoritative; these
// tests lock the client port to the same rules so invalid input is blocked
// before the round-trip.
describe("validateBranchName", () => {
  const fields: BranchField[] = ["git_work_branch", "git_base_branch"];

  it("treats empty as valid (clearing the field)", () => {
    for (const f of fields) expect(validateBranchName(f, "")).toBeNull();
  });

  it("accepts ordinary branch names", () => {
    for (const f of fields) {
      expect(validateBranchName(f, "feature/m-44-git-branches")).toBeNull();
      expect(validateBranchName(f, "fix_123")).toBeNull();
      expect(validateBranchName(f, "release-2.1.0")).toBeNull();
    }
  });

  it("rejects names longer than 200 chars", () => {
    const long = "a".repeat(201);
    expect(validateBranchName("git_work_branch", long)).toBe("too_long");
  });

  it("accepts a name of exactly 200 chars", () => {
    expect(validateBranchName("git_work_branch", "a".repeat(200))).toBeNull();
  });

  it("rejects disallowed characters", () => {
    expect(validateBranchName("git_work_branch", "has space")).toBe("invalid_chars");
    expect(validateBranchName("git_work_branch", "tilde~1")).toBe("invalid_chars");
    expect(validateBranchName("git_work_branch", "caret^")).toBe("invalid_chars");
    expect(validateBranchName("git_work_branch", "colon:x")).toBe("invalid_chars");
    expect(validateBranchName("git_base_branch", "star*")).toBe("invalid_chars");
  });

  it("rejects a leading dash", () => {
    expect(validateBranchName("git_work_branch", "-leading")).toBe("leading_dash");
  });

  it("rejects '..' anywhere", () => {
    expect(validateBranchName("git_work_branch", "a..b")).toBe("dotdot");
  });

  it("rejects '@{' (via the charset check, which runs first — '@' and '{' are not allowed)", () => {
    // Mirrors the server: the charset regex rejects '@' and '{' before the
    // dedicated '@{' check is ever reached, so the surfaced code is
    // invalid_chars. The at_brace branch is kept only for parity with the
    // server's rule list.
    expect(validateBranchName("git_work_branch", "ref@{0}")).toBe("invalid_chars");
  });

  it("rejects HEAD for both fields", () => {
    for (const f of fields) expect(validateBranchName(f, "HEAD")).toBe("head");
  });

  it("rejects main/master for the work branch only", () => {
    expect(validateBranchName("git_work_branch", "main")).toBe("work_integration");
    expect(validateBranchName("git_work_branch", "master")).toBe("work_integration");
    // base branch may legitimately be main/master
    expect(validateBranchName("git_base_branch", "main")).toBeNull();
    expect(validateBranchName("git_base_branch", "master")).toBeNull();
  });
});
