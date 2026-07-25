// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";
import { HealthCell } from "./runtime-list";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

vi.mock("./shared", () => ({
  HealthIcon: () => null,
  useHealthLabel: () => () => "Online",
}));

function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "rt",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-04-27T12:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function renderHealthCell(runtime: AgentRuntime, now = new Date("2026-04-27T12:00:00Z").getTime()) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <HealthCell
        runtime={runtime}
        workload={{ agentIds: [], runningCount: 0, queuedCount: 0 }}
        now={now}
      />
    </I18nProvider>,
  );
}

describe("runtime list health cell hold state", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows the hold reason and countdown when a runtime is held", () => {
    renderHealthCell(
      makeRuntime({
        hold_until: "2026-04-27T14:00:00Z",
        hold_reason: "session_limit",
      }),
    );

    expect(screen.getByText(/On hold/)).toBeInTheDocument();
    expect(screen.getByText(/Session limit/)).toBeInTheDocument();
    expect(screen.getByText(/Resumes/)).toBeInTheDocument();
  });

  it("falls back to the raw reason when the hold reason is unknown", () => {
    renderHealthCell(
      makeRuntime({
        hold_until: "2026-04-27T14:00:00Z",
        hold_reason: "custom_reason",
      }),
    );

    expect(screen.getByText(/custom_reason/)).toBeInTheDocument();
  });

  it("does not show hold UI when the runtime is not held", () => {
    renderHealthCell(makeRuntime());

    expect(screen.queryByText(/On hold/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Reason:/)).not.toBeInTheDocument();
  });
});
