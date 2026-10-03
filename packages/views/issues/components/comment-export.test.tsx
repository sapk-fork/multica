import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
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

import { exportCommentImage } from "./comment-export";

const entry: TimelineEntry = {
  type: "comment",
  id: "root",
  actor_type: "member",
  actor_id: "user-1",
  content: "body root",
  parent_id: null,
  comment_type: "comment",
  reactions: [],
  attachments: [],
  created_at: "2026-09-11T07:00:00Z",
  updated_at: "2026-09-11T07:00:00Z",
  revision: 1,
};

beforeEach(() => {
  domToBlob.mockReset();
  domToBlob.mockResolvedValue(new Blob(["png"], { type: "image/png" }));
  downloadBlob.mockReset();
  toastError.mockClear();
});

afterEach(() => {
  document.querySelectorAll(".comment-print-portal").forEach((node) => node.remove());
  document.body.innerHTML = "";
});

describe("exportCommentImage", () => {
  it("captures the comment it was handed, and downloads it", async () => {
    exportCommentImage(entry);

    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    expect(domToBlob.mock.calls[0]![0]?.textContent).toContain("body root");
    await waitFor(() =>
      expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), "comment-2026-09-11-0700.png"),
    );
  });

  it("mounts its host on the document, then takes it away again", async () => {
    exportCommentImage(entry);
    expect(document.querySelector("[data-comment-export-root]")).not.toBeNull();

    // Wait for the export itself first: asserting cleanup while the surface has
    // not even rendered would pass on a component that never cleans up.
    await waitFor(() => expect(downloadBlob).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(document.querySelector(".comment-print-portal")).toBeNull());
    expect(document.querySelector("[data-comment-export-root]")).toBeNull();
  });

  it("cleans up after a failed capture too, so the app is not left under a sheet", async () => {
    domToBlob.mockRejectedValue(new Error("canvas too large"));

    exportCommentImage(entry);

    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(document.querySelector(".comment-print-portal")).toBeNull());
    expect(downloadBlob).not.toHaveBeenCalled();
    expect(document.querySelector("[data-comment-export-root]")).toBeNull();
  });

  it("exports on demand rather than on render: a fresh call captures again", async () => {
    exportCommentImage(entry);
    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));

    exportCommentImage(entry);
    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(2));
  });
});