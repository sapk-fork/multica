import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { forwardRef, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TimelineEntry } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

// The export must print the comment whose menu was opened — a reply's own body,
// never the thread root's — and must leave nothing on the page behind it.

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

let printed: string[];
let realPrint: unknown;

beforeEach(() => {
  printed = [];
  realPrint = window.print;
  Object.defineProperty(window, "print", {
    configurable: true,
    value: vi.fn(() => {
      printed.push(document.querySelector(".comment-print-doc")?.textContent ?? "");
    }),
  });
});

afterEach(() => {
  Object.defineProperty(window, "print", { configurable: true, value: realPrint });
  vi.useRealTimers();
});

const actionMenus = () => screen.getAllByRole("button", { name: "Comment actions" });

async function exportFrom(index: number) {
  fireEvent.click(actionMenus()[index]!);
  fireEvent.click(await screen.findByText("Download PDF"));
}

const portal = () => document.querySelector(".comment-print-portal");

describe("CommentCard — download comment as PDF", () => {
  it("prints the thread root's own body", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);

    await waitFor(() => expect(printed).toHaveLength(1));
    expect(printed[0]).toContain("body root");
  });

  it("prints a reply's own body, not the thread root's", async () => {
    renderThread(comment("root", null), [comment("reply", "root")]);

    // Menus render in order: root first, then the reply.
    await exportFrom(1);

    await waitFor(() => expect(printed).toHaveLength(1));
    expect(printed[0]).toContain("body reply");
    expect(printed[0]).not.toContain("body root");
  });

  it("keeps the surface up until the print dialog is done with it", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);
    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(1));

    // The dialog is still open; the page still has to hold the comment.
    expect(portal()).not.toBeNull();
    expect(portal()?.textContent).toContain("body root");

    window.dispatchEvent(new Event("afterprint"));

    await waitFor(() => expect(portal()).toBeNull());
  });

  it("opens the print pipeline exactly once per click", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);

    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(1));
    // Past the readiness wait and past the close fallback: a second dialog here
    // means the surface re-ran its print effect.
    await new Promise((resolve) => setTimeout(resolve, 1200));
    expect(window.print).toHaveBeenCalledTimes(1);
    expect(printed).toHaveLength(1);
  });

  it("lets a reader export again after the first dialog closes", async () => {
    renderThread(comment("root", null), []);

    await exportFrom(0);
    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(1));
    window.dispatchEvent(new Event("afterprint"));
    await waitFor(() => expect(portal()).toBeNull());

    await exportFrom(0);

    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(2));
    expect(printed).toHaveLength(2);
  });
});
