import "./env";

import { readFile } from "fs/promises";
import { expect, test, type Download, type Page } from "@playwright/test";

import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

/**
 * "Download PNG" hands one comment to the browser's own rasterizer.
 *
 * The capture is real — the surface, the readiness wait and the download all
 * run — but the rasterizer's output is observed rather than inspected: what this
 * file proves is *which* comment reached the capture, that the surface is torn
 * down afterwards, and that the file lands with a comment's own name.
 *
 * What it cannot prove is anything about the pixels beyond that: no image
 * decoding, no layout fidelity. Whether the captured sheet is legible and well
 * composed stays the manual check.
 */

const ROOT_BODY = "Root comment body, the thread opener, never the captured one.";
const REPLY_BODY = "Reply comment body, the only body that should reach the capture.";

/** The sheet's own text and colours at the moment the download was triggered. */
interface CaptureRecord {
  text: string;
  background: string;
  color: string;
}

const PNG_SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];

/**
 * The reader gets an image, not an empty or HTML payload wearing a `.png` name.
 * A `download` event and a matching filename both pass on a file with nothing in
 * it, and IHDR carries the pixel size for free.
 */
async function readPng(download: Download) {
  const path = await download.path();
  expect(path, "the download never reached the disk").toBeTruthy();
  const bytes = await readFile(path!);
  expect([...bytes.subarray(0, 8)]).toEqual(PNG_SIGNATURE);
  expect(bytes.length).toBeGreaterThan(512);
  return { width: bytes.readUInt32BE(16), height: bytes.readUInt32BE(20) };
}

/**
 * Perceived brightness of any CSS colour, 0 (black) to 1 (white).
 *
 * The app's own colours are `oklch()`, which a regex cannot read, so the browser
 * resolves them: a canvas accepts every syntax `getComputedStyle` hands back.
 */
function brightness(page: Page, color: string) {
  return page.evaluate((value) => {
    const ctx = document.createElement("canvas").getContext("2d");
    if (!ctx) return -1;
    ctx.fillStyle = value;
    ctx.fillRect(0, 0, 1, 1);
    const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
    return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
  }, color);
}

/**
 * Records the surface and the download together.
 *
 * `downloadBlob` is the one place the export hands the image to the browser, so
 * hooking `URL.createObjectURL` there observes the real capture rather than a
 * stand-in — and it needs no seam in the product code.
 */
async function watchCapture(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as { __captures: CaptureRecord[] };
    w.__captures = [];
    const original = URL.createObjectURL.bind(URL);
    URL.createObjectURL = (object: Blob | MediaSource) => {
      if (object instanceof Blob && object.type === "image/png") {
        // The surface is still mounted here: `downloadBlob` runs before the
        // export tears it down, which makes this the one moment its colours are
        // guaranteed to be the ones the rasterizer saw.
        const doc = document.querySelector<HTMLElement>(".comment-print-doc");
        const style = doc ? getComputedStyle(doc) : null;
        w.__captures.push({
          text: doc?.textContent ?? "",
          background: style?.backgroundColor ?? "",
          color: style?.color ?? "",
        });
      }
      return original(object as Blob);
    };
  });

  return {
    // Listen BEFORE the click: the capture can finish in a couple of seconds,
    // and a download that fires before the listener is attached is missed.
    downloaded: page.waitForEvent("download", { timeout: 120_000 }),
    async records(): Promise<CaptureRecord[]> {
      return page.evaluate(
        () => (window as unknown as { __captures: CaptureRecord[] }).__captures,
      );
    },
  };
}

/**
 * The card holding `ownText` and not `otherText`. Both the thread root and each
 * reply carry `data-comment-block`, and the reply's block sits inside the
 * root's — so excluding the other body is what tells the two apart.
 */
function commentBlock(page: Page, ownText: string, otherText: string) {
  return page
    .locator("[data-comment-block]")
    .filter({ hasText: ownText })
    .filter({ hasNotText: otherText })
    .first();
}

let api: TestApiClient;
let issueId: string;
let workspaceSlug: string;

test.beforeEach(async ({ page }) => {
  api = await createTestApi();
  const issue = await api.createIssue(`E2E comment PNG export ${Date.now()}`);
  issueId = issue.id;
  const root = await api.createComment(issueId, ROOT_BODY);
  await api.createComment(issueId, REPLY_BODY, root.id);
  workspaceSlug = await loginAsDefault(page);
});

test.afterEach(async () => {
  if (api) await api.cleanup();
});

