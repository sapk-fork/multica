"use client";

/**
 * Hosts the comment image export (M-125) for the whole app.
 *
 * The request comes from a comment card, and the issue timeline is virtualized:
 * a card unmounts and remounts within the same second, and it does exactly that
 * as soon as the export surface mounts its own blocks. An export held in that
 * card's state died with it mid-capture — the click produced no file, no error
 * and no toast, which is the worst possible failure for a button.
 *
 * So the request lives in the app-level modal store and the surface is rendered
 * here, next to every other app-level overlay, where nothing recycles it. It
 * also has to be here: the surface renders `ReadonlyContent`, which needs the
 * app's providers, so it cannot be mounted in a React root of its own.
 */

import { useModalStore } from "@multica/core/modals";
import type { TimelineEntry } from "@multica/core/types";

import { CommentPrintSurface } from "./comment-print";

export function CommentExportHost() {
  const modal = useModalStore((s) => s.modal);
  const data = useModalStore((s) => s.data);
  const close = useModalStore((s) => s.close);

  if (modal !== "comment-image-export") return null;

  // The store carries modal payloads as an opaque record, like every other
  // modal's `data`; the shape is the opener's contract, checked there.
  const entry = data?.entry as TimelineEntry | undefined;
  if (!entry) return null;

  return <CommentPrintSurface entry={entry} onClose={close} />;
}
