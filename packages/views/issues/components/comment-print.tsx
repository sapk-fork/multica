"use client";

/**
 * One comment's print surface (M-125).
 *
 * The browser is the PDF producer: `window.print()` opens the print pipeline,
 * where "Save as PDF" is one of its destinations. There is no PDF library and
 * no second rendering path to keep in step with the app — the surface renders
 * the same `ReadonlyContent` the comment card renders, so what prints is what
 * the reader saw, GFM tables, highlighted fences, KaTeX and Mermaid included.
 *
 * It is portalled to `document.body` and pinned over the app, which is what
 * frees it from the issue view's scroll containers, sticky comment headers and
 * `pl-12` reply padding. `comment-print.css` then hides every other child of
 * `<body>` and gives the page to this one element.
 */

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { toast } from "sonner";
import { useActorName } from "@multica/core/workspace/hooks";
import type { TimelineEntry } from "@multica/core/types";
import { useLocale, useT } from "../../i18n";
import { ReadonlyContent } from "../../editor";
import { waitForPrintSurfaceReady } from "../utils/comment-print";
import "./comment-print.css";

/**
 * How long the surface waits for `afterprint` before tearing itself down
 * anyway, for engines that never fire it.
 *
 * It is a ceiling, not a delay: Chromium and Firefox block inside
 * `window.print()` until the dialog is dismissed and fire `afterprint` while
 * still inside that call, so they never reach it. The floor is the point —
 * Safari's `window.print()` returns immediately and opens the panel afterwards,
 * so anything that closes the surface on return tears it down BEFORE the
 * browser snapshots the page and the reader gets a blank sheet. A second is
 * comfortably past the panel appearing, while still short enough that a silent
 * engine does not leave a white sheet over the app.
 */
export const PRINT_CLOSE_FALLBACK_MS = 1000;

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

  // The print effect must not depend on `t`: a new identity would re-run it
  // and open another print dialog. The effect reads the translator through a
  // ref instead, which is why this is declared before it — effects run in
  // declaration order, so the ref is already current when the print effect
  // starts.
  const tRef = useRef(t);
  useEffect(() => {
    tRef.current = t;
  }, [t]);

  // The surface only ever opens from a click, so it always opens after
  // hydration — but `createPortal` needs a `document`, which a server render
  // has none of. The gate is what keeps the first client frame identical to
  // the server's.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  // Belt and braces against this effect's own deps: whatever re-runs it — a
  // caller that forgot `useCallback`, a language change, any re-render — the
  // pipeline opens at most once per mount.
  const printedRef = useRef(false);

  useEffect(() => {
    if (!mounted) return;
    const doc = docRef.current;
    if (!doc || printedRef.current) return;
    printedRef.current = true;

    let cancelled = false;
    let fallback: ReturnType<typeof setTimeout> | undefined;

    const close = () => {
      if (fallback !== undefined) clearTimeout(fallback);
      onClose();
    };

    // Registered BEFORE the print call: an engine that blocks inside
    // `window.print()` fires `afterprint` before that call returns, and a
    // listener attached afterwards would miss it and leave a stale surface up.
    window.addEventListener("afterprint", close);

    void (async () => {
      // A diagram's SVG and a rich block's mount both land after this paint.
      // Printing first ships an empty box, so wait — on a bounded budget,
      // because a comment whose diagram never resolves still has to print.
      await waitForPrintSurfaceReady(doc);
      if (cancelled) return;
      if (typeof window.print !== "function") {
        toast.error(tRef.current(($) => $.comment.export_pdf_failed_toast));
        close();
        return;
      }
      try {
        window.print();
      } catch {
        toast.error(tRef.current(($) => $.comment.export_pdf_failed_toast));
      }
      // Returning from `window.print()` says nothing about the dialog: Safari
      // opens its panel afterwards. If `afterprint` already closed the surface
      // while we were blocked inside the call, `cancelled` is set and there is
      // nothing left to schedule.
      if (cancelled) return;
      fallback = setTimeout(close, PRINT_CLOSE_FALLBACK_MS);
    })();

    return () => {
      cancelled = true;
      if (fallback !== undefined) clearTimeout(fallback);
      window.removeEventListener("afterprint", close);
    };
    // `onClose` MUST be stable (useCallback at the call site). `printedRef` and
    // `tRef` are refs, so `t` deliberately stays out of this list.
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
        <ReadonlyContent content={entry.content ?? ""} attachments={entry.attachments} />
      </div>
    </div>,
    document.body,
  );
}
