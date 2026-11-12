import type { PublicModelPricingItem, PublicPricingGroup } from "./api-types"
import { hasPrice } from "./model-pricing"

// Sub2API sends these fields through GatewayOps without a schema guarantee.
// Recognize the channel-pricing contract defensively; never infer missing prices
// or evaluate the upstream's rule precedence on this public display.
export const rulePriceColumns = [
  { key: "input_price", multiplier: "input_multiplier", label: "输入" },
  { key: "output_price", multiplier: "output_multiplier", label: "输出" },
  { key: "cache_read_price", multiplier: "cache_read_multiplier", label: "缓存读" },
  { key: "cache_write_price", multiplier: "cache_write_multiplier", label: "缓存写" },
  { key: "cache_write_1h_price", label: "缓存写 · 1 小时" },
  { key: "cache_write_12h_price", label: "缓存写 · 12 小时" },
  { key: "per_request_price", label: "每次请求", perRequest: true },
] as const

export type RulePriceColumn = { key: string; multiplier?: string; label: string; perRequest?: boolean }
export type RuleRecord = Record<string, unknown>
export type PricingTier = { label: string; range: string; values: RuleRecord }
export type PricingPeriod = { start: string; end: string; multiplier: number }
export type ParsedPricingRules = {
  tiers: PricingTier[]
  tokenTiers: boolean
  columns: RulePriceColumn[]
  periods: PricingPeriod[]
  timezone: string
  days: string
  fast: number | null
  flex: number | null
  warnings: string[]
  hasRules: boolean
}

function record(value: unknown): value is RuleRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
function text(value: unknown): string { return typeof value === "string" ? value.trim() : "" }
export function isRuleMultiplier(value: unknown): value is number { return hasPrice(value) && value > 0 }
function tokenCount(value: unknown): value is number { return hasPrice(value) && Number.isSafeInteger(value) }

function tierRange(value: RuleRecord): string {
  const min = tokenCount(value.min_tokens) ? value.min_tokens.toLocaleString("zh-CN") : null
  const max = tokenCount(value.max_tokens) ? value.max_tokens.toLocaleString("zh-CN") : null
  if (min !== null && max !== null) return `> ${min} 且 ≤ ${max} Token`
  if (min !== null && value.max_tokens === null) return `> ${min} Token · 无上限`
  if (min !== null) return `> ${min} Token · 上限未提供`
  if (max !== null) return `下限未提供 · ≤ ${max} Token`
  return value.max_tokens === null ? "下限未提供 · 无上限" : "Token 区间未提供"
}

