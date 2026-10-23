import type { PublicModelPricingItem, PublicModelPricingValues, PublicModelPricingView, PublicPricingGroup } from "./api-types"

export type PricingKind = "token" | "request"
export type PricingColumn = { key: keyof PublicModelPricingValues; label: string; perRequest?: boolean }

export const tokenColumns: PricingColumn[] = [
  { key: "input_price", label: "输入" },
  { key: "output_price", label: "输出" },
  { key: "cache_read_price", label: "缓存读" },
  { key: "cache_write_price", label: "缓存写" },
]
export const imageColumns: PricingColumn[] = [
  { key: "image_price_1k", label: "1K ($)", perRequest: true },
  { key: "image_price_2k", label: "2K ($)", perRequest: true },
  { key: "image_price_4k", label: "4K ($)", perRequest: true }
]
export const requestColumns: PricingColumn[] = [{ key: "per_request_price", label: "每次请求", perRequest: true }]

export function hasPrice(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
}

export function hasPricing(value?: PublicModelPricingValues | null): value is PublicModelPricingValues {
  return Boolean(value && Object.entries(value).some(([key, amount]) => key !== "billing_mode" && hasPrice(amount)))
}

export function pricingKind(item: PublicModelPricingItem): PricingKind {
  return item.price_source === "group_image_pricing" ? "request" : "token"
}

export function pricingColumns(items: PublicModelPricingItem[]): PricingColumn[] {
  if (!items.length) return []
  if (items.some((item) => item.price_source === "group_image_pricing")) return imageColumns
  return [
    ...tokenColumns,
    ...(items.some((item) => hasPrice(item.image_input_price)) ? [{ key: "image_input_price", label: "图片输入" } satisfies PricingColumn] : []),
    ...(items.some((item) => hasPrice(item.image_output_price)) ? [{ key: "image_output_price", label: "图片输出" } satisfies PricingColumn] : []),
  ]
}

// The API returns groups in Sub2API's current sort_order; preserve that order in the public page.
export function publicPricingGroups(data: PublicModelPricingView): PublicPricingGroup[] {
  return [...(data.public_groups ?? [])]
    .filter((group) => Boolean(group.name?.trim()) && hasPrice(group.rate_multiplier))
}

export function publicPricingItems(data: PublicModelPricingView): PublicModelPricingItem[] {
  const names = new Set(publicPricingGroups(data).map((group) => group.name))
  return data.items.flatMap((item) => {
    const groups = [...new Set((item.groups ?? []).filter((name) => names.has(name)))]
    if (!groups.length) return []
    // Public per-request prices are only the resolution-specific prices explicitly configured for image groups.
    if (item.price_source !== "group_image_pricing" && item.billing_mode && item.billing_mode !== "token") return []
    return [{ ...item, groups }]
  })
}

export function pricingReferences(items: PublicModelPricingItem[]): PublicModelPricingItem[] {
  const eligible = items.filter((item) => item.price_source !== "group_image_pricing")
  const references = eligible.filter((item) => hasPricing(item.official_pricing))
    .filter((item, index, values) => values.findIndex((other) => other.official_pricing_source === item.official_pricing_source && (item.official_pricing_source !== "channel_pricing" || other.channel_id === item.channel_id) && JSON.stringify(other.official_pricing) === JSON.stringify(item.official_pricing)) === index)
  return references.length ? references : eligible.slice(0, 1)
}

export function pricingValue(item: PublicModelPricingItem, key: keyof PublicModelPricingValues, group?: PublicPricingGroup): number | null {
  if (!group) {
    if (item.price_source === "group_image_pricing") return null
    const value = item.official_pricing?.[key]
    return hasPrice(value) ? value : null
  }
  if (!item.groups?.includes(group.name)) return null
  const value = item[key]
  if (!hasPrice(value) || !hasPrice(group.rate_multiplier)) return null
  // Independent resolution prices are already group-owned final prices.
  return item.price_source === "group_image_pricing" ? value : value * group.rate_multiplier
}

export function formatModelPrice(value: number | null | undefined, perRequest = false, currency: "USD" | "CNY" = "USD"): string {
  if (!hasPrice(value)) return "—"
  // Sub2API channel model_pricing and its default-pricing endpoint both return USD/token.
  // Its admin form converts USD/MTok to USD/token before saving; request prices are unscaled.
  const amount = perRequest ? value : value * 1_000_000
  return `${currency === "CNY" ? "¥" : "$"}${amount.toLocaleString("zh-CN", { maximumFractionDigits: 8 })}`
}

export function referenceLabel(item: PublicModelPricingItem): string {
  if (!hasPricing(item.official_pricing)) return "原价待确认"
  if (item.official_pricing_source === "channel_pricing") return "渠道设置原价"
  return item.official_pricing_source === "official_catalog" ? "官方原价 · 公开价格目录" : "参考原价 · Sub2API系统定价"
}

export function effectivePricingLabel(item: PublicModelPricingItem): string {
  if (item.price_source === "group_image_pricing") return "分组独立生图价"
  if (item.price_source === "unavailable") return "价格待更新"
  if (item.price_source === "system_default" && item.official_pricing_source === "official_catalog") return "按官方原价估算"
  if (item.price_source === "channel_override_with_default") return "渠道定价 + 默认补全"
  return item.price_source === "channel_override" ? "渠道定价" : "系统基价"
}
