"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { BadgeDollarSign, Building2, Check, Copy, Database, Search, X } from "lucide-react"
import { useParams } from "react-router-dom"
import openAIIcon from "@lobehub/icons-static-svg/icons/openai.svg"
import anthropicIcon from "@lobehub/icons-static-svg/icons/anthropic.svg"
import googleIcon from "@lobehub/icons-static-svg/icons/google-color.svg"
import deepSeekIcon from "@lobehub/icons-static-svg/icons/deepseek-color.svg"
import qwenIcon from "@lobehub/icons-static-svg/icons/alibabacloud-color.svg"
import zhipuIcon from "@lobehub/icons-static-svg/icons/zhipu-color.svg"
import moonshotIcon from "@lobehub/icons-static-svg/icons/moonshot.svg"
import miniMaxIcon from "@lobehub/icons-static-svg/icons/minimax-color.svg"
import tencentIcon from "@lobehub/icons-static-svg/icons/tencent-color.svg"
// Verified against the published @lobehub/icons-static-svg@1.94.0 SVGs on jsDelivr.
import stepFunIcon from "@lobehub/icons-static-svg/icons/stepfun-color.svg"
import xiaomiIcon from "@lobehub/icons-static-svg/icons/xiaomimimo.svg"
import xAIIcon from "@lobehub/icons-static-svg/icons/xai.svg"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PublicPageHeader } from "@/components/public/public-page-header"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch } from "@/lib/api"
import type { PublicModelPricingItem, PublicModelPricingView, PublicPricingGroup } from "@/lib/api-types"
import { effectivePricingLabel, formatModelPrice, pricingColumns, pricingKind, pricingReferences, pricingValue, publicPricingGroups, publicPricingItems, referenceLabel, type PricingKind } from "@/lib/model-pricing"
import { cn } from "@/lib/utils"

const cacheVersion = 8
const refreshMS = 10 * 60 * 1000
const companyOrder = ["OpenAI", "Anthropic", "Google", "xAI", "DeepSeek", "阿里云", "智谱 AI", "月之暗面", "MiniMax", "腾讯"]
const kindLabels: Record<PricingKind, string> = { token: "按量付费", request: "按次付费" }
const companyIcons: Record<string, string> = { openai: openAIIcon, anthropic: anthropicIcon, google: googleIcon, xai: xAIIcon, deepseek: deepSeekIcon, 阿里云: qwenIcon, "智谱 ai": zhipuIcon, 月之暗面: moonshotIcon, minimax: miniMaxIcon, 腾讯: tencentIcon, 阶跃星辰: stepFunIcon, 小米: xiaomiIcon }
const tones = ["border-l-violet-500 bg-violet-50 text-violet-800", "border-l-emerald-500 bg-emerald-50 text-emerald-800", "border-l-blue-500 bg-blue-50 text-blue-800", "border-l-amber-500 bg-amber-50 text-amber-800"]

type PriceItem = PublicModelPricingItem
type ModelEntry = { key: string; model: string; company: string; kind: PricingKind; items: PriceItem[] }

function compareCompanies(left: string, right: string) {
  const rank = (name: string) => { const index = companyOrder.indexOf(name); return index < 0 ? 999 : index }
  return rank(left) - rank(right) || left.localeCompare(right, "zh-CN")
}

function cacheKey(stationID: number) { return `gatewayops:model-pricing:v${cacheVersion}:${stationID}` }
function readCache(stationID: number): PublicModelPricingView | null {
  try {
    const cached = JSON.parse(window.localStorage.getItem(cacheKey(stationID)) || "null") as { at: number; data: PublicModelPricingView } | null
    return cached?.data?.station_id === stationID && Array.isArray(cached.data.items) && Array.isArray(cached.data.public_groups) && Date.now() - cached.at < (cached.data.warnings?.length ? 30_000 : refreshMS) ? cached.data : null
  } catch { return null }
}

