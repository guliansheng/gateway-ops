import assert from "node:assert/strict"
import { test } from "node:test"
import type { PublicModelPricingItem, PublicModelPricingView } from "./api-types"
// Node's native TypeScript runner requires the extension; the app tsconfig forbids TS extensions.
// @ts-expect-error -- this test runs directly without emitting JavaScript.
import { formatModelPrice, pricingColumns, pricingKind, pricingReferences, pricingValue, publicPricingGroups, publicPricingItems, referenceLabel } from "./model-pricing.ts"

function item(overrides: Partial<PublicModelPricingItem> = {}): PublicModelPricingItem {
  return {
    model: "public-model", company: "DeepSeek", billing_mode: "token", price_source: "channel_override",
    channel_id: 1, channel_name: "渠道甲", station_id: 1, station_name: "测试站", groups: ["Z-优先"],
    ...overrides,
  }
}

function view(items: PublicModelPricingItem[]): PublicModelPricingView {
  return {
    station_id: 1, station_name: "测试站", updated_at: "2026-01-01T00:00:00Z", items,
    public_groups: [{ name: "Z-优先", rate_multiplier: 3 }, { name: "B-同序较小ID", rate_multiplier: 4 }, { name: "A-同序较大ID", rate_multiplier: 1 }],
    summary: { models: 0, companies: 0, channels: 0, companies_list: [], platforms: [], billing_modes: [] },
  }
}

test("public group UI preserves API sort_order/external_id order without alphabet/rate sorting", () => {
  const data = view([])
  assert.deepEqual(publicPricingGroups(data).map((group) => group.name), ["Z-优先", "B-同序较小ID", "A-同序较大ID"])
  assert.equal(data.public_groups?.[0].name, "Z-优先")
})

test("public request billing is restricted to independent group image prices", () => {
  const data = view([
    item(),
    item({ model: "generic-request", billing_mode: "per_request", per_request_price: 0.4 }),
    item({ model: "channel-image", billing_mode: "image", per_request_price: 0.5 }),
    item({ model: "paint-alias", billing_mode: "per_request", price_source: "group_image_pricing", image_price_1k: 0, image_price_2k: 0.02, image_price_4k: 0.04 }),
    item({ model: "hidden-model", groups: ["专属"] }),
  ])
  const items = publicPricingItems(data)
  assert.deepEqual(items.map((entry) => entry.model), ["public-model", "paint-alias"])
  assert.deepEqual(items.map(pricingKind), ["token", "request"])
})

test("group image prices have 1K/2K/4K columns, no original price and no rate multiplication", () => {
  const image = item({
    billing_mode: "per_request", price_source: "group_image_pricing", image_price_1k: 0, image_price_2k: 0.02, image_price_4k: 0.04,
    // Guard even against stale/malformed reference data reaching the UI.
    official_pricing: { input_price: 0.000005, image_price_2k: 999 }, official_pricing_source: "channel_pricing",
  })
  const group = { name: "Z-优先", rate_multiplier: 10 }
  assert.deepEqual(pricingReferences([image]), [])
  assert.equal(pricingValue(image, "image_price_2k"), null)
  assert.equal(pricingValue(image, "image_price_1k", group), 0)
  assert.equal(pricingValue(image, "image_price_2k", group), 0.02)
  assert.equal(pricingValue(image, "image_price_4k", group), 0.04)
  assert.equal(pricingValue(image, "image_price_2k", { ...group, name: "其他分组" }), null)
  assert.deepEqual(pricingColumns([image]).map((column) => [column.key, column.perRequest]), [["image_price_1k", true], ["image_price_2k", true], ["image_price_4k", true]])
  assert.equal(formatModelPrice(pricingValue(image, "image_price_2k", group), true), "$0.02")
})

test("saved channel token units render 1.5/4.5 USD per million, not 1500000/4500000", () => {
  // Upstream admin types.ts mTokToPerToken saves the form's 1.5/4.5 as 1.5e-6/4.5e-6.
  const channel = item({ input_price: 1.5e-6, output_price: 4.5e-6, cache_read_price: 0, official_pricing: { input_price: 1.5e-6, output_price: 4.5e-6, cache_read_price: 0 }, official_pricing_source: "channel_pricing" })
  assert.equal(formatModelPrice(pricingValue(channel, "input_price")), "$1.5")
  assert.equal(formatModelPrice(pricingValue(channel, "output_price")), "$4.5")
  assert.equal(formatModelPrice(pricingValue(channel, "cache_read_price")), "$0")
  assert.equal(pricingValue(channel, "cache_write_price"), null)
  assert.equal(formatModelPrice(null), "—")
  assert.equal(formatModelPrice(pricingValue(channel, "input_price", { name: "Z-优先", rate_multiplier: 3 })), "$4.5")
  assert.equal(referenceLabel(channel), "渠道设置原价")
})

test("different channels retain their own original prices and source labels", () => {
  const first = item({ official_pricing: { input_price: 1.5e-6, output_price: 4.5e-6 }, official_pricing_source: "channel_pricing" })
  const second = item({ channel_id: 2, channel_name: "渠道乙", official_pricing: { input_price: 0, output_price: 8e-6 }, official_pricing_source: "channel_pricing" })
  assert.equal(pricingReferences([first, second]).length, 2)
  assert.equal(pricingReferences([first, { ...first, channel_id: 2, channel_name: "相同价格的另一个渠道" }]).length, 2)
  assert.equal(pricingReferences([first, first]).length, 1)
  assert.equal(pricingReferences([item()]).length, 1)
  assert.equal(referenceLabel(item()), "原价待确认")
})
