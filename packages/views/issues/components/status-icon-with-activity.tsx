"use client";

import { memo } from "react";
import type { IssueStatus } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIsAgentRunningForIssue } from "@multica/core/agents";
import { StatusIcon } from "./status-icon";
import { cn } from "@multica/ui/lib/utils";

interface StatusIconWithActivityProps {
  issueId: string;
  status: IssueStatus;
  className?: string;
  inheritColor?: boolean;
}

export const StatusIconWithActivity = memo(function StatusIconWithActivity({
  issueId,
  status,
  className = "h-4 w-4",
  inheritColor = false,
}: StatusIconWithActivityProps) {
  const wsId = useWorkspaceId();
  const isRunning = useIsAgentRunningForIssue(wsId, issueId);

  return (
    <span
      className={cn(
        "inline-flex rounded-full",
        isRunning && "animate-status-agent-ring",
      )}
    >
      <StatusIcon status={status} className={className} inheritColor={inheritColor} />
    </span>
  );
});