function CompanyLogo({ company }: { company: string }) {
  const source = companyIcons[company.toLowerCase()]
  return source ? <img src={source} alt="" className="size-5 shrink-0 object-contain" /> : <Building2 className="size-5 shrink-0 text-[#657889]" />
}

function Counter({ value }: { value: number }) { return <span className="shrink-0 text-[10px] tabular-nums text-[#8996ad]">{value}</span> }

function ModelMatrix({ entry, groups, currency, toneByGroup }: { entry: ModelEntry; groups: PublicPricingGroup[]; currency: "USD" | "CNY"; toneByGroup: Map<string, number> }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])
  const columns = pricingColumns(entry.items)
  const referenceRows = pricingReferences(entry.items)
  const rows = groups.flatMap((group) => {
    const matching = entry.items.filter((item) => item.groups?.includes(group.name))
    const unique = matching.filter((item, index) => matching.findIndex((other) => JSON.stringify([columns.map(({ key }) => other[key]), other.billing_model, other.intervals, other.time_pricing]) === JSON.stringify([columns.map(({ key }) => item[key]), item.billing_model, item.intervals, item.time_pricing])) === index)
    return unique.map((item) => ({ group, item, multiple: unique.length > 1 }))
  })
  const advanced = entry.items.filter((item) => item.intervals?.length || item.time_pricing || item.fast_multiplier != null || item.flex_multiplier != null)
  const unit = `${currency} / ${entry.kind === "token" ? "百万 Token" : "次"}`

  async function copyModel() {
    try {
      await navigator.clipboard.writeText(entry.model)
      setCopied(true)
      timer.current = setTimeout(() => setCopied(false), 1600)
    } catch { setCopied(false) }
  }

  return <article className="grid min-w-0 gap-3 border-b border-[#e5eaf2] bg-white px-4 py-4 last:border-b-0 xl:grid-cols-[minmax(12rem,0.29fr)_minmax(0,1fr)] xl:gap-6 xl:px-5 xl:py-5">
    <div className="min-w-0"><div className="flex items-start gap-2"><h3 className="break-all font-mono text-[13px] font-semibold leading-6 text-[#315077]">{entry.model}</h3><button type="button" aria-label={`复制模型 ${entry.model}`} title={copied ? "已复制" : "复制模型名称"} className="mt-1 shrink-0 rounded p-0.5 text-[#94a3b8] hover:text-[#4c5bd4] focus-visible:outline-2 focus-visible:outline-[#6e55e5]" onClick={() => void copyModel()}>{copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}</button></div><p className="mt-1 text-[11px] text-[#8996ad]">{unit}</p>{entry.items.some((item) => item.billing_model && item.billing_model !== entry.model) ? <p className="mt-2 break-all text-[11px] text-[#8996ad]">计费模型：{[...new Set(entry.items.map((item) => item.billing_model).filter(Boolean))].join("、")}</p> : null}</div>
    <div className="min-w-0 overflow-x-auto"><table className="w-full min-w-[550px] border-separate border-spacing-y-1.5 text-left text-xs" aria-label={`${entry.model}公开分组价格`}><thead><tr className="text-[#8c9aab]"><th scope="col" className="min-w-44 px-3 pb-1 font-semibold">分组 / 价格来源</th>{columns.map((column) => <th scope="col" key={column.key} className="px-3 pb-1 text-right font-semibold">{column.label}</th>)}</tr></thead><tbody>
      {referenceRows.map((item, index) => <tr key={`reference-${index}`} className="bg-[#f4f7f8] text-[#50656e]"><th scope="row" className="rounded-l-md border-l-[3px] border-l-[#c5d1d6] px-3 py-2.5 font-medium"><span className="block">{referenceLabel(item)}</span>{item.official_pricing_source === "channel_pricing" || referenceRows.length > 1 ? <span className="mt-0.5 block text-[10px] text-[#83959c]">{[item.official_pricing_source === "channel_pricing" ? item.channel_name : "", item.billing_model || item.model].filter(Boolean).join(" · ")}</span> : null}</th>{columns.map(({ key, perRequest }, cell) => <td key={key} className={cn("px-3 py-2.5 text-right font-mono text-sm font-semibold tabular-nums", cell === columns.length - 1 && "rounded-r-md")}>{formatModelPrice(pricingValue(item, key), perRequest, currency)}</td>)}</tr>)}
      {rows.map(({ group, item, multiple }, index) => <tr key={`${group.name}-${item.channel_id}-${index}`} className={tones[(toneByGroup.get(group.name) ?? 0) % tones.length]}><th scope="row" className="rounded-l-md border-l-[3px] border-l-inherit px-3 py-2.5 font-semibold"><span>{group.name}</span>{item.price_source !== "group_image_pricing" ? <span className="ml-1.5 text-[10px] font-normal opacity-70">×{group.rate_multiplier}</span> : null}<span className="mt-0.5 block text-[10px] font-normal opacity-65">{effectivePricingLabel(item)}{multiple ? ` · ${item.channel_name}` : ""}</span></th>{columns.map(({ key, perRequest }, cell) => <td key={key} className={cn("px-3 py-2.5 text-right font-mono text-sm font-semibold tabular-nums", cell === columns.length - 1 && "rounded-r-md")}>{formatModelPrice(pricingValue(item, key, group), perRequest, currency)}</td>)}</tr>)}
    </tbody></table>{rows.some(({ item }) => item.price_source === "unavailable") ? <p className="mt-1 text-[11px] text-[#a17832]">价格待更新：此模型已公开，尚未取得有效单价。</p> : null}
    {advanced.length ? <details className="mt-2 text-[11px] text-[#718099]"><summary className="cursor-pointer hover:text-[#4c5bd4]">查看阶梯、时段与高级定价规则</summary><div className="mt-2 space-y-3 rounded-md border border-[#e1e6ef] bg-[#f8faff] p-3">{advanced.map((item, index) => <div key={index}><p className="mb-1 font-semibold">{item.channel_name || "系统定价"}</p>{item.fast_multiplier != null ? <p>Fast 倍率：{item.fast_multiplier}</p> : null}{item.flex_multiplier != null ? <p>Flex 倍率：{item.flex_multiplier}</p> : null}{item.intervals?.length ? <pre className="mt-1 max-h-48 overflow-auto">{JSON.stringify(item.intervals, null, 2)}</pre> : null}{item.time_pricing ? <pre className="mt-1 max-h-48 overflow-auto">{JSON.stringify(item.time_pricing, null, 2)}</pre> : null}</div>)}</div></details> : null}</div>
  </article>
}

