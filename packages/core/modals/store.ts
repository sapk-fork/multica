"use client";

import { create } from "zustand";

type ModalType =
  | "create-issue"
  | "quick-create-issue"
  | "create-project"
  | "create-squad"
  | "feedback"
  | "issue-set-parent"
  | "issue-mark-duplicate"
  | "issue-add-child"
  | "issue-delete-confirm"
  | "issue-run-confirm"
  // The comment image export (M-125) is an overlay rather than a dialog, but it
  // is the same idea: one app-level surface, opened from anywhere, hosted once.
  | "comment-image-export"
  | null;

export type IssueLimitRecoveryReason = "issue_limit" | "autopilot_quota";

interface ModalStore {
  modal: ModalType;
  data: Record<string, unknown> | null;
  issueLimitRecoveryWorkspaceId: string | null;
  issueLimitRecoveryReason: IssueLimitRecoveryReason;
  open: (modal: NonNullable<ModalType>, data?: Record<string, unknown> | null) => void;
  close: () => void;
  showIssueLimitRecovery: (
    workspaceId: string,
    reason?: IssueLimitRecoveryReason,
  ) => void;
  dismissIssueLimitRecovery: () => void;
}

export const useModalStore = create<ModalStore>((set) => ({
  modal: null,
  data: null,
  issueLimitRecoveryWorkspaceId: null,
  issueLimitRecoveryReason: "issue_limit",
  open: (modal, data = null) => set({ modal, data }),
  close: () => set({ modal: null, data: null }),
  showIssueLimitRecovery: (workspaceId, reason = "issue_limit") =>
    set({
      issueLimitRecoveryWorkspaceId: workspaceId,
      issueLimitRecoveryReason: reason,
    }),
  dismissIssueLimitRecovery: () =>
    set({
      issueLimitRecoveryWorkspaceId: null,
      issueLimitRecoveryReason: "issue_limit",
    }),
}));
