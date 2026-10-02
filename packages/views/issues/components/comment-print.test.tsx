import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, waitFor } from "@testing-library/react";
import { useState } from "react";
import type { TimelineEntry } from "@multica/core/types";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

vi.mock("@multica/core/api", () => ({
  api: { uploadFile: vi.fn() },
  dispatchReasonCode: () => undefined,
  errorCode: () => undefined,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Ada" }),
}));

// `t` is a NEW function on every call, which is the hostile case the print
// effect has to survive: an effect that listed `t` in its deps would re-run on
// every render and open another print dialog each time. Real i18next holds one
// identity per language, so this is strictly harder than production. The real EN
// bundle still answers the toast, so the assertion below reads the shipped
// string rather than a stub's.
vi.mock("../../i18n", async () => {
  const issues = (await import("../../locales/en/issues.json")).default;
  return {
    useLocale: () => "en",
    useT: () => ({ t: (select: (bundle: typeof issues) => string) => select(issues) }),
  };
});

// The real renderer pulls in Mermaid and KaTeX CSS; the surface's own contract
// is "the body it was handed reaches the page", which the stub still proves.
vi.mock("../../editor", async () => ({
  ...(await vi.importActual<typeof import("../../editor/use-upload-gate")>("../../editor/use-upload-gate")),
  ReadonlyContent: ({ content }: { content: string }) => <div>{content}</div>,
}));

import { CommentPrintSurface, PRINT_CLOSE_FALLBACK_MS } from "./comment-print";

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
  toastError.mockClear();
});

afterEach(() => {
  Object.defineProperty(window, "print", { configurable: true, value: realPrint });
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

/** Safari: `print()` returns at once and the panel opens afterwards. */
function panelCloses() {
  window.dispatchEvent(new Event("afterprint"));
}

describe("CommentPrintSurface", () => {
  it("puts the comment body on the page and prints it", async () => {
    render(<Harness onClose={vi.fn()} />);

    await waitFor(() => expect(printed).toHaveLength(1));
    expect(printed[0]).toContain("## Findings");
  });

  it("names the author and dates the document", async () => {
    render(<Harness onClose={vi.fn()} />);

    await waitFor(() => expect(printed).toHaveLength(1));
    expect(printed[0]).toContain("Ada");
    expect(printed[0]).toContain("2026");
  });

  // The regression this component's ordering exists for. `window.print()`
  // returning does NOT mean the dialog is gone: Safari's is non-blocking and
  // opens the panel afterwards, so a surface torn down on return is torn down
  // BEFORE the snapshot, and the reader gets a blank page.
  it("holds the surface on the page between print() returning and afterprint", async () => {
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(1));

    // print() has returned; the panel is still open.
    expect(onClose).not.toHaveBeenCalled();
    expect(portal()).not.toBeNull();
    expect(doc()?.textContent).toContain("## Findings");

    panelCloses();

    await waitFor(() => expect(portal()).toBeNull());
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes once — afterprint, then no second close from the fallback", async () => {
    vi.useFakeTimers();
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(window.print).toHaveBeenCalledTimes(1);

    act(() => {
      panelCloses();
    });
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(PRINT_CLOSE_FALLBACK_MS * 4);
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // An engine that never fires `afterprint` (some webviews, Electron) must not
  // leave the reader looking at a white sheet over the app forever.
  it("still takes the surface down when afterprint never arrives", async () => {
    vi.useFakeTimers();
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(PRINT_CLOSE_FALLBACK_MS + 20);
    });

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(portal()).toBeNull();
  });

  it("budgets the fallback long enough to clear a panel opened after print() returned", () => {
    // The floor is the requirement, not the number: shorter than this and a
    // non-blocking engine tears the surface down before it has snapshotted.
    expect(PRINT_CLOSE_FALLBACK_MS).toBeGreaterThanOrEqual(500);
  });

  it("reports instead of hanging when the browser has no print pipeline", async () => {
    Object.defineProperty(window, "print", { configurable: true, value: undefined });
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Failed to open the print dialog"),
    );
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(portal()).toBeNull();
  });

  // `useT` hands back a fresh `t` per render here (see the mock above), and the
  // harness passes a fresh `onClose` for the same reason — a caller who forgets
  // `useCallback`. Neither may produce a second dialog.
  it("opens the print pipeline once per mount, whatever the deps do", async () => {
    const onClose = vi.fn();
    const { rerender } = render(<Harness onClose={onClose} />);

    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(1));

    for (let i = 0; i < 5; i++) {
      rerender(<Harness onClose={onClose} />);
    }
    // The print call sits behind an `await`, so let those continuations run
    // before counting: asserting synchronously would read the count from
    // before the rerenders and pass on a component that would print six times.
    await act(async () => {});

    expect(window.print).toHaveBeenCalledTimes(1);
    expect(printed).toHaveLength(1);
  });
});