function PricingContent({ stationID }: { stationID: number }) {
  const [data, setData] = useState<PublicModelPricingView | null>(() => readCache(stationID))
  const [loading, setLoading] = useState(!data)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")
  const [query, setQuery] = useState("")
  const [selectedGroup, setSelectedGroup] = useState("all")
  const [selectedCompany, setSelectedCompany] = useState("all")
  const [selectedKind, setSelectedKind] = useState<PricingKind | "all">("all")
  const controller = useRef<AbortController | null>(null)

  const load = useCallback(async () => {
    controller.current?.abort()
    const abort = new AbortController()
    controller.current = abort
    setRefreshing(true)
    try {
      const next = await apiFetch<PublicModelPricingView>(`/public/relay-stations/${stationID}/model-pricing`, { skipAuthErrorHandler: true, signal: abort.signal })
      if (abort.signal.aborted) return
      setData(next); setError("")
      try { window.localStorage.setItem(cacheKey(stationID), JSON.stringify({ at: Date.now(), data: next })) } catch { /* persistence is optional */ }
    } catch (requestError) {
      if (!abort.signal.aborted) setError(requestError instanceof Error ? requestError.message : "价格读取失败")
    } finally {
      if (!abort.signal.aborted) { setLoading(false); setRefreshing(false) }
    }
  }, [stationID])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, refreshMS)
    return () => { controller.current?.abort(); window.clearInterval(timer) }
  }, [load])
  useEffect(() => { document.title = "模型价格" }, [])

  const groups = useMemo(() => data ? publicPricingGroups(data) : [], [data])
  const items = useMemo(() => data ? publicPricingItems(data) : [], [data])
  const toneByGroup = useMemo(() => new Map(groups.map((group, index) => [group.name, index])), [groups])
  const allModels = useMemo(() => {
    const map = new Map<string, ModelEntry>()
    for (const item of items) {
      const kind = pricingKind(item)
      const company = item.company || item.provider || "其他"
      const key = `${company}\u0000${item.model}\u0000${kind}`
      const entry = map.get(key)
      if (entry) entry.items.push(item)
      else map.set(key, { key, model: item.model, company, kind, items: [item] })
    }
    return [...map.values()].sort((left, right) => compareCompanies(left.company, right.company) || left.model.localeCompare(right.model, "zh-CN", { numeric: true }) || left.kind.localeCompare(right.kind))
  }, [items])
  const filtered = useMemo(() => {
    const search = query.trim().toLowerCase()
    return allModels.filter((entry) => (selectedCompany === "all" || entry.company === selectedCompany) && (selectedKind === "all" || entry.kind === selectedKind) && (selectedGroup === "all" || entry.items.some((item) => item.groups?.includes(selectedGroup))) && (!search || [entry.model, entry.company, ...entry.items.flatMap((item) => [item.billing_model, item.provider, ...(item.groups ?? [])])].some((value) => value?.toLowerCase().includes(search))))
  }, [allModels, query, selectedCompany, selectedGroup, selectedKind])
  const grouped = useMemo(() => {
    const map = new Map<string, ModelEntry[]>()
    for (const entry of filtered) map.set(entry.company, [...(map.get(entry.company) ?? []), entry])
    return [...map.entries()]
  }, [filtered])
  const companies = useMemo(() => [...new Set(allModels.map((entry) => entry.company))].sort(compareCompanies), [allModels])
  const visibleGroups = selectedGroup === "all" ? groups : groups.filter((group) => group.name === selectedGroup)
  const uniqueCount = (list: ModelEntry[]) => new Set(list.map((entry) => entry.model)).size
  const totalModels = uniqueCount(allModels)
  const reset = () => { setQuery(""); setSelectedGroup("all"); setSelectedCompany("all"); setSelectedKind("all") }
  const hasFilter = Boolean(query || selectedGroup !== "all" || selectedCompany !== "all" || selectedKind !== "all")
  const currency = data?.currency ?? "USD"

  return <main data-public-page className="min-h-screen min-w-0 bg-transparent text-[#29415f]"><div className="w-full min-w-0">
    <PublicPageHeader icon={BadgeDollarSign} title="模型价格" description={`${totalModels} 个模型 · ${groups.length} 个分组 · ${currency === "USD" ? "美元" : "人民币"}计价`} updatedAt={data?.updated_at} refreshing={refreshing} refreshLabel="刷新模型价格" onRefresh={() => void load()} />
    <div className="grid items-start gap-4 2xl:grid-cols-[220px_minmax(0,1fr)]">
		<aside className="sticky top-5 hidden max-h-[calc(100dvh-2.5rem)] overflow-y-auto rounded-lg border border-[#dbe4f2] bg-[#f8faff] p-3 2xl:block" aria-label="价格筛选">
			<div className="mb-5 flex items-center justify-between"><h2 className="text-sm font-bold">筛选</h2><button type="button" className="text-xs text-[#0f9488] hover:underline" onClick={reset}>重置</button></div>
			<div className="mb-5"><label htmlFor="pricing-search" className="mb-2 block text-[11px] font-semibold text-[#8292ad]">模型搜索</label><div className="relative"><Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[#9aa8be]" /><Input id="pricing-search" value={query} onChange={(event) => setQuery(event.target.value)} className="h-9 border-[#dbe4f2] bg-white pl-8 text-xs" placeholder="名称、模型、分组" /></div></div>
			<h3 className="mb-2 border-t border-[#e4eaf4] pt-4 text-[11px] font-semibold text-[#8292ad]">计费方式</h3><div className="mb-5 grid grid-cols-2 gap-1.5">{(["all", "token", "request"] as const).map((kind) => <button key={kind} type="button" aria-pressed={selectedKind === kind} onClick={() => setSelectedKind(kind)} className={cn("rounded-md border px-2 py-2 text-xs", selectedKind === kind ? "border-violet-300 bg-violet-50 font-semibold text-violet-700" : "border-[#dce5f3] bg-white text-[#657b9b] hover:border-violet-200")}><span className="block">{kind === "all" ? "全部" : kindLabels[kind]}</span><Counter value={uniqueCount(kind === "all" ? allModels : allModels.filter((entry) => entry.kind === kind))} /></button>)}</div>
			<h3 className="mb-2 border-t border-[#e4eaf4] pt-4 text-[11px] font-semibold text-[#8292ad]">公开分组</h3><div className="mb-5 space-y-1.5"><button type="button" aria-pressed={selectedGroup === "all"} onClick={() => setSelectedGroup("all")} className={cn("flex w-full items-center justify-between rounded-md border px-2.5 py-2 text-left text-xs", selectedGroup === "all" ? "border-teal-400 bg-teal-50 font-semibold text-teal-700" : "border-[#dce5f3] bg-white")}><span>全部分组</span><Counter value={totalModels} /></button>{groups.map((group) => <button key={group.name} type="button" aria-pressed={selectedGroup === group.name} onClick={() => setSelectedGroup(group.name)} className={cn("flex w-full items-center justify-between gap-1 rounded-md border px-2.5 py-2 text-left text-xs", selectedGroup === group.name ? "border-teal-400 bg-teal-50 font-semibold text-teal-700" : "border-[#dce5f3] bg-white hover:border-teal-200")}><span className="min-w-0"><span className="block truncate" title={group.name}>{group.name}</span><span className="mt-0.5 block text-[10px] font-normal text-[#8996ad]">×{group.rate_multiplier}</span></span><Counter value={uniqueCount(allModels.filter((entry) => entry.items.some((item) => item.groups?.includes(group.name))))} /></button>)}</div>
			<h3 className="mb-2 border-t border-[#e4eaf4] pt-4 text-[11px] font-semibold text-[#8292ad]">模型厂商</h3><button type="button" onClick={() => setSelectedCompany("all")} aria-pressed={selectedCompany === "all"} className={cn("mb-1 flex w-full items-center justify-between rounded px-2 py-2 text-xs", selectedCompany === "all" && "bg-violet-50 font-semibold text-violet-700")}><span>全部厂商</span><Counter value={totalModels} /></button>{companies.map((company) => <button type="button" key={company} aria-pressed={selectedCompany === company} onClick={() => setSelectedCompany(company)} className={cn("flex w-full items-center gap-2 rounded px-2 py-2.5 text-left text-xs hover:bg-white", selectedCompany === company && "bg-violet-50 font-semibold text-violet-700")}><CompanyLogo company={company} /><span className="min-w-0 flex-1 truncate">{company}</span><Counter value={uniqueCount(allModels.filter((entry) => entry.company === company))} /></button>)}
		</aside>
		<section className="min-w-0" aria-label="模型价格目录">
			<div className="mb-3 rounded-lg border border-[#dbe4f2] bg-white p-3 2xl:hidden"><div className="mb-2 flex gap-2"><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型、厂商或分组" aria-label="搜索模型" /><Button variant="outline" size="icon" disabled={!hasFilter} onClick={reset} aria-label="清除筛选"><X className="size-4" /></Button></div><div className="grid gap-2 sm:grid-cols-3"><select aria-label="筛选计费方式" value={selectedKind} onChange={(event) => setSelectedKind(event.target.value as PricingKind | "all")} className="h-10 rounded-md border border-[#dbe4f2] bg-white px-2 text-xs"><option value="all">全部计费方式</option>{Object.entries(kindLabels).map(([key, label]) => <option value={key} key={key}>{label}</option>)}</select><select aria-label="筛选公开分组" value={selectedGroup} onChange={(event) => setSelectedGroup(event.target.value)} className="h-10 rounded-md border border-[#dbe4f2] bg-white px-2 text-xs"><option value="all">全部公开分组</option>{groups.map((group) => <option value={group.name} key={group.name}>{group.name}</option>)}</select><select aria-label="筛选模型厂商" value={selectedCompany} onChange={(event) => setSelectedCompany(event.target.value)} className="h-10 rounded-md border border-[#dbe4f2] bg-white px-2 text-xs"><option value="all">全部厂商</option>{companies.map((company) => <option key={company}>{company}</option>)}</select></div></div>
			<div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs"><p className="font-semibold">{hasFilter ? `筛选结果 · ${uniqueCount(filtered)} 个模型` : "全部公开价格"}</p><span className="text-[11px] text-[#8292ad]">— 为未提供，0 为免费</span></div><div className="mb-4 rounded-lg border border-[#e1e6f2] bg-[#f8faff] px-4 py-3 text-[11px] leading-5 text-[#72849f]">原价优先读取对应渠道管理保存的模型定价，缺失字段再使用系统基价 / 官方目录；按量分组价使用渠道基价 × 分组倍率。按次仅展示分组独立生图的 1K / 2K / 4K 价格，不乘倍率、不展示原价。Token 价格按百万 Token 展示，未做汇率换算。阶梯、时段和 Fast / Flex 规则见模型详情。</div>
			{data?.warnings?.length ? <div role="alert" className="mb-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-5 text-amber-800"><p className="font-semibold">部分分组价格暂未读取完整</p>{data.warnings.map((warning, index) => <p key={index}>{warning}</p>)}</div> : null}
			{error ? <div role="alert" className="mb-4 flex items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700"><span>{error}{data ? "，当前显示上次数据。" : ""}</span><Button type="button" variant="outline" size="sm" onClick={() => void load()}>重试</Button></div> : null}
			{loading && !data ? <div className="space-y-3">{Array.from({ length: 4 }, (_, index) => <div key={index} className="rounded-lg border border-[#dbe4f2] bg-white p-5"><Skeleton className="h-5 w-44" /><Skeleton className="mt-4 h-28 w-full" /></div>)}</div> : grouped.length ? <div className="space-y-5">{grouped.map(([company, entries]) => <section key={company} className="overflow-hidden rounded-lg border border-[#d6e0ee] bg-white"><header className="flex items-center gap-2.5 border-b border-[#d5e6e5] border-l-[3px] border-l-teal-500 bg-[#eef9f6] px-4 py-3"><CompanyLogo company={company} /><span className="text-[10px] font-semibold text-[#8199a6]">厂商</span><h2 className="text-sm font-bold text-[#1b6764]">{company}</h2><Counter value={uniqueCount(entries)} /></header>{entries.map((entry) => <ModelMatrix key={entry.key} entry={entry} groups={visibleGroups} currency={currency} toneByGroup={toneByGroup} />)}</section>)}</div> : <div className="rounded-lg border border-dashed border-[#cbd7e8] bg-white px-5 py-16 text-center"><Database className="mx-auto size-7 text-[#9aa8be]" /><h2 className="mt-3 text-base font-semibold">{hasFilter ? "没有匹配的模型" : "暂无公开模型"}</h2><p className="mt-2 text-xs text-[#8292ad]">{hasFilter ? "调整搜索词或筛选条件后重试。" : "当前没有公开且可用的模型，请稍后刷新。"}</p>{hasFilter ? <Button type="button" variant="outline" size="sm" className="mt-4" onClick={reset}>清除筛选</Button> : null}</div>}
		</section>
    </div>
  </div></main>
}

export default function PublicModelPricingPage() {
  const { stationID } = useParams()
  const id = Number(stationID)
  return Number.isInteger(id) && id > 0 ? <PricingContent key={id} stationID={id} /> : <main data-public-page className="min-w-0 bg-transparent px-0 py-8 text-center text-sm text-danger">模型价格页面地址无效</main>
}
