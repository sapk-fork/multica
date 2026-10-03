import "./env";

import { expect, test, type Page } from "@playwright/test";

import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

/**
 * "Download PDF" hands one comment to the browser's own print pipeline.
 *
 * `window.print` is stubbed through `addInitScript`: headless Chromium has no
 * print dialog to dismiss, and a real one would block the test. The stub
 * captures what the print surface actually put on the page, which is what this
 * file can honestly prove — the surface holds exactly the comment whose menu was
 * opened, and it is gone from the document once it closes.
 *
 * It proves nothing about the sheet a reader gets out of the dialog:
 * pagination, `@page` margins, print colour adjustment and light-mode tokens
 * are only observable in a real print preview. That stays the manual check.
 */

const ROOT_BODY = "Root comment body, the thread opener, never the printed one.";
const REPLY_BODY = "Reply comment body, the only body that should reach print.";

interface PrintCapture {
  count: number;
  /** One entry per `window.print()` call; `null` means no surface was on the page. */
  texts: (string | null)[];
}

/**
 * @param fireAfterPrint `true` reproduces a blocking engine (Chromium, Firefox):
 * `afterprint` arrives while `window.print()` has not returned yet. `false`
 * reproduces one that never fires it, which is the path the bounded fallback
 * exists for.
 */
async function stubPrint(page: Page, fireAfterPrint: boolean) {
  await page.addInitScript((fires) => {
    const capture: PrintCapture = { count: 0, texts: [] };
    (window as unknown as { __printCapture: PrintCapture }).__printCapture = capture;
    window.print = () => {
      const doc = document.querySelector(".comment-print-doc");
      capture.count += 1;
      capture.texts.push(doc ? doc.textContent : null);
      if (fires) window.dispatchEvent(new Event("afterprint"));
    };
  }, fireAfterPrint);
}

function readCapture(page: Page) {
  return page.evaluate(
    () => (window as unknown as { __printCapture?: PrintCapture }).__printCapture,
  );
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

/**
 * The stub has to be installed before the document exists, so navigation
 * happens here rather than in `beforeEach`.
 */
async function openIssue(page: Page, fireAfterPrint: boolean) {
  // `addInitScript` only reaches documents created after it runs, so the stub
  // goes in before the navigation rather than after it.
  await stubPrint(page, fireAfterPrint);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`/${workspaceSlug}/issues/${issueId}`, { waitUntil: "domcontentloaded" });
  await expect(page.getByText(REPLY_BODY, { exact: true })).toBeVisible({ timeout: 30_000 });
}

async function exportToPdfFrom(page: Page, ownText: string, otherText: string) {
  const trigger = commentBlock(page, ownText, otherText).getByRole("button", {
    name: "Comment actions",
  });
  await expect(trigger).toBeVisible();
  // A reply's sticky header sits on top of its own action row once the row is
  // scrolled to the top of the viewport, so a pointer click would land on the
  // header. The keyboard reaches the same trigger without that contest.
  await trigger.focus();
  await page.keyboard.press("Enter");

  const item = page.getByRole("menuitem", { name: "Download PDF" });
  await expect(item).toBeVisible();
  await item.focus();
  await page.keyboard.press("Enter");
}

let api: TestApiClient;
let issueId: string;
let workspaceSlug: string;

test.beforeEach(async ({ page }) => {
  api = await createTestApi();
  const issue = await api.createIssue(`E2E comment print export ${Date.now()}`);
  issueId = issue.id;
  const root = await api.createComment(issueId, ROOT_BODY);
  await api.createComment(issueId, REPLY_BODY, root.id);
  workspaceSlug = await loginAsDefault(page);
});

test.afterEach(async () => {
  if (api) await api.cleanup();
});

test.describe("Comment actions — Download PDF", () => {
  test("a reply's print surface holds that reply, not the thread root", async ({ page }) => {
    await openIssue(page, true);

    await exportToPdfFrom(page, REPLY_BODY, ROOT_BODY);

    await expect
      .poll(async () => (await readCapture(page))?.count ?? 0, { timeout: 15_000 })
      .toBe(1);
    const capture = await readCapture(page);
    expect(capture?.texts[0]).toContain(REPLY_BODY);
    expect(capture?.texts[0]).not.toContain(ROOT_BODY);
  });

  test("the thread root's print surface holds the root", async ({ page }) => {
    await openIssue(page, true);

    await exportToPdfFrom(page, ROOT_BODY, REPLY_BODY);

    await expect
      .poll(async () => (await readCapture(page))?.count ?? 0, { timeout: 15_000 })
      .toBe(1);
    const capture = await readCapture(page);
    expect(capture?.texts[0]).toContain(ROOT_BODY);
    expect(capture?.texts[0]).not.toContain(REPLY_BODY);
  });

  test("the print surface leaves the document once the dialog returns", async ({ page }) => {
    await openIssue(page, true);

    await exportToPdfFrom(page, REPLY_BODY, ROOT_BODY);

    // Printing first, so an absent surface cannot pass this by never opening.
    await expect
      .poll(async () => (await readCapture(page))?.count ?? 0, { timeout: 15_000 })
      .toBe(1);
    await expect(page.locator(".comment-print-portal")).toHaveCount(0);
    await expect(page.locator(".comment-print-doc")).toHaveCount(0);
  });

  test("an engine that never fires afterprint still takes the surface down", async ({ page }) => {
    await openIssue(page, false);

    await exportToPdfFrom(page, REPLY_BODY, ROOT_BODY);

    // Bounded fallback, so this holds without a dialog to dismiss it.
    await expect(page.locator(".comment-print-portal")).toHaveCount(0, { timeout: 15_000 });
    const capture = await readCapture(page);
    expect(capture?.count).toBe(1);
    expect(capture?.texts[0]).toContain(REPLY_BODY);
    expect(capture?.texts[0]).not.toContain(ROOT_BODY);
  });
});