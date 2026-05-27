// StatusIconWithActivity — renders StatusIcon in a rounded-full span; animates when agent is running
import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";

// Mock useWorkspaceId so the component doesn't need a WorkspaceIdProvider in tests.
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// Control the hook response per test rather than wiring up a real QueryClient.
const mockUseIsAgentRunning = vi.hoisted(() => vi.fn<() => boolean>(() => false));
vi.mock("@multica/core/agents", () => ({
  useIsAgentRunningForIssue: mockUseIsAgentRunning,
}));

// Render the real StatusIcon — it is pure SVG with no external deps.
import { StatusIconWithActivity } from "./status-icon-with-activity";

describe("StatusIconWithActivity", () => {
  // Happy path: renders StatusIcon inside a rounded-full span
  it("renders an SVG status icon inside a rounded-full wrapper span", () => {
    mockUseIsAgentRunning.mockReturnValue(false);
    const { container } = render(
      <StatusIconWithActivity issueId="issue-1" status="in_progress" />,
    );

    const span = container.querySelector("span");
    expect(span).toBeTruthy();
    expect(span?.className).toContain("rounded-full");

    const svg = span?.querySelector("svg");
    expect(svg).toBeTruthy();
  });

  // Happy path: animation class is applied when agent is running
  it("applies animate-status-agent-ring class when useIsAgentRunningForIssue returns true", () => {
    mockUseIsAgentRunning.mockReturnValue(true);
    const { container } = render(
      <StatusIconWithActivity issueId="issue-2" status="todo" />,
    );

    const span = container.querySelector("span");
    expect(span?.className).toContain("animate-status-agent-ring");
  });

  // Regression: animation class must not appear when agent is not running (MUL-8)
  it("does not apply animate-status-agent-ring when useIsAgentRunningForIssue returns false", () => {
    mockUseIsAgentRunning.mockReturnValue(false);
    const { container } = render(
      <StatusIconWithActivity issueId="issue-3" status="done" />,
    );

    const span = container.querySelector("span");
    expect(span?.className).not.toContain("animate-status-agent-ring");
  });
});
