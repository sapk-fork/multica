import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, waitFor } from "@testing-library/react";
import { useState } from "react";
import type { TimelineEntry } from "@multica/core/types";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));
vi.mock("modern-screenshot", () => ({ domToBlob }));

const { downloadBlob } = vi.hoisted(() => ({ downloadBlob: vi.fn() }));
vi.mock("../../editor/utils/mermaid-export", () => ({ downloadBlob }));

vi.mock("@multica/core/api", () => ({
  api: { uploadFile: vi.fn() },
  dispatchReasonCode: () => undefined,
  errorCode: () => undefined,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Ada" }),
}));

// `t` is a NEW function on every call, which is the hostile case the capture
// effect has to survive: an effect that listed `t` in its deps would re-run on
// every render and capture again each time. Real i18next holds one identity per
// language, so this is strictly harder than production. The real EN bundle
// still answers the toast, so the assertion below reads the shipped string
// rather than a stub's.
vi.mock("../../i18n", async () => {
  const issues = (await import("../../locales/en/issues.json")).default;
  return {
    useLocale: () => "en",
    useT: () => ({ t: (select: (bundle: typeof issues) => string) => select(issues) }),
  };
});

// The real renderer pulls in Mermaid and KaTeX CSS; the surface's own contract
// is "the body it was handed reaches the capture", which the stub still proves.
vi.mock("../../editor", async () => ({
  ...(await vi.importActual<typeof import("../../editor/use-upload-gate")>("../../editor/use-upload-gate")),
  ReadonlyContent: ({ content }: { content: string }) => <div>{content}</div>,
}));

import { CommentPrintSurface } from "./comment-print";

const entry: TimelineEntry = {
  type: "comment",
  id: "root",
  actor_type: "member",
  actor_id: "user-1",
  content: "## Findings\n\n| a | b |",
  parent_id: null,
  comment_type: "comment",
  reactions: [],
  attachments: [],
  created_at: "2026-09-11T07:00:00Z",
  updated_at: "2026-09-11T07:00:00Z",
  revision: 1,
};

/**
 * Big enough to clear `captureCommentPng`'s plausibility floor. A three-byte
 * stub is now a rejected capture, which is the point: a capture that resolves
 * with nothing in it is the bug this guard exists for.
 */
function capturedPng(): Blob {
  return new Blob(["x".repeat(40_000)], { type: "image/png" });
}

/** What each capture actually saw on the page, in call order. */
let captured: string[];

beforeEach(() => {
  captured = [];
  domToBlob.mockReset();
  domToBlob.mockImplementation(async () => {
    captured.push(document.querySelector(".comment-print-doc")?.textContent ?? "");
    return capturedPng();
  });
  downloadBlob.mockReset();
  toastError.mockClear();
});

afterEach(() => {
  vi.useRealTimers();
});

/**
 * Production's shape: the PARENT owns the open flag and hands the surface a
 * close callback that flips it. A spy alone would record the call without
 * unmounting anything, and every "is the surface gone" question here would be
 * about the spy rather than the product.
 */
function Harness({ onClose }: { onClose: () => void }) {
  const [open, setOpen] = useState(true);
  if (!open) return null;
  return (
    <CommentPrintSurface
      entry={entry}
      onClose={() => {
        onClose();
        setOpen(false);
      }}
    />
  );
}

const portal = () => document.querySelector(".comment-print-portal");
const doc = () => document.querySelector(".comment-print-doc");

describe("CommentPrintSurface", () => {
  it("puts the comment body on the page and captures it", async () => {
    render(<Harness onClose={vi.fn()} />);

    await waitFor(() => expect(captured).toHaveLength(1));
    expect(captured[0]).toContain("## Findings");
  });

  it("hands the captured image to the browser as a PNG file", async () => {
    render(<Harness onClose={vi.fn()} />);

    await waitFor(() => expect(downloadBlob).toHaveBeenCalledTimes(1));
    const [blob, filename] = downloadBlob.mock.calls[0]!;
    expect(blob).toBeInstanceOf(Blob);
    expect(blob.type).toBe("image/png");
    expect(filename).toBe("comment-2026-09-11-0700.png");
  });

  it("names the author and dates the document", async () => {
    render(<Harness onClose={vi.fn()} />);

    await waitFor(() => expect(captured).toHaveLength(1));
    expect(captured[0]).toContain("Ada");
    expect(captured[0]).toContain("2026");
  });

  // The regression this component's ordering exists for. A capture is not free:
  // it clones the DOM, embeds fonts and rasterizes, all asynchronously. A
  // surface torn down before the blob lands exports nothing at all.
  it("holds the surface on the page until the capture lands", async () => {
    let land: (blob: Blob) => void = () => {};
    domToBlob.mockImplementation(
      () =>
        new Promise<Blob>((resolve) => {
          land = resolve;
        }),
    );
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));

    // Capturing, nothing downloaded yet.
    expect(onClose).not.toHaveBeenCalled();
    expect(downloadBlob).not.toHaveBeenCalled();
    expect(portal()).not.toBeNull();
    expect(doc()?.textContent).toContain("## Findings");

    await act(async () => {
      land(capturedPng());
    });

    await waitFor(() => expect(portal()).toBeNull());
    expect(downloadBlob).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("reports and closes instead of hanging when the capture fails", async () => {
    domToBlob.mockRejectedValue(new Error("canvas too large"));
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't create the image"),
    );
    expect(downloadBlob).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(portal()).toBeNull();
  });

  // `useT` hands back a fresh `t` per render here (see the mock above), and the
  // harness passes a fresh `onClose` for the same reason — a caller who forgets
  // `useCallback`. Neither may produce a second capture.
  it("captures once per mount, whatever the deps do", async () => {
    const onClose = vi.fn();
    const { rerender } = render(<Harness onClose={onClose} />);

    await waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));

    for (let i = 0; i < 5; i++) {
      rerender(<Harness onClose={onClose} />);
    }
    // The capture sits behind an `await`, so let those continuations run before
    // counting: asserting synchronously would read the count from before the
    // rerenders and pass on a component that would capture six times.
    await act(async () => {});

    expect(domToBlob).toHaveBeenCalledTimes(1);
    expect(downloadBlob).toHaveBeenCalledTimes(1);
    expect(captured).toHaveLength(1);
  });
});