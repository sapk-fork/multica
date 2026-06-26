"use client";

import { useEffect, useRef, useState } from "react";
import { GitBranch } from "lucide-react";
import type { UpdateIssueRequest } from "@multica/core/types";
import {
  validateBranchName,
  type BranchField,
  type BranchNameError,
} from "@multica/core/issues/branch";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../../i18n";

/**
 * Inline editor for the issue-level `git_work_branch` / `git_base_branch`
 * pins (MUL-44). A branch-name text input with Apply / Clear actions.
 *
 * Validation mirrors the server's `validateBranchName` via the shared
 * `@multica/core/issues/branch` util, so obviously-invalid input is blocked
 * before the round-trip. The SERVER stays authoritative for the rules a
 * single field value can't decide (work !== base, multi-repo guard,
 * work-branch uniqueness); those rejections surface through the normal
 * update error toast wired by `useUpdateIssue`.
 */
export function BranchPicker({
  field,
  value,
  onUpdate,
  align = "start",
  defaultOpen = false,
}: {
  field: BranchField;
  value: string | null;
  onUpdate: (updates: Partial<UpdateIssueRequest>) => void;
  align?: "start" | "center" | "end";
  /** Open the popover on first mount. Used by progressive-disclosure
   *  sidebars so a newly-added field immediately enters edit state. */
  defaultOpen?: boolean;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(defaultOpen);
  const [draft, setDraft] = useState(value ?? "");
  const inputRef = useRef<HTMLInputElement>(null);

  // Re-seed the draft from the current value every time the popover opens, so
  // a reopen after an external change (or a cancelled edit) starts clean.
  useEffect(() => {
    if (open) setDraft(value ?? "");
  }, [open, value]);

  const trimmed = draft.trim();
  const errorCode: BranchNameError | null = validateBranchName(field, trimmed);
  const hasValue = !!value;

  const errorMessage = (code: BranchNameError): string => {
    switch (code) {
      case "too_long":
        return t(($) => $.pickers.branch.errors.too_long);
      case "invalid_chars":
        return t(($) => $.pickers.branch.errors.invalid_chars);
      case "leading_dash":
        return t(($) => $.pickers.branch.errors.leading_dash);
      case "dotdot":
        return t(($) => $.pickers.branch.errors.dotdot);
      case "at_brace":
        return t(($) => $.pickers.branch.errors.at_brace);
      case "head":
        return t(($) => $.pickers.branch.errors.head);
      case "work_integration":
        return t(($) => $.pickers.branch.errors.work_integration);
    }
  };

  const apply = () => {
    if (trimmed === "") {
      // Empty Apply is a clear (only meaningful when something was set).
      if (hasValue) {
        onUpdate({ [field]: null });
        setOpen(false);
      }
      return;
    }
    if (errorCode) return; // blocked; the inline error is already shown
    onUpdate({ [field]: trimmed });
    setOpen(false);
  };

  const clear = () => {
    onUpdate({ [field]: null });
    setOpen(false);
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger className="flex items-center gap-1.5 cursor-pointer rounded px-1 -mx-1 hover:bg-accent/30 transition-colors">
        <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        {value ? (
          <span className="truncate font-mono text-xs">{value}</span>
        ) : (
          <span className="text-muted-foreground">
            {t(($) => $.pickers.branch.empty_label)}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent className="w-64 p-2" align={align}>
        <div className="space-y-2">
          <Input
            ref={inputRef}
            autoFocus
            value={draft}
            spellCheck={false}
            placeholder={
              field === "git_work_branch"
                ? t(($) => $.pickers.branch.work_placeholder)
                : t(($) => $.pickers.branch.base_placeholder)
            }
            aria-label={
              field === "git_work_branch"
                ? t(($) => $.pickers.branch.work_aria)
                : t(($) => $.pickers.branch.base_aria)
            }
            aria-invalid={trimmed !== "" && !!errorCode}
            className="h-8 font-mono text-xs"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                apply();
              }
            }}
          />
          {trimmed !== "" && errorCode && (
            <p role="alert" className="text-xs text-destructive">
              {errorMessage(errorCode)}
            </p>
          )}
          <div className="flex items-center justify-between gap-2">
            <Button
              size="xs"
              onClick={apply}
              disabled={trimmed !== "" && !!errorCode}
            >
              {t(($) => $.pickers.branch.apply_action)}
            </Button>
            {hasValue && (
              <Button
                variant="ghost"
                size="xs"
                onClick={clear}
                className="text-muted-foreground hover:text-foreground"
              >
                {t(($) => $.pickers.branch.clear_action)}
              </Button>
            )}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
