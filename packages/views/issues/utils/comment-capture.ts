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

export async function captureCommentPng(doc: HTMLElement): Promise<Blob> {
  // Imported on demand: the rasterizer is only needed once someone exports a
  // comment, and keeping it out of the comment-card chunk keeps that cost off
  // every issue view that never uses it. A failed import surfaces through the
  // same catch as a failed capture.
  const { domToBlob } = await import("modern-screenshot");

  return domToBlob(doc, {
    backgroundColor: COMMENT_CAPTURE_BACKGROUND,
    scale: COMMENT_CAPTURE_PIXEL_RATIO,
    timeout: COMMENT_CAPTURE_TIMEOUT_MS,
  });
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