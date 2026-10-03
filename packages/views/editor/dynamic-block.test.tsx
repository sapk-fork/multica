import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("../i18n", async () => {
  const editor = (await import("../locales/en/editor.json")).default;
  return {
    useT: () => ({ t: (select: (bundle: typeof editor) => string) => select(editor) }),
  };
});

vi.mock("./code-block-static", () => ({
  CodeBlockStatic: ({ body }: { body: string }) => <pre>{body}</pre>,
}));

const { copyTextMock } = vi.hoisted(() => ({ copyTextMock: vi.fn() }));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: copyTextMock }));

import { DynamicBlock, DynamicBlockSkeleton } from "./dynamic-block";

afterEach(() => vi.restoreAllMocks());

function renderBlock() {
  return render(
    <DynamicBlock
      kind="html"
      title="Latency"
      source="<p>chart</p>"
      preview={() => <div data-testid="content">chart</div>}
    />,
  );
}

describe("DynamicBlock", () => {
  it("collapses content taller than 480px behind Show all", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(900);
    const { container } = renderBlock();

    const body = container.querySelector<HTMLElement>("[data-collapsed]");
    expect(body).not.toBeNull();
    expect(body!.style.maxHeight).toBe("480px");

    fireEvent.click(screen.getByRole("button", { name: "Show all" }));
    expect(container.querySelector("[data-collapsed]")).toBeNull();
    expect(screen.queryByRole("button", { name: "Show all" })).toBeNull();
  });

  it("leaves content that fits alone", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(300);
    const { container } = renderBlock();
    expect(container.querySelector("[data-collapsed]")).toBeNull();
    expect(screen.queryByRole("button", { name: "Show all" })).toBeNull();
  });

  // The print stylesheet un-collapses `[data-collapsed]` and hides what is left
  // over: the "Show all" button and the gradient it fades with. Both are
  // selected by attribute, so both need one that is not a Tailwind class.
  it("marks the collapse gradient so a print stylesheet can find it", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(900);
    const { container } = renderBlock();

    const body = container.querySelector<HTMLElement>("[data-collapsed]")!;
    expect(body.querySelector("[data-fade]")).not.toBeNull();
    expect(body.querySelector("[data-fade]")).toBe(
      body.querySelector("button")!.previousElementSibling,
    );
  });

  // The comment PDF export waits for this before printing, so it polls for a
  // durable hook rather than for a Tailwind class it does not own.
  it("marks the skeleton a print surface waits on", () => {
    const { container } = render(<DynamicBlockSkeleton />);
    expect(container.querySelector("[data-dynamic-block-skeleton]")).not.toBeNull();
  });

  // The comment PNG export keeps this bar and strips it: the title is content,
  // the icon and the preview/source tabs are ours. The stylesheet has to name
  // something durable rather than a Tailwind class it does not own, and the
  // capture stylesheet's own assertions fail if the two ever drift apart — so
  // this is the half that says the four things it names exist at all.
  it("marks the title, and the chrome the capture strips, on the title bar", () => {
    const { container } = renderBlock();

    const header = container.querySelector<HTMLElement>("[data-dynamic-block-header]");
    expect(header).not.toBeNull();
    // Kept: the title, here "Latency".
    expect(header!.textContent).toContain("Latency");
    // Stripped: the icon beside it, the actions, and the view tabs.
    expect(header!.querySelector(":scope > svg")).not.toBeNull();
    expect(header!.querySelector("[data-dynamic-block-actions]")).not.toBeNull();
    expect(header!.querySelector("[role='tablist']")).not.toBeNull();
  });

  it("copies the fence source", async () => {
    copyTextMock.mockResolvedValue(true);
    renderBlock();
    fireEvent.click(screen.getByRole("button", { name: "Copy source" }));
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("<p>chart</p>"));
  });

  it("reveals the actions on hover, but keeps them up in the source view and after a copy", async () => {
    copyTextMock.mockResolvedValue(true);
    const { container } = renderBlock();
    const actions = container.querySelector<HTMLElement>("[data-dynamic-block-actions]")!;
    const hiddenUntilHover = "[@media(hover:hover)]:opacity-0";

    // The title always shows; only the actions wait for hover or focus.
    expect(actions.textContent).not.toContain("Latency");
    expect(actions.className).toContain(hiddenUntilHover);
    expect(actions.className).toContain("group-hover/dynamic-block:opacity-100");
    expect(actions.className).toContain("group-focus-within/dynamic-block:opacity-100");

    // Preview is the way back from the source view, so it cannot hide.
    fireEvent.click(screen.getByRole("tab", { name: "Source" }));
    await waitFor(() => expect(actions.className).not.toContain(hiddenUntilHover));
    fireEvent.click(screen.getByRole("tab", { name: "Preview" }));
    await waitFor(() => expect(actions.className).toContain(hiddenUntilHover));

    // The copy confirmation stays readable after the pointer leaves.
    fireEvent.click(screen.getByRole("button", { name: "Copy source" }));
    await waitFor(() => expect(actions.className).not.toContain(hiddenUntilHover));
  });

  it("has no fullscreen button unless the kind offers one", () => {
    renderBlock();
    expect(screen.queryByRole("button", { name: "Fullscreen" })).toBeNull();
  });
});
