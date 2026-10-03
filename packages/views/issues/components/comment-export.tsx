"use client";

/**
 * Runs the comment image export (M-125) from its own React root.
 *
 * The menu item lives inside a comment card, and the issue timeline is
 * virtualized: a card can unmount and remount within the same second, and it
 * does exactly that the moment the export surface mounts its own blocks — the
 * row is recycled, its state goes with it, and an export held there is killed
 * mid-capture. The click then does nothing at all: no file, no error, no toast.
 * That is not hypothetical; it is what the first browser run of this feature did
 * every time.
 *
 * So the capture does not live in the card's tree. This module is the whole
 * wiring a menu item needs: mount the surface, and let it close itself.
 */

import { createRoot } from "react-dom/client";
import type { TimelineEntry } from "@multica/core/types";

import { CommentPrintSurface } from "./comment-print";

/**
 * Captures one comment as a PNG and hands it to the browser as a download.
 *
 * Synchronous and fire-and-forget on purpose: it opens a surface and returns.
 * Everything the reader sees afterwards — the sheet, the file, the failure toast
 * — belongs to `CommentPrintSurface`.
 */
export function exportCommentImage(entry: TimelineEntry): void {
  // The surface is a portal onto `document.body`; there is nothing to export
  // without one.
  if (typeof document === "undefined") return;

  const container = document.createElement("div");
  // A named hook for the host node: an export's container is otherwise an empty
  // div, which nothing — not even a test — can find afterwards.
  container.dataset.commentExportRoot = "";
  document.body.appendChild(container);
  const root = createRoot(container);

  const close = () => {
    // After the current task, never inside it: this runs from a React commit
    // (the surface's own effect), and unmounting a root synchronously from
    // there is what React refuses to do. The container goes in a `finally` —
    // an unmount that throws must not leave an empty div in the app for good.
    queueMicrotask(() => {
      try {
        root.unmount();
      } finally {
        container.remove();
      }
    });
  };

  root.render(<CommentPrintSurface entry={entry} onClose={close} />);
}