function clock(value: unknown, end = false): { label: string; seconds: number } | null {
  if (typeof value !== "string" || !/^(?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?$/.test(value)) return null
  const [hour, minute, second = 0] = value.split(":").map(Number)
  if (end && hour === 0 && minute === 0 && second === 0) return { label: "24:00", seconds: 86400 }
  return { label: second === 0 ? value.slice(0, 5) : value, seconds: hour * 3600 + minute * 60 + second }
}

const tierKeys = new Set(["min_tokens", "max_tokens", "tier_label", "sort_order", ...rulePriceColumns.flatMap((column) => [column.key, "multiplier" in column ? column.multiplier : ""]).filter(Boolean)])
const timeKeys = new Set(["timezone", "weekdays_only", "periods"])
const periodKeys = new Set(["start_time", "end_time", "multiplier"])
const unknownKeys = (value: RuleRecord, keys: Set<string>) => Object.keys(value).some((key) => !keys.has(key))

export function parsePricingRules(item: PublicModelPricingItem): ParsedPricingRules {
  const tiers: PricingTier[] = []
  const periods: PricingPeriod[] = []
  const warnings: string[] = []
  const tokenTiers = item.price_source !== "group_image_pricing" && (!item.billing_mode || item.billing_mode === "token")
  const tierBounds: { min: number; max: number }[] = []
  const periodBounds: { start: number; end: number }[] = []
  let unsupportedTier = false
  const rawIntervals: unknown = item.intervals
  if (Array.isArray(rawIntervals)) {
    for (const value of rawIntervals) {
      if (!record(value) || !Object.keys(value).some((key) => tierKeys.has(key))) { unsupportedTier = true; continue }
      if (unknownKeys(value, tierKeys)) unsupportedTier = true
      const validBounds = tokenCount(value.min_tokens) && (value.max_tokens === null || (tokenCount(value.max_tokens) && value.max_tokens > value.min_tokens))
      if (tokenTiers && !validBounds) unsupportedTier = true
      if (tokenTiers && validBounds) tierBounds.push({ min: value.min_tokens as number, max: value.max_tokens === null ? Infinity : value.max_tokens as number })
      for (const column of rulePriceColumns) {
        if (value[column.key] != null && !hasPrice(value[column.key])) unsupportedTier = true
        if ("multiplier" in column && value[column.multiplier] != null && !isRuleMultiplier(value[column.multiplier])) unsupportedTier = true
      }
      tiers.push({ label: text(value.tier_label) || `阶梯 ${tiers.length + 1}`, range: tokenTiers ? (validBounds ? tierRange(value) : "Token 区间未提供或无法识别") : "按档位匹配", values: value })
    }
  } else if (rawIntervals != null) unsupportedTier = true
  if (unsupportedTier) warnings.push("部分阶梯规则无法识别或配置不完整，请以上游配置为准。")
  const sortedTiers = [...tierBounds].sort((left, right) => left.min - right.min)
  if (sortedTiers.some((tier, index) => index > 0 && tier.min < sortedTiers[index - 1].max)) warnings.push("阶梯区间存在重叠，无法判断规则优先级，请核对上游配置。")

  const time = item.time_pricing
  let timezone = "时区未提供"
  let days = "适用日期未提供"
  let unsupportedTime = false
  if (record(time)) {
    if (unknownKeys(time, timeKeys)) unsupportedTime = true
    if (text(time.timezone)) {
      try { new Intl.DateTimeFormat("zh-CN", { timeZone: text(time.timezone) }); timezone = text(time.timezone) }
      catch { timezone = "时区无法识别"; unsupportedTime = true }
    } else if (time.timezone != null && time.timezone !== "") unsupportedTime = true
    if (typeof time.weekdays_only === "boolean") days = time.weekdays_only ? "周一至周五" : "每日"
    else if (time.weekdays_only != null) unsupportedTime = true
    if (Array.isArray(time.periods)) {
      for (const value of time.periods) {
        if (!record(value)) { unsupportedTime = true; continue }
        const start = clock(value.start_time)
        const end = clock(value.end_time, true)
        if (!start || !end || start.seconds >= end.seconds || !isRuleMultiplier(value.multiplier)) { unsupportedTime = true; continue }
        if (unknownKeys(value, periodKeys)) unsupportedTime = true
        periods.push({ start: start.label, end: end.label, multiplier: value.multiplier })
        periodBounds.push({ start: start.seconds, end: end.seconds })
      }
    } else if (time.periods != null || Object.keys(time).length > 0) unsupportedTime = true
  } else if (time != null) unsupportedTime = true
  if (unsupportedTime) warnings.push("部分分时规则无法识别，请以上游配置为准。")
  const sortedPeriods = [...periodBounds].sort((left, right) => left.start - right.start)
  if (sortedPeriods.some((period, index) => index > 0 && period.start < sortedPeriods[index - 1].end)) warnings.push("计费时段存在重叠，无法判断规则优先级，请核对上游配置。")

  const fast = isRuleMultiplier(item.fast_multiplier) ? item.fast_multiplier : null
  const flex = isRuleMultiplier(item.flex_multiplier) ? item.flex_multiplier : null
  if ((item.fast_multiplier != null && fast === null) || (item.flex_multiplier != null && flex === null)) warnings.push("部分 Fast / Flex 倍率无法识别，请以上游配置为准。")
  const columns: RulePriceColumn[] = rulePriceColumns.filter((column) => tiers.some(({ values }) => hasPrice(values[column.key]) || ("multiplier" in column && isRuleMultiplier(values[column.multiplier]))))
  return { tiers, tokenTiers, columns, periods, timezone, days, fast, flex, warnings, hasRules: Boolean(tiers.length || periods.length || fast !== null || flex !== null || warnings.length) }
}

export function visiblePricingRules(items: PublicModelPricingItem[], groups: PublicPricingGroup[]) {
  const names = new Set(groups.map((group) => group.name))
  const seen = new Map<string, { item: PublicModelPricingItem; rules: ParsedPricingRules }>()
  for (const source of items) {
    const visibleGroups = (source.groups ?? []).filter((name) => names.has(name))
    if (!visibleGroups.length) continue
    const item = { ...source, groups: visibleGroups }
    const rules = parsePricingRules(item)
    if (!rules.hasRules) continue
    // Only merge identical rules from the same channel and billing model.
    const key = JSON.stringify([item.channel_id, item.channel_name, item.billing_model, item.intervals, item.time_pricing, item.fast_multiplier, item.flex_multiplier])
    const previous = seen.get(key)
    if (previous) previous.item.groups = [...new Set([...(previous.item.groups ?? []), ...visibleGroups])]
    else seen.set(key, { item, rules })
  }
  return [...seen.values()]
}
