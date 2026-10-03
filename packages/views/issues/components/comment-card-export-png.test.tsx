import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { forwardRef, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TimelineEntry } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

// The export must capture the comment whose menu was opened — a reply's own
// body, never the thread root's — and must leave nothing on the page behind it.

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));
vi.mock("modern-screenshot", () => ({ domToBlob }));

const { downloadBlob } = vi.hoisted(() => ({ downloadBlob: vi.fn() }));
vi.mock("../../editor/utils/mermaid-export", () => ({ downloadBlob }));

vi.mock("@multica/core/api", () => ({
  api: { uploadFile: vi.fn() },
  dispatchReasonCode: () => undefined,
  errorCode: () => undefined,
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    pathname: "/acme/issues",
    getShareableUrl: (p: string) => `https://app.example${p}`,
  }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Ada" }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("../hooks/use-comment-trigger-preview", () => ({
  useCommentTriggerPreview: () => ({ agents: [], blocked: [] }),
}));

vi.mock("../../editor", async () => ({
  ...(await vi.importActual<typeof import("../../editor/use-upload-gate")>("../../editor/use-upload-gate")),
  ...(await vi.importActual<typeof import("../../editor/use-lazy-editor")>("../../editor/use-lazy-editor")),
  ...(await vi.importActual<typeof import("../../editor/use-composer-submit")>("../../editor/use-composer-submit")),
  useEditorUpload: () => ({ uploadWithToast: vi.fn(), upload: vi.fn(), uploading: false }),
  useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
  FileDropOverlay: () => null,
  ReadonlyContent: ({ content }: { content: string }) => <div>{content}</div>,
  Attachment: () => null,
  AttachmentDownloadProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
  ContentEditor: forwardRef(function MockContentEditor() {
    return <textarea data-testid="editor" />;
  }),
}));

import { CommentCard } from "./comment-card";

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

function renderThread(root: TimelineEntry, replies: TimelineEntry[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <CommentCard
        issueId="issue-1"
        entry={root}
        replies={replies}
        currentUserId="user-1"
        onReply={vi.fn().mockResolvedValue(true)}
        onEdit={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn()}
        onToggleReaction={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

/** What each capture actually saw on the page, in call order. */
let captured: string[];

beforeEach(() => {
  captured = [];
  domToBlob.mockReset();
  domToBlob.mockImplementation(async () => {
    captured.push(document.querySelector(".comment-print-doc")?.textContent ?? "");
    return new Blob(["png"], { type: "image/png" });
  });
  downloadBlob.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

const actionMenus = () => screen.getAllByRole("button", { name: "Comment actions" });

async function exportFrom(index: number) {
  fireEvent.click(actionMenus()[index]!);
  fireEvent.click(await screen.findByText("Download PNG"));
}

const portal = () => document.querySelector(".comment-print-portal");

describe("CommentCard — download comment as PNG", () => {
  it("captures the thread root's own body", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);

    await waitFor(() => expect(captured).toHaveLength(1));
    expect(captured[0]).toContain("body root");
  });

  it("captures a reply's own body, not the thread root's", async () => {
    renderThread(comment("root", null), [comment("reply", "root")]);

    // Menus render in order: root first, then the reply.
    await exportFrom(1);

    await waitFor(() => expect(captured).toHaveLength(1));
    expect(captured[0]).toContain("body reply");
    expect(captured[0]).not.toContain("body root");
  });

  it("keeps the surface up until the capture has landed", async () => {
    let land: (blob: Blob) => void = () => {};
    domToBlob.mockImplementation(
      () =>
        new Promise<Blob>((resolve) => {
          land = resolve;
        }),
    );
    renderThread(comment("root", null), []);

    await exportFrom(0);
    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));

    // Capturing, nothing downloaded yet.
    expect(portal()).not.toBeNull();
    expect(portal()?.textContent).toContain("body root");
    expect(downloadBlob).not.toHaveBeenCalled();

    land(new Blob(["png"], { type: "image/png" }));

    await waitFor(() => expect(portal()).toBeNull());
    expect(downloadBlob).toHaveBeenCalledTimes(1);
  });

  it("captures exactly once per click", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);

    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    // Past the readiness wait: a second capture here means the surface re-ran
    // its export effect.
    await new Promise((resolve) => setTimeout(resolve, 120));
    expect(domToBlob).toHaveBeenCalledTimes(1);
    expect(captured).toHaveLength(1);
  });

  it("lets a reader export again after the first image is on its way", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);
    await waitFor(() => expect(portal()).toBeNull());

    await exportFrom(0);

    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(2));
    expect(captured).toHaveLength(2);
    expect(downloadBlob).toHaveBeenCalledTimes(2);
  });
});
