import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));

vi.mock("modern-screenshot", () => ({ domToBlob }));

import {
  COMMENT_CAPTURE_BACKGROUND,
  COMMENT_CAPTURE_PIXEL_RATIO,
  captureCommentPng,
  commentPngFilename,
} from "./comment-capture";

function doc(): HTMLElement {
  const el = document.createElement("div");
  el.className = "comment-print-doc";
  document.body.appendChild(el);
  return el;
}

beforeEach(() => {
  domToBlob.mockReset();
  domToBlob.mockResolvedValue(new Blob(["png"], { type: "image/png" }));
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
    const blob = new Blob(["png"], { type: "image/png" });
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