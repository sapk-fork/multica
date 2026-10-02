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

  it("leaves light mode to light mode", () => {
    // `color-scheme` is not a custom property, so it is outside the block above.
    expect(topLevelRule(readFileSync(PRINT_CSS, "utf8"), ".comment-print-doc")).toContain(
      "color-scheme: light",
    );
  });
});
