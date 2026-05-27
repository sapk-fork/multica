/**
 * @vitest-environment jsdom
 */

// useIsAgentRunningForIssue — returns true when a task for the given issueId has status === "running"
import { createElement } from "react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { AgentTask } from "../types/agent";
import { agentTaskSnapshotKeys } from "./queries";
import { useIsAgentRunningForIssue } from "./use-is-agent-running-for-issue";

function makeTask(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "rt-1",
    issue_id: "issue-1",
    status: "running",
    priority: 0,
    dispatched_at: "2026-01-01T00:00:00Z",
    started_at: "2026-01-01T00:00:00Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: qc }, children);
  };
}

function createQcWithSnapshot(wsId: string, tasks: AgentTask[]): QueryClient {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  qc.setQueryData(agentTaskSnapshotKeys.list(wsId), tasks);
  return qc;
}

describe("useIsAgentRunningForIssue", () => {
  // Happy path: task for the issue is running
  it("returns true when a task for the given issueId has status === 'running'", () => {
    const qc = createQcWithSnapshot("ws-1", [makeTask({ issue_id: "issue-42", status: "running" })]);
    const { result } = renderHook(
      () => useIsAgentRunningForIssue("ws-1", "issue-42"),
      { wrapper: createWrapper(qc) },
    );
    expect(result.current).toBe(true);
  });

  // Edge: task exists but status is not running
  it("returns false when a task for the issueId exists but is not running", () => {
    const qc = createQcWithSnapshot("ws-1", [
      makeTask({ issue_id: "issue-42", status: "completed" }),
      makeTask({ id: "t2", issue_id: "issue-42", status: "queued" }),
      makeTask({ id: "t3", issue_id: "issue-42", status: "failed" }),
    ]);
    const { result } = renderHook(
      () => useIsAgentRunningForIssue("ws-1", "issue-42"),
      { wrapper: createWrapper(qc) },
    );
    expect(result.current).toBe(false);
  });

  // Regression: running task for a different issue must not bleed into the queried issueId
  it("returns false when the running task belongs to a different issueId (no cross-issue bleed)", () => {
    const qc = createQcWithSnapshot("ws-1", [makeTask({ issue_id: "other-issue", status: "running" })]);
    const { result } = renderHook(
      () => useIsAgentRunningForIssue("ws-1", "issue-42"),
      { wrapper: createWrapper(qc) },
    );
    expect(result.current).toBe(false);
  });
});
