import { afterEach, describe, expect, it, vi } from "vitest";
import {
  PRINT_SURFACE_READY_TIMEOUT_MS,
  isPrintSurfaceReady,
  waitForPrintSurfaceReady,
} from "./comment-print";

function surface(html: string): HTMLElement {
  const el = document.createElement("div");
  el.innerHTML = html;
  return el;
}

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
});

describe("isPrintSurfaceReady", () => {
  it("is ready when nothing is still filling in", () => {
    expect(isPrintSurfaceReady(surface("<p>body</p>"))).toBe(true);
  });

  it("is not ready while a rich block is still behind its mount gate", () => {
    expect(isPrintSurfaceReady(surface('<div data-rich-block-shell></div>'))).toBe(false);
  });

  it("is not ready while a diagram is still rendering", () => {
    expect(
      isPrintSurfaceReady(surface("<div data-dynamic-block-skeleton></div>")),
    ).toBe(false);
  });

  it("is ready once both have settled", () => {
    expect(
      isPrintSurfaceReady(
        surface(
          '<div data-rich-block-shell data-mounted><div class="rich-text-editor">body</div></div>',
        ),
      ),
    ).toBe(true);
  });

  it("ignores pending blocks outside the surface", () => {
    const root = surface("<p>body</p>");
    document.body.appendChild(surface('<div data-rich-block-shell></div>'));
    expect(isPrintSurfaceReady(root)).toBe(true);
  });
});

describe("waitForPrintSurfaceReady", () => {
  it("resolves true straight away when the surface is already complete", async () => {
    await expect(waitForPrintSurfaceReady(surface("<p>body</p>"))).resolves.toBe(true);
  });

  it("resolves true once the last pending block mounts", async () => {
    vi.useFakeTimers();
    const root = surface('<div data-rich-block-shell></div>');
    const pending = waitForPrintSurfaceReady(root);
    root.querySelector("div")!.setAttribute("data-mounted", "");
    await vi.advanceTimersByTimeAsync(60);
    await expect(pending).resolves.toBe(true);
  });

  it("resolves false when nothing settles inside the default budget", async () => {
    vi.useFakeTimers();
    const pending = waitForPrintSurfaceReady(surface('<div data-rich-block-shell></div>'));
    await vi.advanceTimersByTimeAsync(PRINT_SURFACE_READY_TIMEOUT_MS + 60);
    await expect(pending).resolves.toBe(false);
  });

  it("gives up on a stuck diagram so the comment still prints", async () => {
    vi.useFakeTimers();
    const pending = waitForPrintSurfaceReady(
      surface("<div data-dynamic-block-skeleton></div>"),
      200,
    );
    await vi.advanceTimersByTimeAsync(260);
    await expect(pending).resolves.toBe(false);
  });
});