/**
 * The stub has to be installed before the document exists, so navigation happens
 * here rather than in `beforeEach`.
 */
async function openIssue(page: Page) {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`/${workspaceSlug}/issues/${issueId}`, { waitUntil: "domcontentloaded" });
  await expect(page.getByText(REPLY_BODY, { exact: true })).toBeVisible({ timeout: 30_000 });
}

async function exportToPngFrom(page: Page, ownText: string, otherText: string) {
  const trigger = commentBlock(page, ownText, otherText).getByRole("button", {
    name: "Comment actions",
  });
  await expect(trigger).toBeVisible();
  // A reply's sticky header sits on top of its own action row once the row is
  // scrolled to the top of the viewport, so a pointer click would land on the
  // header. The keyboard reaches the same trigger without that contest.
  await trigger.focus();
  await page.keyboard.press("Enter");

  const item = page.getByRole("menuitem", { name: "Download PNG" });
  await expect(item).toBeVisible();
  await item.focus();
  await page.keyboard.press("Enter");
}

test.describe("Comment actions — Download PNG", () => {
  test("a reply's capture holds that reply, not the thread root", async ({ page }) => {
    const capture = await watchCapture(page);
    await openIssue(page);

    await exportToPngFrom(page, REPLY_BODY, ROOT_BODY);
    const download = await capture.downloaded;

    expect(download.suggestedFilename()).toMatch(/^comment-\d{4}-\d{2}-\d{2}-\d{4}\.png$/);
    expect((await readPng(download)).width).toBeGreaterThan(0);
    const records = await capture.records();
    expect(records).toHaveLength(1);
    expect(records[0]!.text).toContain(REPLY_BODY);
    expect(records[0]!.text).not.toContain(ROOT_BODY);
  });

  test("the thread root's capture holds the root", async ({ page }) => {
    const capture = await watchCapture(page);
    await openIssue(page);

    await exportToPngFrom(page, ROOT_BODY, REPLY_BODY);
    await capture.downloaded;

    const records = await capture.records();
    expect(records).toHaveLength(1);
    expect(records[0]!.text).toContain(ROOT_BODY);
    expect(records[0]!.text).not.toContain(REPLY_BODY);
  });

  test("the capture surface leaves the document once the file is on its way", async ({ page }) => {
    const capture = await watchCapture(page);
    await openIssue(page);

    await exportToPngFrom(page, REPLY_BODY, ROOT_BODY);

    // Downloading first, so an absent surface cannot pass by never opening.
    await capture.downloaded;
    await expect(page.locator(".comment-print-portal")).toHaveCount(0);
    await expect(page.locator(".comment-print-doc")).toHaveCount(0);
    // Cleanup is total: no host node is left in the app either.
    await expect(page.locator("[data-comment-export-root]")).toHaveCount(0);
  });

  test("a reader can export again after the first file is on its way", async ({ page }) => {
    const capture = await watchCapture(page);
    await openIssue(page);

    await exportToPngFrom(page, REPLY_BODY, ROOT_BODY);
    await capture.downloaded;
    await expect(page.locator(".comment-print-portal")).toHaveCount(0);

    // A second wait, not a second await of the first: one promise resolves once,
    // so awaiting `capture.downloaded` again would hand back the *first*
    // download and pass on a file that was never re-exported.
    const second = page.waitForEvent("download", { timeout: 120_000 });
    await exportToPngFrom(page, REPLY_BODY, ROOT_BODY);

    expect((await second).suggestedFilename()).toMatch(/\.png$/);
    await expect
      .poll(async () => (await capture.records()).length, { timeout: 30_000 })
      .toBe(2);
  });

  // The sheet is pinned to light tokens so a dark-mode reader drops a legible
  // image into a chat while the app around it stays dark. The token test guards
  // the stylesheet; only a real browser can say whether the pin still wins the
  // cascade against `.dark` on <html>.
  test("a dark-mode reader gets the light sheet, not the app's dark one", async ({ page }) => {
    const capture = await watchCapture(page);
    await page.emulateMedia({ colorScheme: "dark" });
    await openIssue(page);
    // Without this the case could sit in light mode and prove nothing.
    expect(await page.evaluate(() => document.documentElement.className)).toContain("dark");

    await exportToPngFrom(page, REPLY_BODY, ROOT_BODY);
    await capture.downloaded;

    const records = await capture.records();
    expect(records).toHaveLength(1);
    const app = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    expect(records[0]!.background).not.toBe(app);
    expect(await brightness(page, records[0]!.background)).toBeGreaterThan(
      await brightness(page, app),
    );
  });
});