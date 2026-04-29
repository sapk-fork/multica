import { describe, it, expect, afterEach } from "vitest";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import type { RuntimeUsage } from "@multica/core/types";
import {
  aggregateCostByModel,
  collectUnmappedModels,
  estimateCost,
  estimateCostBreakdown,
  isModelPriced,
} from "./utils";

// Build a one-million-token usage row so estimateCost output equals the
// per-MTok rate directly — makes pricing assertions readable.
function usage(overrides: Partial<RuntimeUsage>): RuntimeUsage {
  return {
    runtime_id: "rt-1",
    date: "2026-04-01",
    provider: "",
    model: "",
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    ...overrides,
  };
}

const ONE_M = 1_000_000;

afterEach(() => {
  // Reset overrides so tests don't bleed pricing state into one another.
  useCustomPricingStore.setState({ pricings: {} });
});

const zeroUsage = usage({});

const ONE_M = 1_000_000;

describe("resolvePricing / isModelPriced", () => {
  it("matches full provider/model keys exactly", () => {
    expect(isModelPriced("anthropic/claude-sonnet-4-5")).toBe(true);
  });

  it("matches models with trailing date suffix via startsWith on date-stripped name", () => {
    expect(isModelPriced("anthropic/claude-sonnet-4-5-20250929")).toBe(true);
  });

  it("matches a date-suffixed `provider/model-YYYYMMDD` form", () => {
    expect(isModelPriced("anthropic/claude-sonnet-4-5-20250929")).toBe(true);
    expect(isModelPriced("openai/gpt-4o-2024-08-06")).toBe(true);
  });

  it("matches popular OpenAI models reported by OpenCode", () => {
    for (const m of [
      "openai/gpt-4o",
      "openai/gpt-4o-mini",
      "openai/gpt-4.1",
      "openai/o1",
      "openai/o3-mini",
      "openai/o4-mini",
    ]) {
      expect(isModelPriced(m), m).toBe(true);
    }
  });

  it("matches popular Google Gemini models reported by OpenCode", () => {
    for (const m of [
      "google/gemini-2.5-pro",
      "google/gemini-2.5-flash",
      "google/gemini-2.0-flash",
    ]) {
      expect(isModelPriced(m), m).toBe(true);
    }
  });

  it("returns false for xAI and DeepSeek models (not in pricing table)", () => {
    for (const m of [
      "xai/grok-4",
      "xai/grok-3-mini",
      "deepseek/deepseek-chat",
      "deepseek/deepseek-reasoner",
    ]) {
      expect(isModelPriced(m), m).toBe(false);
    }
  });

  it("returns undefined / false for genuinely unknown provider/model", () => {
    expect(isModelPriced("madeup/totally-not-a-real-model")).toBe(false);
    expect(isModelPriced("openai/this-is-not-a-real-model-xyzzy")).toBe(false);
  });

  it("treats empty strings as unknown", () => {
    expect(isModelPriced("")).toBe(false);
  });
});

describe("estimateCost — OpenCode provider/model parity", () => {
  // The "Anthropic-via-OpenCode" parity case from the acceptance criteria:
  // routing the same model through OpenCode must not change billing.
  it("returns accurate cost for anthropic/claude-sonnet-4-5", () => {
    const tokens = {
      input_tokens: 1_000_000,
      output_tokens: 500_000,
      cache_read_tokens: 200_000,
      cache_write_tokens: 50_000,
    };
    const cost = estimateCost(usage({ model: "anthropic/claude-sonnet-4-5", ...tokens }));
    expect(cost).toBeGreaterThan(0);
  });

  it("returns a non-zero, accurate cost for openai/gpt-4o", () => {
    // OpenAI gpt-4o published rates (per 1M tokens):
    //   input  $2.50
    //   output $10.00
    // 1M input + 1M output should be exactly $12.50 — assert tightly so any
    // accidental decimal-point shift in MODEL_PRICING fails this test.
    const cost = estimateCost(
      usage({
        model: "openai/gpt-4o",
        input_tokens: ONE_M,
        output_tokens: ONE_M,
      }),
    );
    expect(cost).toBeCloseTo(12.5, 6);
  });

  it("returns a non-zero, accurate cost for google/gemini-2.5-pro", () => {
    // Google Gemini 2.5 Pro (≤200K-context tier, the OpenCode default):
    //   input  $1.25
    //   output $10.00
    const cost = estimateCost(
      usage({
        model: "google/gemini-2.5-pro",
        input_tokens: ONE_M,
        output_tokens: ONE_M,
      }),
    );
    expect(cost).toBeCloseTo(11.25, 6);
  });

  it("deepseek/deepseek-chat is not priced (not in pricing table)", () => {
    const cost = estimateCost(
      usage({
        model: "deepseek/deepseek-chat",
        input_tokens: ONE_M,
        output_tokens: ONE_M,
      }),
    );
    expect(cost).toBe(0);
  });

  it("matches Anthropic's `claude-3-5-haiku-latest` id format", () => {
    // Anthropic's actual model ids use `claude-3-5-haiku-…`, not
    // `claude-haiku-3-5-…`. Earlier the table keyed off the latter and
    // returned $0 for OpenCode's `anthropic/claude-3-5-haiku-latest`.
    const cost = estimateCost(
      usage({
        model: "anthropic/claude-3-5-haiku-latest",
        input_tokens: ONE_M,
        output_tokens: ONE_M,
      }),
    );
    expect(cost).toBeCloseTo(0.8 + 4, 6);
  });

  it("breakdown sums match the total cost", () => {
    const u = usage({
      model: "openai/gpt-4o-mini",
      input_tokens: 750_000,
      output_tokens: 250_000,
      cache_read_tokens: 100_000,
      cache_write_tokens: 50_000,
    });
    const total = estimateCost(u);
    const b = estimateCostBreakdown(u);
    expect(b.input + b.output + b.cacheRead + b.cacheWrite).toBeCloseTo(total, 10);
    expect(total).toBeGreaterThan(0);
  });
});

