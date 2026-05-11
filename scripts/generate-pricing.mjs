#!/usr/bin/env node
// Fetch https://models.dev/api.json (the open-source pricing database
// OpenCode uses internally, MIT-licensed), filter it to the LLM
// providers Multica runtimes actually emit, and write a static pricing
// snapshot to packages/views/runtimes/pricing.generated.ts.
//
// Output is keyed by `<provider>/<model>` to match what OpenCode and
// other multi-provider runtimes report on the wire. Only the `cost`
// field of each model entry is preserved.
//
// Usage:
//   node scripts/generate-pricing.mjs                # live fetch
//   MODELS_DEV_PATH=/path/to/api.json node scripts/generate-pricing.mjs
//
// Re-run whenever upstream prices change. The output file is checked in
// so production builds don't depend on `models.dev` being reachable.

import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..");
const OUTPUT_PATH = resolve(
  REPO_ROOT,
  "packages/views/runtimes/pricing.generated.ts",
);

// LLM providers (as keyed in models.dev) whose models Multica runtimes
// surface in usage data. Each entry is paired with the Multica runtime
// kinds that route to it, so the rationale stays close to the data.
//
// Add a provider here only if a Multica runtime can actually emit a
// `<provider>/<model>` pair under it — extra providers just bloat the
// snapshot with markup-laden re-hosts (see 302ai, helicone, etc., which
// we deliberately leave out).
// Providers: anthropic, openai, google, moonshotai, opencode, opencode-go
const ALLOWED_PROVIDERS = [
  "anthropic",
  "openai",
  "google",
  "moonshotai",
  "opencode",
  "opencode-go",
];

async function loadModelsDev() {
  const path = process.env.MODELS_DEV_PATH;
  if (path) return JSON.parse(readFileSync(path, "utf8"));
  const res = await fetch("https://models.dev/api.json");
  if (!res.ok) throw new Error(`models.dev fetch ${res.status}`);
  return await res.json();
}



(async () => {
  const db = await loadModelsDev();

  const skippedProviders = ALLOWED_PROVIDERS.filter((p) => !db[p]);
  if (skippedProviders.length) {
    console.warn(
      "Warning: allowed providers missing from models.dev: " +
        skippedProviders.join(", "),
    );
  }

  const rows = [];
  let pricedCount = 0;
  let skippedNoCost = 0;
  for (const provider of ALLOWED_PROVIDERS) {
    const prov = db[provider];
    if (!prov) continue;
    const ids = Object.keys(prov.models || {}).sort();
    for (const id of ids) {
      const cost = prov.models[id]?.cost;
      if (!cost || (cost.input == null && cost.output == null)) {
        skippedNoCost++;
        continue;
      }
      rows.push({ key: `${provider}/${id}`, cost });
      pricedCount++;
    }
  }
  rows.sort((a, b) => a.key.localeCompare(b.key));

  const today = new Date().toISOString().slice(0, 10);
  const lines = [
    "// AUTO-GENERATED — do not edit by hand.",
    "//",
    "// Source: https://models.dev/api.json (MIT, community-maintained,",
    "// the same dataset OpenCode uses internally).",
    `// Snapshot: ${today}`,
    "// Providers: " + ALLOWED_PROVIDERS.join(", "),
    "//",
    "// Regenerate with: node scripts/generate-pricing.mjs",
    "",
    "// Keys are `<provider>/<model>` to match what OpenCode and other",
    "// multi-provider runtimes report on the wire.",
    "export const PRICING: Readonly<Record<string, {",
    "  input: number;",
    "  output: number;",
    "  cacheRead: number;",
    "  cacheWrite: number;",
    "}>> = {",
  ];

  for (const r of rows) {
    const c = r.cost;
    // Clamp cacheRead to input so estimateCacheSavings never goes negative.
    // models.dev has a few upstream-quirky rows (e.g. gpt-3.5-turbo) where
    // cache_read > input, which would make "money saved by the cache" a
    // negative number. A discount can never cost more than the full rate.
    const cacheRead = Math.min(c.cache_read ?? c.input, c.input);
    const cacheWrite = c.cache_write ?? c.input;
    lines.push(`  ${JSON.stringify(r.key)}: { input: ${c.input}, output: ${c.output}, cacheRead: ${cacheRead}, cacheWrite: ${cacheWrite} },`);
  }
  lines.push("};");
  lines.push("");

  mkdirSync(dirname(OUTPUT_PATH), { recursive: true });
  writeFileSync(OUTPUT_PATH, lines.join("\n"));

  console.log(`Wrote ${OUTPUT_PATH}`);
  console.log(`  Providers included: ${ALLOWED_PROVIDERS.length - skippedProviders.length}`);
  console.log(`  Models with pricing: ${pricedCount}`);
  console.log(`  Models skipped (no cost data): ${skippedNoCost}`);
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
