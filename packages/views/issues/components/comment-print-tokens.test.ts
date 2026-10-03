// @vitest-environment node

/**
 * The print surface re-pins the app's colour tokens so a dark-mode reader still
 * exports a legible page. That block is hand-copied out of `tokens.css`, and a
 * hand-copied block rots silently: a token added to `.dark` later keeps printing
 * dark, and nothing complains. These assertions are the contract that keeps it
 * honest — every token dark mode overrides is re-pinned here, to the exact light
 * value.
 *
 * A relative path, not `@multica/ui/styles/tokens.css`: this asserts a property
 * of two source files, and reading one of them off disk is what the property is
 * about. Nothing is imported from the package.
 */

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const TOKENS_CSS = new URL("../../../ui/styles/tokens.css", import.meta.url);
const PRINT_CSS = new URL("./comment-print.css", import.meta.url);

/** Declaration body of the first top-level rule with this exact selector. */
function topLevelRule(css: string, selector: string): string {
  const open = css.indexOf(`\n${selector} {`);
  expect(open, `no top-level \`${selector} {}\` rule`).toBeGreaterThan(-1);
  let depth = 0;
  for (let i = css.indexOf("{", open); i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(css.indexOf("{", open) + 1, i);
  }
  throw new Error(`unbalanced braces after \`${selector}\``);
}

/**
 * Declaration body of the top-level rule whose selector list mentions `token`.
 *
 * `topLevelRule` needs the rule's *first* selector, which a comma-separated list
 * does not give you, and the opening brace of such a rule sits after every one of
 * its selectors rather than after the first — so the search starts at the end of
 * the token's own line. The sheet has no nested rules, so the closing brace is
 * unambiguous.
 */
/** `name -> value` for every custom property in a declaration body. */
function customProperties(body: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    out.set(m[1]!, m[2]!.replace(/\s+/g, " ").trim());
  }
  return out;
}

const tokens = readFileSync(TOKENS_CSS, "utf8");
const light = customProperties(topLevelRule(tokens, ":root"));
const dark = customProperties(topLevelRule(tokens, ".dark"));
const pinned = customProperties(topLevelRule(readFileSync(PRINT_CSS, "utf8"), ".comment-print-doc"));

describe("comment-print.css light token block", () => {
  it("re-pins every token dark mode overrides", () => {
    const missed = [...dark.keys()].filter((name) => !pinned.has(name));

    expect(missed).toEqual([]);
  });

  it("pins each one to the exact light value", () => {
    const drifted = [...dark.keys()]
      .map((name) => `${name}: print=${pinned.get(name)!} light=${light.get(name)!}`)
      .filter((line) => {
        const [, print, lit] = line.split(/ print=(.*) light=(.*)$/);
        return print !== lit;
      });

    expect(drifted).toEqual([]);
  });

  it("pins no token dark mode leaves alone", () => {
    // A pinned name `.dark` never touches is a copy nothing maintains, and one
    // that will drift from `:root` without this test noticing.
    const extra = [...pinned.keys()].filter((name) => name.startsWith("--") && !dark.has(name));

    expect(extra).toEqual([]);
  });

  it("carries no print-pipeline rules", () => {
    // M-125 pivoted from `window.print()` to a PNG capture (sapk drops the file
    // into Slack, which renders an image inline). `@page`, `@media print` and
    // `print-color-adjust` only ever governed a print dialog this surface no
    // longer opens; left in place they read as a live mechanism and quietly rot
    // against markup that is no longer printed.
    const css = readFileSync(PRINT_CSS, "utf8");

    expect(css).not.toContain("@media print");
    expect(css).not.toContain("@page");
    expect(css).not.toContain("print-color-adjust");
  });

  it("leaves light mode to light mode", () => {
    // `color-scheme` is not a custom property, so it is outside the block above.
    expect(topLevelRule(readFileSync(PRINT_CSS, "utf8"), ".comment-print-doc")).toContain(
      "color-scheme: light",
    );
  });

  it("releases a rich block's reserved height", () => {
    // `LazyRichBlock` keeps its `min-height` after mount so the page never
    // shrinks back and re-triggers a measurement pass. On screen that
    // reservation is deliberate; in a fixed-size image it is dead pixels, and a
    // diagram drawing 120px inside a 280px box shipped a third of its height as
    // white. Nothing else fails if the release is dropped — the export stays
    // green and merely wastes the reader's Slack column — so it is pinned here.
    const css = readFileSync(PRINT_CSS, "utf8");

    expect(topLevelRule(css, ".comment-print-doc [data-rich-block-shell]")).toContain(
      "min-height: 0",
    );
  });

  it("keeps a framed block's title, which is content", () => {
    // `package.json` above a config block tells the reader what they are
    // looking at. The bar was hidden whole for a while, which took the title
    // with it — this pins the title's survival, since the rest of the bar (icon,
    // preview/source tabs) is still ours to drop.
    const css = readFileSync(PRINT_CSS, "utf8");

    // Hidden as a list item, or as a rule of its own: either shape takes the
    // title down with the icon.
    expect(css).not.toContain(".comment-print-doc [data-dynamic-block-header],");
    expect(css).not.toContain(".comment-print-doc [data-dynamic-block-header] {");
    // The bar's own controls still go.
    expect(css).toContain(".comment-print-doc [data-dynamic-block-actions],");
  });

  it("drops the icon, the view tabs and the library chip out of the bar it keeps", () => {
    // Three things are ours: the icon, the preview/source tabs, and the chip
    // that names our rendering library beside the author's own title. The title
    // itself is content and stays — see the assertion above.
    const css = readFileSync(PRINT_CSS, "utf8");

    expect(
      topLevelRule(css, ".comment-print-doc [data-dynamic-block-header] > svg"),
    ).toContain("display: none");
    expect(
      topLevelRule(css, '.comment-print-doc [data-dynamic-block-header] [role="tablist"]'),
    ).toContain("display: none");
    expect(
      topLevelRule(
        css,
        ".comment-print-doc [data-dynamic-block-header] [data-dynamic-block-kind]",
      ),
    ).toContain("display: none");
  });
});
