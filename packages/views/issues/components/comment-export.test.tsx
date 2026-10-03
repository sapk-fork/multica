import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { useModalStore } from "@multica/core/modals";
import type { TimelineEntry } from "@multica/core/types";

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));
vi.mock("modern-screenshot", () => ({ domToBlob }));

const { downloadBlob } = vi.hoisted(() => ({ downloadBlob: vi.fn() }));
vi.mock("../../editor/utils/mermaid-export", () => ({ downloadBlob }));

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Ada" }),
}));

vi.mock("../../i18n", async () => {
  const issues = (await import("../../locales/en/issues.json")).default;
  return {
    useLocale: () => "en",
    useT: () => ({ t: (select: (bundle: typeof issues) => string) => select(issues) }),
  };
});

vi.mock("../../editor", async () => ({
  ...(await vi.importActual<typeof import("../../editor/use-upload-gate")>("../../editor/use-upload-gate")),
  ReadonlyContent: ({ content }: { content: string }) => <div>{content}</div>,
}));

import { CommentExportHost } from "./comment-export-host";
import { exportCommentImage } from "./comment-export";

function comment(id: string, parentId: string | null): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: "member",
    actor_id: "user-1",
    content: `body ${id}`,
    parent_id: parentId,
    comment_type: "comment",
    reactions: [],
    attachments: [],
    created_at: "2026-09-11T07:00:00Z",
    updated_at: "2026-09-11T07:00:00Z",
    revision: 1,
  };
}

beforeEach(() => {
  useModalStore.setState({ modal: null, data: null });
  domToBlob.mockReset();
  domToBlob.mockResolvedValue(new Blob(["png"], { type: "image/png" }));
  downloadBlob.mockReset();
  toastError.mockClear();
});

afterEach(() => {
  useModalStore.setState({ modal: null, data: null });
});

describe("exportCommentImage", () => {
  it("asks the app-level host for the comment it was handed", () => {
    exportCommentImage(comment("reply", "root"));

    expect(useModalStore.getState().modal).toBe("comment-image-export");
    expect(useModalStore.getState().data?.entry).toMatchObject({ id: "reply" });
  });

  it("renders nothing on its own — the request outlives the card that made it", () => {
    // The whole point: the opener is a virtualized row, and the surface must
    // not be anchored to it.
    exportCommentImage(comment("root", null));

    expect(document.querySelector(".comment-print-portal")).toBeNull();
  });
});

describe("CommentExportHost", () => {
  it("captures and downloads the requested comment", async () => {
    render(<CommentExportHost />);
    exportCommentImage(comment("root", null));

    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    expect(domToBlob.mock.calls[0]![0]?.textContent).toContain("body root");
    await waitFor(() =>
      expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), "comment-2026-09-11-0700.png"),
    );
  });

  it("closes the request when the export is done, taking the surface with it", async () => {
    render(<CommentExportHost />);
    exportCommentImage(comment("root", null));

    await waitFor(() => expect(downloadBlob).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(document.querySelector(".comment-print-portal")).toBeNull());
    expect(useModalStore.getState().modal).toBeNull();
  });

  it("closes the request after a failed capture, so the app is not left under a sheet", async () => {
    domToBlob.mockRejectedValue(new Error("canvas too large"));
    render(<CommentExportHost />);
    exportCommentImage(comment("root", null));

    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    expect(downloadBlob).not.toHaveBeenCalled();
    await waitFor(() => expect(document.querySelector(".comment-print-portal")).toBeNull());
  });

  it("shows nothing while no comment is being exported", () => {
    render(<CommentExportHost />);

    expect(document.querySelector(".comment-print-portal")).toBeNull();
    expect(domToBlob).not.toHaveBeenCalled();
  });
});
