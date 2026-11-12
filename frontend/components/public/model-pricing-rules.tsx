import { ChevronDown, Clock3, Layers3, SlidersHorizontal, Zap } from "lucide-react"
import type { PublicModelPricingItem, PublicPricingGroup } from "@/lib/api-types"
import { formatModelPrice, hasPrice } from "@/lib/model-pricing"
import { isRuleMultiplier, visiblePricingRules, type PricingTier, type RulePriceColumn } from "@/lib/model-pricing-rules"

function TierValue({ tier, column, currency }: { tier: PricingTier; column: RulePriceColumn; currency: "USD" | "CNY" }) {
  const price = tier.values[column.key]
  const multiplier = column.multiplier ? tier.values[column.multiplier] : null
  return <>
    {hasPrice(price) ? <span className="block font-mono font-semibold tabular-nums text-[#315077]">{formatModelPrice(price, column.perRequest, currency)}</span> : null}
    {isRuleMultiplier(multiplier) ? <span className="block text-[11px] font-medium tabular-nums text-violet-700">倍率 ×{multiplier}</span> : null}
    {!hasPrice(price) && !isRuleMultiplier(multiplier) ? <span className="text-[#9aa8be]">—</span> : null}
  </>
}

export function ModelPricingRules({ items, groups, currency }: { items: PublicModelPricingItem[]; groups: PublicPricingGroup[]; currency: "USD" | "CNY" }) {
  const entries = visiblePricingRules(items, groups)
  if (!entries.length) return null
  const hasTiers = entries.some(({ rules }) => rules.tiers.length)
  const hasPeriods = entries.some(({ rules }) => rules.periods.length)
  const hasModes = entries.some(({ rules }) => rules.fast !== null || rules.flex !== null)

  return <details className="group/rules mt-3 min-w-0 rounded-lg border border-[#dfe5f0] bg-[#f8faff] text-xs text-[#526880]">
    <summary className="flex cursor-pointer list-none flex-wrap items-center gap-2 rounded-lg px-3 py-3 transition-colors hover:bg-[#f0f3fc] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-violet-500 [&::-webkit-details-marker]:hidden">
      <SlidersHorizontal aria-hidden="true" className="size-3.5 shrink-0 text-[#6e65be]" />
      <span className="font-semibold text-[#4d5a7c]">定价规则</span>
      <span className="flex flex-1 flex-wrap gap-1.5 text-[10px] text-[#7b87a3]">
        {hasTiers ? <span className="rounded border border-[#e3e7f2] bg-white px-1.5 py-0.5">阶梯价格</span> : null}
        {hasPeriods ? <span className="rounded border border-[#e3e7f2] bg-white px-1.5 py-0.5">分时倍率</span> : null}
        {hasModes ? <span className="rounded border border-[#e3e7f2] bg-white px-1.5 py-0.5">Fast / Flex</span> : null}
      </span>
      <ChevronDown aria-hidden="true" className="size-4 shrink-0 text-[#8b96af] transition-transform group-open/rules:rotate-180" />
    </summary>
    <div className="min-w-0 space-y-3 border-t border-[#e3e8f2] p-3">
      <p className="text-[11px] leading-5 text-[#8190a5]">以下为渠道配置，未折算分组倍率、时段或 Fast / Flex 倍率；实际计费以上游为准。</p>
      {entries.map(({ item, rules }, index) => <section key={`${item.channel_id}-${index}`} aria-label={`${item.channel_name || "系统定价"}定价规则`} className="min-w-0 overflow-hidden rounded-lg border border-[#e0e7f0] bg-white">
        <header className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-[#edf0f6] bg-[#fbfcff] px-3 py-2.5">
          <div className="min-w-0"><h4 className="break-words font-semibold text-[#425b7b]">{item.channel_name || "系统定价"}</h4>{item.billing_model && item.billing_model !== item.model ? <p className="mt-1 break-all text-[10px] text-[#8996ad]">计费模型：{item.billing_model}</p> : null}</div>
          <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[10px] text-[#7a8aa3]"><span>适用分组</span>{item.groups?.map((name) => <span key={name} className="max-w-full break-words rounded bg-[#eef3f9] px-1.5 py-0.5 text-[#607794]">{name}</span>)}</div>
        </header>
        <div className="min-w-0 space-y-4 p-3">
          {rules.tiers.length ? <section aria-label="阶梯价格" className="min-w-0">
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2"><h5 className="flex items-center gap-1.5 font-semibold text-[#526880]"><Layers3 aria-hidden="true" className="size-3.5 text-[#7d71bf]" />阶梯价格<span className="ml-0.5 rounded bg-violet-50 px-1.5 py-0.5 text-[10px] font-normal text-violet-600">{rules.tiers.length} 档</span></h5><span className="text-[10px] text-[#8996ad]">{currency} / {rules.columns.length && rules.columns.every((column) => column.perRequest) ? "次" : "百万 Token"}{rules.columns.some((column) => column.perRequest) && rules.columns.some((column) => !column.perRequest) ? " · 请求价按次" : ""}</span></div>
            <div className="hidden overflow-x-auto rounded-md border border-[#e7ebf3] sm:block"><table aria-label={`${item.channel_name || "系统定价"}阶梯价格`} className="w-full text-left text-[11px]"><thead className="bg-[#f5f7fc] text-[#8291a8]"><tr><th scope="col" className="min-w-40 px-3 py-2 font-medium">{rules.tokenTiers ? "Token 区间" : "计费档位"}</th>{rules.columns.map((column) => <th scope="col" key={column.key} className="whitespace-nowrap px-3 py-2 text-right font-medium">{column.label}</th>)}{!rules.columns.length ? <th scope="col" className="px-3 py-2 font-medium">单价 / 倍率</th> : null}</tr></thead><tbody className="divide-y divide-[#edf0f6]">{rules.tiers.map((tier, tierIndex) => <tr key={tierIndex}><th scope="row" className="px-3 py-2.5 font-normal"><span className="block break-words font-medium text-[#4e6485]">{tier.label}</span><span className="mt-1 block whitespace-nowrap text-[10px] tabular-nums text-[#8996ad]">{tier.range}</span></th>{rules.columns.map((column) => <td key={column.key} className="whitespace-nowrap px-3 py-2.5 text-right"><TierValue tier={tier} column={column} currency={currency} /></td>)}{!rules.columns.length ? <td className="px-3 py-2.5 text-[#8996ad]">未提供单价或倍率</td> : null}</tr>)}</tbody></table></div>
            <div className="space-y-2 sm:hidden">{rules.tiers.map((tier, tierIndex) => <div key={tierIndex} className="min-w-0 rounded-md border border-[#e7ebf3] p-3"><p className="break-words font-medium text-[#4e6485]">{tier.label}</p><p className="mt-1 break-words text-[10px] leading-5 tabular-nums text-[#8996ad]">{tier.range}</p><dl className="mt-2 grid min-w-0 grid-cols-2 gap-x-3 gap-y-2 border-t border-[#edf0f6] pt-2">{rules.columns.map((column) => <div key={column.key} className="min-w-0 break-words"><dt className="mb-0.5 text-[10px] text-[#8996ad]">{column.label}{column.perRequest ? " / 次" : ""}</dt><dd><TierValue tier={tier} column={column} currency={currency} /></dd></div>)}</dl>{!rules.columns.length ? <p className="text-[11px] text-[#8996ad]">未提供单价或倍率</p> : null}</div>)}</div>
            <p className="mt-2 text-[10px] leading-5 text-[#8996ad]">{rules.tokenTiers ? "按上下文 Token 区间展示配置" : "按计费档位展示配置"}；— 表示未提供该项覆盖值，不代表免费。</p>
          </section> : null}
          {rules.periods.length ? <section aria-label="分时倍率" className="min-w-0">
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2"><h5 className="flex items-center gap-1.5 font-semibold text-[#526880]"><Clock3 aria-hidden="true" className="size-3.5 text-[#5e9eaa]" />分时倍率</h5><div className="flex min-w-0 flex-wrap gap-x-2 gap-y-1 text-[10px] text-[#8996ad]"><span className="break-all">{rules.timezone}</span><span>{rules.days}</span></div></div>
            <div className="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,13rem),1fr))] gap-2">{rules.periods.map((period, periodIndex) => <div key={periodIndex} className="flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-md border border-teal-100 bg-teal-50/50 px-3 py-2.5"><span className="font-mono text-[12px] font-medium tabular-nums text-[#426779]">{period.start}<span className="mx-1.5 text-[#9ab0ba]">–</span>{period.end}</span><span className="rounded bg-white px-2 py-0.5 font-mono text-sm font-semibold tabular-nums text-teal-700 ring-1 ring-teal-100">×{period.multiplier}</span></div>)}</div>
          </section> : null}
          {rules.fast !== null || rules.flex !== null ? <section aria-label="Fast / Flex 倍率" className="flex flex-wrap items-center gap-2"><h5 className="mr-1 flex items-center gap-1.5 text-[11px] font-medium text-[#7b87a3]"><Zap aria-hidden="true" className="size-3.5" />模式倍率</h5>{rules.fast !== null ? <span className="rounded-md border border-amber-100 bg-amber-50 px-2.5 py-1 font-medium text-amber-700">Fast <span className="ml-1 font-mono tabular-nums">×{rules.fast}</span></span> : null}{rules.flex !== null ? <span className="rounded-md border border-violet-100 bg-violet-50 px-2.5 py-1 font-medium text-violet-700">Flex <span className="ml-1 font-mono tabular-nums">×{rules.flex}</span></span> : null}</section> : null}
          {rules.warnings.map((warning) => <p key={warning} role="note" className="rounded-md border border-amber-100 bg-amber-50 px-3 py-2 text-[11px] leading-5 text-amber-800">{warning}</p>)}
        </div>
      </section>)}
    </div>
  </details>
}
