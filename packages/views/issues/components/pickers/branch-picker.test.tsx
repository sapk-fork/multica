import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enIssues from "../../../locales/en/issues.json";
import { BranchPicker } from "./branch-picker";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

function renderPicker(props: Partial<React.ComponentProps<typeof BranchPicker>> = {}) {
  const onUpdate = vi.fn();
  render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <BranchPicker
        field="git_work_branch"
        value={null}
        onUpdate={onUpdate}
        defaultOpen
        {...props}
      />
    </I18nProvider>,
  );
  return { onUpdate };
}

describe("BranchPicker", () => {
  it("saves a valid branch name on Apply", () => {
    const { onUpdate } = renderPicker();
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "feature/m-44" } });
    fireEvent.click(screen.getByRole("button", { name: /apply/i }));
    expect(onUpdate).toHaveBeenCalledWith({ git_work_branch: "feature/m-44" });
  });

  it("trims surrounding whitespace before saving", () => {
    const { onUpdate } = renderPicker();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "  feature/x  " } });
    fireEvent.click(screen.getByRole("button", { name: /apply/i }));
    expect(onUpdate).toHaveBeenCalledWith({ git_work_branch: "feature/x" });
  });

  it("blocks save and shows an inline error for invalid input", () => {
    const { onUpdate } = renderPicker();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "bad branch" } });
    // Error is surfaced...
    expect(screen.getByRole("alert")).toBeInTheDocument();
    // ...and Apply does not call onUpdate.
    fireEvent.click(screen.getByRole("button", { name: /apply/i }));
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it("rejects 'main' for the work branch", () => {
    const { onUpdate } = renderPicker({ field: "git_work_branch" });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "main" } });
    expect(screen.getByRole("alert")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /apply/i }));
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it("accepts 'main' for the base branch", () => {
    const { onUpdate } = renderPicker({ field: "git_base_branch", value: null });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "main" } });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /apply/i }));
    expect(onUpdate).toHaveBeenCalledWith({ git_base_branch: "main" });
  });

  it("clears the field via Clear when a value is already set", () => {
    const { onUpdate } = renderPicker({ value: "feature/old" });
    fireEvent.click(screen.getByRole("button", { name: /clear/i }));
    expect(onUpdate).toHaveBeenCalledWith({ git_work_branch: null });
  });

  it("does not offer Clear when the field is unset", () => {
    renderPicker({ value: null });
    expect(screen.queryByRole("button", { name: /clear/i })).not.toBeInTheDocument();
  });

  it("saves on Enter", () => {
    const { onUpdate } = renderPicker();
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "feature/enter" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onUpdate).toHaveBeenCalledWith({ git_work_branch: "feature/enter" });
  });
});
