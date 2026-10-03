"use client";

/**
 * One comment's capture surface (M-125).
 *
 * The reader drops the result straight into a chat message, so the browser's own
 * renderer produces it: the surface is pinned over the app, then
 * `captureCommentPng` clones the document node, serializes the clone into an SVG
 * `foreignObject` and hands back a PNG blob. There is no PDF library and no
 * second rendering path to keep in step with the app — the surface renders the
 * same `ReadonlyContent` the comment card renders, so what is captured is what
 * the reader saw, GFM tables, highlighted fences, KaTeX and Mermaid included.
 *
 * It is portalled to `document.body` and pinned over the app, which is what
 * frees it from the issue view's scroll containers, sticky comment headers and
 * `pl-12` reply padding. `comment-print.css` then styles that one node as the
 * page it is about to become.
 *
 * What this cannot give back is a text layer: the image is pixels, so nobody
 * can select or search inside it. That is the trade the export makes on
 * purpose — the destination is a message, not a document.
 *
 * The names still say "print". `.comment-print-doc` is the contract this
 * stylesheet and the DOM assertions are written against, and renaming it buys
 * nothing a reader can see.
 */

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { toast } from "sonner";
import { useActorName } from "@multica/core/workspace/hooks";
import type { TimelineEntry } from "@multica/core/types";
import { useLocale, useT } from "../../i18n";
import { ReadonlyContent } from "../../editor";
import { InlineMermaidContext } from "../../editor/mermaid-diagram";
import { downloadBlob } from "../../editor/utils/mermaid-export";
import { captureCommentPng, commentPngFilename } from "../utils/comment-capture";
import { waitForPrintSurfaceReady } from "../utils/comment-print";
import "./comment-print.css";

export function CommentPrintSurface({
  entry,
  onClose,
}: {
  entry: TimelineEntry;
  onClose: () => void;
}) {
  const { t } = useT("issues");
  const locale = useLocale();
  const { getActorName } = useActorName();
  const docRef = useRef<HTMLDivElement>(null);

  // The capture effect must not depend on `t`: a new identity would re-run it
  // and export again. The effect reads the translator through a ref instead,
  // which is why this is declared before it — effects run in declaration order,
  // so the ref is already current when the capture effect starts.
  const tRef = useRef(t);
  useEffect(() => {
    tRef.current = t;
  }, [t]);

  // The surface only ever opens from a click, so it always opens after
  // hydration — but `createPortal` needs a `document`, which a server render
  // has none of. The gate is what keeps the first client frame identical to the
  // server's.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  // Belt and braces against this effect's own deps: whatever re-runs it — a
  // caller that forgot `useCallback`, a language change, any re-render — the
  // export happens at most once per mount.
  const capturedRef = useRef(false);

  // Fixed for the life of the surface, so it belongs in a ref rather than in the
  // deps: a different comment would mean a different surface.
  const createdAtRef = useRef(entry.created_at);

  useEffect(() => {
    if (!mounted) return;
    const doc = docRef.current;
    if (!doc || capturedRef.current) return;
    capturedRef.current = true;

    let cancelled = false;

    void (async () => {
      // A diagram's SVG and a rich block's mount both land after this paint.
      // Capturing first ships an empty box, so wait — on a bounded budget,
      // because a comment whose diagram never resolves still has to export.
      await waitForPrintSurfaceReady(doc);
      if (cancelled) return;
      try {
        const blob = await captureCommentPng(doc);
        if (cancelled) return;
        downloadBlob(blob, commentPngFilename(createdAtRef.current));
      } catch {
        toast.error(tRef.current(($) => $.comment.export_png_failed_toast));
      }
      // The image is in the browser's hands by now; nothing about the surface is
      // worth keeping over the app.
      if (!cancelled) onClose();
    })();

    return () => {
      cancelled = true;
    };
    // `onClose` MUST be stable (useCallback at the call site). `capturedRef`,
    // `tRef` and `createdAtRef` are refs, so `t` and `entry` deliberately stay
    // out of this list: a changed language must not re-export, and the entry
    // cannot change under a surface that exists to export it.
  }, [mounted, onClose]);

  if (!mounted) return null;

  return createPortal(
    <div className="comment-print-portal" data-comment-print-portal="">
      <div ref={docRef} className="comment-print-doc" data-comment-print-doc="">
        <header className="comment-print-head">
          <span className="comment-print-author">
            {entry.actor_name || getActorName(entry.actor_type, entry.actor_id)}
          </span>
          <time dateTime={entry.created_at}>
            {new Date(entry.created_at).toLocaleString(locale)}
          </time>
        </header>
        {/* The diagram has to be inline SVG here: a sandboxed `srcDoc` iframe is
            a separate document the rasterizer cannot read, so the capture would
            come back with an empty box where the diagram should be. */}
        <InlineMermaidContext.Provider value={true}>
          <ReadonlyContent content={entry.content ?? ""} attachments={entry.attachments} />
        </InlineMermaidContext.Provider>
      </div>
    </div>,
    document.body,
  );
}