/**
 * Rasterizing one comment into a PNG (M-125).
 *
 * The browser's own renderer does the work: `modern-screenshot` clones the
 * node, serializes the clone into an SVG `foreignObject` and lets the browser
 * paint it. That is why this export needs no PDF/canvas library of its own —
 * and why it can parse the app's `oklch()` tokens, which is the whole reason it
 * was chosen over `html2canvas`.
 *
 * What it cannot give back is a text layer: the image is pixels, so a reader
 * cannot select or search inside it. That is the trade the feature makes on
 * purpose — the destination is a chat message, not a document.
 */

/**
 * Capture above CSS pixels so text stays legible once Slack scales the image
 * into a message column. 2 is a typical HiDPI ratio; 3 buys nothing visible
 * and triples the file.
 */
export const COMMENT_CAPTURE_PIXEL_RATIO = 2;

/**
 * Opaque white, not transparent. A PNG with alpha looks fine on the white web
 * app and wrong on a dark Slack theme, and the surface's own background is
 * below the canvas fill rather than instead of it.
 */
export const COMMENT_CAPTURE_BACKGROUND = "#ffffff";

/**
 * Ceiling on the whole capture. Font embedding and the rasterization step both
 * fetch, and a stalled request must end the export with an error rather than
 * leave a white sheet over the app forever.
 */
export const COMMENT_CAPTURE_TIMEOUT_MS = 10_000;

/**
 * Chrome caps a canvas at 65,535 device px per dimension, so at the 2x scale
 * the sheet ceiling is about 32,700 CSS px — roughly 417 paragraphs.
 *
 * Measured before the capture rather than after, because past the ceiling the
 * rasterizer does not fail: it RESOLVES with a stub (measured: 54 bytes of WebP
 * carrying a `wPHYS` chunk and no PNG signature). A download of that is a file
 * no viewer opens, handed over as a success — the same "click produced nothing
 * and said nothing" class as a recycled row, so it is refused up front with the
 * failure the reader is already told about.
 */
export const COMMENT_CAPTURE_MAX_CSS_PX = 32_000;

/**
 * Floor for a believable capture, in bytes.
 *
 * A real sheet is hundreds of kilobytes; the stub at the ceiling is 54. This is
 * a backstop rather than the primary guard — the height check above catches the
 * known case, and this catches whatever else a browser might hand back as a
 * resolved-but-empty blob.
 */
export const COMMENT_CAPTURE_MIN_BYTES = 1024;

/** The sheet's own height in CSS px, independent of how much of it is scrolled. */
function sheetHeightPx(doc: HTMLElement): number {
  return Math.max(doc.scrollHeight, doc.getBoundingClientRect().height);
}

/**
 * Rejects a sheet the browser cannot rasterize into one canvas.
 *
 * Exported for its own tests, not for callers: `captureCommentPng` is the door.
 */
export function assertSheetWithinCaptureCeiling(doc: HTMLElement): void {
  const height = sheetHeightPx(doc);
  if (height > COMMENT_CAPTURE_MAX_CSS_PX) {
    throw new Error(
      `Comment sheet is too tall to capture: ${Math.round(height)}px of a ` +
        `${COMMENT_CAPTURE_MAX_CSS_PX}px canvas`,
    );
  }
}

/**
 * Rejects a capture that resolved but is not an image of the sheet.
 *
 * The rasterizer labels its own output, so the MIME type is the ground truth
 * for what came back; the size floor catches a same-typed stub.
 */
export function assertCapturedPng(blob: Blob): void {
  if (blob.type !== "image/png") {
    throw new Error(`Capture came back as ${blob.type || "an unlabelled blob"}, not a PNG`);
  }
  if (blob.size < COMMENT_CAPTURE_MIN_BYTES) {
    throw new Error(
      `Capture came back as ${blob.size} bytes, too small to be a comment image`,
    );
  }
}

export async function captureCommentPng(doc: HTMLElement): Promise<Blob> {
  assertSheetWithinCaptureCeiling(doc);

  // Imported on demand: the rasterizer is only needed once someone exports a
  // comment, and keeping it out of the comment-card chunk keeps that cost off
  // every issue view that never uses it. A failed import surfaces through the
  // same catch as a failed capture.
  const { domToBlob } = await import("modern-screenshot");

  const blob = await domToBlob(doc, {
    backgroundColor: COMMENT_CAPTURE_BACKGROUND,
    scale: COMMENT_CAPTURE_PIXEL_RATIO,
    timeout: COMMENT_CAPTURE_TIMEOUT_MS,
  });

  // Checked even though the height guard ran: the two cover different failures,
  // and a resolved blob is the one shape that reaches the reader as a file.
  assertCapturedPng(blob);

  return blob;
}

/**
 * `comment-2026-09-11-0700.png`, from the comment's own timestamp in UTC.
 *
 * UTC because the name has to be the same string everywhere — it lands in a
 * download list that outlives the session, and a local-time name would differ
 * per reader. The minute is there so two comments from one afternoon do not
 * collide as `comment (1).png`.
 */
export function commentPngFilename(createdAt: string): string {
  const parsed = new Date(createdAt);
  if (Number.isNaN(parsed.getTime())) return "comment.png";

  const stamp = parsed
    .toISOString()
    .slice(0, 16)
    .replace("T", "-")
    .replace(":", "");

  return `comment-${stamp}.png`;
}