describe("collectUnmappedModels", () => {
  it("returns an empty list for supported providers (excludes xAI and DeepSeek)", () => {
    const rows = [
      usage({ model: "anthropic/claude-sonnet-4-5", input_tokens: 1 }),
      usage({ model: "openai/gpt-4o", input_tokens: 1 }),
      usage({ model: "google/gemini-2.5-pro", input_tokens: 1 }),
    ];
    expect(collectUnmappedModels(rows)).toEqual([]);
  });

  it("still flags genuinely unknown models", () => {
    const rows = [
      usage({ model: "anthropic/claude-sonnet-4-5", input_tokens: 1 }),
      usage({ model: "noprovider/madeup-model-xyzzy", input_tokens: 1 }),
    ];
    expect(collectUnmappedModels(rows)).toEqual(["noprovider/madeup-model-xyzzy"]);
  });
});

describe("user-supplied custom pricing", () => {
  it("prices a model the maintained catalog doesn't ship", () => {
    useCustomPricingStore.getState().setCustomPricing("gpt-5.5-mini", {
      input: 1,
      output: 4,
      cacheRead: 0.1,
      cacheWrite: 1,
    });
    expect(isModelPriced("gpt-5.5-mini")).toBe(true);
    expect(
      estimateCost({
        ...zeroUsage,
        model: "gpt-5.5-mini",
        input_tokens: 1_000_000,
        output_tokens: 1_000_000,
      }),
    ).toBeCloseTo(5, 5);
  });

  it("does NOT shadow the maintained catalog when both define the same model", () => {
    // Catalog wins so a user can't accidentally over-charge themselves for
    // a model we already track (and so a stale local override doesn't
    // silently disagree with what the dashboard shows everyone else).
    useCustomPricingStore.getState().setCustomPricing("claude-sonnet-4-6", {
      input: 999,
      output: 999,
      cacheRead: 999,
      cacheWrite: 999,
    });
    expect(
      estimateCost({
        ...zeroUsage,
        model: "claude-sonnet-4-6",
        input_tokens: 1_000_000,
      }),
    ).toBeCloseTo(3, 5); // maintained input rate, not the 999 override
  });

  it("falls back to a stripped dated snapshot in the custom store", () => {
    useCustomPricingStore.getState().setCustomPricing("brand-new-model", {
      input: 2,
      output: 8,
      cacheRead: 0.2,
      cacheWrite: 2,
    });
    expect(
      estimateCost({
        ...zeroUsage,
        model: "brand-new-model-2026-04-01",
        input_tokens: 1_000_000,
      }),
    ).toBeCloseTo(2, 5);
  });

  it("removeCustomPricing clears the override", () => {
    const store = useCustomPricingStore.getState();
    store.setCustomPricing("gpt-5.5-mini", {
      input: 1,
      output: 4,
      cacheRead: 0.1,
      cacheWrite: 1,
    });
    expect(isModelPriced("gpt-5.5-mini")).toBe(true);
    useCustomPricingStore.getState().removeCustomPricing("gpt-5.5-mini");
    expect(isModelPriced("gpt-5.5-mini")).toBe(false);
  });

  it("priced + unpriced models in the same window produce a mixed-cost aggregate", () => {
    // The partial-unmapping case: chart renders normally because some
    // models are priced, but the unmapped ones silently contribute $0 if
    // we don't surface them. Confirm aggregateCostByModel exposes both
    // sides so the UI can show a notice for the gap.
    const rows = [
      {
        ...zeroUsage,
        model: "claude-sonnet-4-6",
        input_tokens: 1_000_000,
        date: "2026-01-01",
        provider: "anthropic",
        agent_count: 1,
      },
      {
        ...zeroUsage,
        model: "fictional-model-x",
        input_tokens: 1_000_000,
        date: "2026-01-01",
        provider: "fictional",
        agent_count: 1,
      },
    ];
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const byModel = aggregateCostByModel(rows as any);
    const sonnet = byModel.find((r) => r.key === "claude-sonnet-4-6");
    const fictional = byModel.find((r) => r.key === "fictional-model-x");
    expect(sonnet?.cost).toBeCloseTo(3, 5);
    expect(fictional?.cost).toBe(0);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(collectUnmappedModels(rows as any)).toEqual(["fictional-model-x"]);
  });

  it("aggregateCostByModel reflects a newly-saved custom price on re-call with the same input", () => {
    // Regression for the memo-dependency bug GPT-Boy flagged: aggregate
    // helpers must give different answers before vs after a price save,
    // otherwise child components (WhenChart / CostByBlock / ActivityHeatmap)
    // that memo on query data alone keep showing pre-save totals.
    const rows = [
      {
        ...zeroUsage,
        model: "fictional-model-x",
        input_tokens: 1_000_000,
        date: "2026-01-01",
        provider: "fictional",
        agent_count: 1,
      },
    ];
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const before = aggregateCostByModel(rows as any);
    expect(before[0]?.cost).toBe(0);

    useCustomPricingStore.getState().setCustomPricing("fictional-model-x", {
      input: 2,
      output: 8,
      cacheRead: 0.2,
      cacheWrite: 2,
    });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const after = aggregateCostByModel(rows as any);
    expect(after[0]?.cost).toBeCloseTo(2, 5);
  });
});
