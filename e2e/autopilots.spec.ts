import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi, reloadAppPage } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Autopilot max concurrent runs (M-87)", () => {
  let api: TestApiClient;
  let workspaceSlug: string;
  let agentId: string;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    workspaceSlug = await loginAsDefault(page);
    agentId = await api.createAgent("E2E Concurrency Agent " + Date.now());
  });

  test.afterEach(async () => {
    if (api) {
      await api.cleanup();
    }
  });

  // Regression pin for M-87: the dialog must default new/existing autopilots
  // to 0 (unlimited) and swap the hint text the moment a real cap is entered,
  // so a user setting a limit gets immediate feedback on what it does.
  test("dialog shows 0=unlimited by default and swaps hint text once a cap is set", async ({
    page,
  }) => {
    const autopilot = await api.createAutopilot(
      "E2E Concurrency Dialog " + Date.now(),
      agentId,
    );

    await page.goto(`/${workspaceSlug}/autopilots/${autopilot.id}`, {
      waitUntil: "domcontentloaded",
    });
    await expect(page.getByRole("button", { name: "Edit" })).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Edit" }).click();

    const dialog = page.getByRole("dialog");
    const input = dialog.getByRole("spinbutton", { name: "Max concurrent runs" });
    await expect(input).toBeVisible();
    await expect(input).toHaveValue("0");
    await expect(
      dialog.getByText("0 means unlimited — runs are never skipped for concurrency."),
    ).toBeVisible();

    await input.fill("2");
    await expect(
      dialog.getByText("New runs are skipped while this many runs are already in progress."),
    ).toBeVisible();

    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Autopilot updated")).toBeVisible({ timeout: 15000 });

    // Persisted, not just local state — reload and re-open to confirm.
    await reloadAppPage(page);
    await page.getByRole("button", { name: "Edit" }).click();
    await expect(
      page.getByRole("dialog").getByRole("spinbutton", { name: "Max concurrent runs" }),
    ).toHaveValue("2");
  });

  // Regression pin for M-87: once active runs (issue_created/running) meet
  // max_concurrent_runs, "Run now" must not start another run — it should
  // report the run as blocked instead of silently piling on a doomed run.
  test("Run now is blocked once active runs meet the concurrency cap", async ({ page }) => {
    const autopilot = await api.createAutopilot(
      "E2E Concurrency Dispatch " + Date.now(),
      agentId,
      { max_concurrent_runs: 1 },
    );
    await api.seedAutopilotRun(autopilot.id, "running");

    await page.goto(`/${workspaceSlug}/autopilots/${autopilot.id}`, {
      waitUntil: "domcontentloaded",
    });

    const runNowButton = page.getByRole("button", { name: "Run now" });
    await expect(runNowButton).toBeVisible({ timeout: 15000 });
    await runNowButton.click();

    await expect(page.getByText("Not triggered — the run was blocked")).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByText("Autopilot triggered")).not.toBeVisible();
  });
});
