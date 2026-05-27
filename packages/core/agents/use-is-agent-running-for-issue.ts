"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentTaskSnapshotOptions } from "./queries";

export function useIsAgentRunningForIssue(
  wsId: string,
  issueId: string,
): boolean {
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  return useMemo(() => {
    return snapshot.some(
      (task) => task.issue_id === issueId && task.status === "running",
    );
  }, [snapshot, issueId]);
}
