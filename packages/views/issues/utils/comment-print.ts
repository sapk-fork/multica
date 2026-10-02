/**
 * Print-surface readiness for the comment PDF export (M-125).
 *
 * `window.print()` snapshots the DOM synchronously, so anything that fills in
 * later simply misses the page. Two things in a rendered comment arrive late: a
 * rich block behind `LazyRichBlock`'s near-viewport gate mounts from an
 * IntersectionObserver callback, and a Mermaid diagram or an HTML preview
 * resolves asynchronously before it swaps `DynamicBlockSkeleton` for its
 * sandboxed `srcDoc` iframe.
 *
 * "Ready" therefore means: no unmounted rich-block shell left, and no preview
 * body still reporting that it is rendering.
 */

/**
 * Long enough for a cached Mermaid render plus the iframe's `srcdoc` parse. It
 * is a ceiling, not a delay: a comment whose diagram never resolves still has
 * to print.
 */
export const PRINT_SURFACE_READY_TIMEOUT_MS = 800;

const POLL_INTERVAL_MS = 30;

/** `LazyRichBlock` marks its shell only once the block has mounted. */
const PENDING_SHELL = "[data-rich-block-shell]:not([data-mounted])";
/**
 * `DynamicBlockSkeleton` — the loading state a framed Mermaid diagram and an
 * HTML preview both render while their content is still being produced.
 *
 * Not `.mermaid-diagram-loading`: that element only exists on the *unframed*
 * preview, and a comment body is always framed, so inside a print surface it
 * can never match. See `mermaid-diagram.tsx`'s `rendered ? … : frame ? skeleton
 * : loading` branch.
 */
const RENDERING_PREVIEW = "[data-dynamic-block-skeleton]";

export function isPrintSurfaceReady(root: ParentNode): boolean {
  return (
    root.querySelector(PENDING_SHELL) === null &&
    root.querySelector(RENDERING_PREVIEW) === null
  );
}

/**
 * Resolves `true` once the surface can be printed, `false` if the budget ran
 * out first. Never rejects: a print that is slightly early beats no print.
 */
export function waitForPrintSurfaceReady(
  root: ParentNode,
  timeoutMs: number = PRINT_SURFACE_READY_TIMEOUT_MS,
): Promise<boolean> {
  if (isPrintSurfaceReady(root)) return Promise.resolve(true);

  return new Promise<boolean>((resolve) => {
    const deadline: ReturnType<typeof setTimeout> = setTimeout(() => {
      clearTimeout(poll);
      resolve(false);
    }, timeoutMs);

    const check = () => {
      if (isPrintSurfaceReady(root)) {
        clearTimeout(deadline);
        resolve(true);
        return;
      }
      poll = setTimeout(check, POLL_INTERVAL_MS);
    };

    let poll: ReturnType<typeof setTimeout> = setTimeout(check, POLL_INTERVAL_MS);
  });
}
