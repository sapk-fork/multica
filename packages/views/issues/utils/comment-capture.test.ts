import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));

vi.mock("modern-screenshot", () => ({ domToBlob }));

import {
  COMMENT_CAPTURE_BACKGROUND,
  COMMENT_CAPTURE_MAX_CSS_PX,
  COMMENT_CAPTURE_MIN_BYTES,
  COMMENT_CAPTURE_PIXEL_RATIO,
  captureCommentPng,
  commentPngFilename,
} from "./comment-capture";

/** Big enough to clear the plausibility floor the capture guards with. */
function pngBytes(size = 40_000): Blob {
  return new Blob(["x".repeat(size)], { type: "image/png" });
}

function doc(sheetHeightPx = 600): HTMLElement {
  const el = document.createElement("div");
  el.className = "comment-print-doc";
  // jsdom reports 0 for every box, and the ceiling guard reads the sheet's
  // height, so the measurement has to come from somewhere.
  Object.defineProperty(el, "scrollHeight", { configurable: true, value: sheetHeightPx });
  document.body.appendChild(el);
  return el;
}

beforeEach(() => {
  domToBlob.mockReset();
  domToBlob.mockResolvedValue(pngBytes());
});

afterEach(() => {
  document.body.innerHTML = "";
});

describe("captureCommentPng", () => {
  it("captures the document node it was handed", async () => {
    const node = doc();

    await captureCommentPng(node);

    expect(domToBlob).toHaveBeenCalledTimes(1);
    expect(domToBlob.mock.calls[0]![0]).toBe(node);
  });

  it("captures on an opaque page, so the image is not transparent in Slack", async () => {
    await captureCommentPng(doc());

    const options = domToBlob.mock.calls[0]![1] as { backgroundColor?: string };
    expect(options.backgroundColor).toBe(COMMENT_CAPTURE_BACKGROUND);
    expect(COMMENT_CAPTURE_BACKGROUND).not.toMatch(/transparent|^$/);
  });

  it("captures above CSS pixels so the text stays legible in a thread", async () => {
    await captureCommentPng(doc());

    const options = domToBlob.mock.calls[0]![1] as { scale?: number };
    expect(options.scale).toBe(COMMENT_CAPTURE_PIXEL_RATIO);
    expect(COMMENT_CAPTURE_PIXEL_RATIO).toBeGreaterThan(1);
  });

  it("bounds the capture, so a stalled font fetch still ends the export", async () => {
    await captureCommentPng(doc());

    const options = domToBlob.mock.calls[0]![1] as { timeout?: number };
    expect(typeof options.timeout).toBe("number");
    expect(options.timeout!).toBeGreaterThan(0);
  });

  it("returns what the rasterizer produced", async () => {
    const blob = pngBytes();
    domToBlob.mockResolvedValue(blob);

    await expect(captureCommentPng(doc())).resolves.toBe(blob);
  });

  it("propagates a failed capture instead of downloading nothing quietly", async () => {
    domToBlob.mockRejectedValue(new Error("canvas too large"));

    await expect(captureCommentPng(doc())).rejects.toThrow("canvas too large");
  });
});

describe("commentPngFilename", () => {
  it("names the file after the comment's own moment, in UTC", () => {
    expect(commentPngFilename("2026-09-11T07:00:00Z")).toBe("comment-2026-09-11-0700.png");
  });

  it("still produces a filename when the timestamp is unusable", () => {
    expect(commentPngFilename("")).toBe("comment.png");
    expect(commentPngFilename("not a date")).toBe("comment.png");
  });

  it("never puts path separators in a download name", () => {
    expect(commentPngFilename("2026-09-11T07:00:00Z")).not.toMatch(/[/\\]/);
  });
});
describe("the canvas ceiling", () => {
  // Chrome caps a canvas at 65,535 device px per dimension, so at the 2x scale
  // the sheet ceiling is about 32,700 CSS px. Past it the rasterizer RESOLVES
  // with a stub — measured at 54 bytes of WebP, no PNG signature — instead of
  // rejecting, so nothing downstream ever learns the capture failed.
  it("refuses a sheet past the ceiling without spending a capture on it", async () => {
    const node = doc(COMMENT_CAPTURE_MAX_CSS_PX + 1);

    await expect(captureCommentPng(node)).rejects.toThrow(/too tall/i);
    expect(domToBlob).not.toHaveBeenCalled();
  });

  it("captures a sheet exactly at the ceiling", async () => {
    await expect(captureCommentPng(doc(COMMENT_CAPTURE_MAX_CSS_PX))).resolves.toBeInstanceOf(Blob);
    expect(domToBlob).toHaveBeenCalledTimes(1);
  });

  it("leaves the practical limit where the comment is", () => {
    // ~417 paragraphs, ~30,000 words. The ceiling is a browser constraint, not
    // the thing a reader hits.
    expect(COMMENT_CAPTURE_MAX_CSS_PX).toBeGreaterThan(30_000);
    expect(COMMENT_CAPTURE_MAX_CSS_PX).toBeLessThan(35_000);
  });
});

describe("a rasterizer that resolves with a stub", () => {
  it("rejects an image that is not a PNG", async () => {
    domToBlob.mockResolvedValue(new Blob(["RIFF"], { type: "image/webp" }));

    await expect(captureCommentPng(doc())).rejects.toThrow(/not a PNG/i);
  });

  it("rejects a PNG too small to be a capture of anything", async () => {
    domToBlob.mockResolvedValue(new Blob(["x".repeat(54)], { type: "image/png" }));

    await expect(captureCommentPng(doc())).rejects.toThrow(/too small/i);
  });

  it("says how small is too small, so the floor is not a magic number", () => {
    expect(COMMENT_CAPTURE_MIN_BYTES).toBeGreaterThan(54);
    expect(COMMENT_CAPTURE_MIN_BYTES).toBeLessThan(64 * 1024);
  });

  it("accepts a plausible PNG", async () => {
    domToBlob.mockResolvedValue(pngBytes(COMMENT_CAPTURE_MIN_BYTES + 1));

    await expect(captureCommentPng(doc())).resolves.toBeInstanceOf(Blob);
  });
});
