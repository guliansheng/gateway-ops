import { useCallback, useEffect, useMemo, useState } from "react"
import { BadgeDollarSign, Building2, Database, Layers3, RefreshCw, Search, Tags } from "lucide-react"
import { useParams } from "react-router-dom"
import alibabaCloudIcon from "@lobehub/icons-static-svg/icons/alibabacloud-color.svg"
import anthropicIcon from "@lobehub/icons-static-svg/icons/anthropic.svg"
import awsIcon from "@lobehub/icons-static-svg/icons/aws-color.svg"
import baichuanIcon from "@lobehub/icons-static-svg/icons/baichuan-color.svg"
import baiduIcon from "@lobehub/icons-static-svg/icons/baidu-color.svg"
import byteDanceIcon from "@lobehub/icons-static-svg/icons/bytedance-color.svg"
import cohereIcon from "@lobehub/icons-static-svg/icons/cohere-color.svg"
import deepSeekIcon from "@lobehub/icons-static-svg/icons/deepseek-color.svg"
import googleIcon from "@lobehub/icons-static-svg/icons/google-color.svg"
import huaweiIcon from "@lobehub/icons-static-svg/icons/huawei-color.svg"
import iflytekIcon from "@lobehub/icons-static-svg/icons/iflytekcloud-color.svg"
import metaIcon from "@lobehub/icons-static-svg/icons/meta-color.svg"
import microsoftIcon from "@lobehub/icons-static-svg/icons/microsoft-color.svg"
import miniMaxIcon from "@lobehub/icons-static-svg/icons/minimax-color.svg"
import mistralIcon from "@lobehub/icons-static-svg/icons/mistral-color.svg"
import moonshotIcon from "@lobehub/icons-static-svg/icons/moonshot.svg"
import nvidiaIcon from "@lobehub/icons-static-svg/icons/nvidia-color.svg"
import openAIIcon from "@lobehub/icons-static-svg/icons/openai.svg"
import senseNovaIcon from "@lobehub/icons-static-svg/icons/sensenova-color.svg"
import stepFunIcon from "@lobehub/icons-static-svg/icons/stepfun-color.svg"
import tencentIcon from "@lobehub/icons-static-svg/icons/tencent-color.svg"
import xAIIcon from "@lobehub/icons-static-svg/icons/xai.svg"
import xiaomiIcon from "@lobehub/icons-static-svg/icons/xiaomimimo.svg"
import zeroOneIcon from "@lobehub/icons-static-svg/icons/zeroone-color.svg"
import zhipuIcon from "@lobehub/icons-static-svg/icons/zhipu-color.svg"
import { apiFetch } from "@/lib/api"
import type { PublicModelPricingItem, PublicModelPricingView } from "@/lib/api-types"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

const tokenPriceScale = 1_000_000
const browserCacheTTL = 60 * 60 * 1000
const browserCacheVersion = 1

type BrowserPricingCache = {
  version: number
  cached_at: number
  data: PublicModelPricingView
}

function browserCacheKey(stationID: number) {
  return `gatewayops:model-pricing:v${browserCacheVersion}:${stationID}`
}

function readBrowserCache(stationID: number): BrowserPricingCache | null {
  if (typeof window === "undefined" || !Number.isInteger(stationID) || stationID <= 0) return null
  try {
    const raw = window.localStorage.getItem(browserCacheKey(stationID))
    if (!raw) return null
    const cached = JSON.parse(raw) as BrowserPricingCache
    if (cached.version !== browserCacheVersion || cached.data?.station_id !== stationID || !Number.isFinite(cached.cached_at) || !Array.isArray(cached.data?.items)) return null
    return cached
  } catch {
    return null
  }
}

function writeBrowserCache(stationID: number, data: PublicModelPricingView, cachedAt: number) {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(browserCacheKey(stationID), JSON.stringify({ version: browserCacheVersion, cached_at: cachedAt, data } satisfies BrowserPricingCache))
  } catch {
    // Storage can be unavailable in private browsing; the page still works without persistence.
  }
}

const sourceLabels: Record<PublicModelPricingItem["price_source"], string> = {
  system_default: "系统默认",
  channel_override: "渠道定价",
  channel_override_with_default: "渠道覆盖 + 默认补全",
  group_image_pricing: "分组生图定价",
  unavailable: "价格不完整",
}

const billingModeLabels: Record<string, string> = {
  token: "Token 计费",
  per_request: "按次计费",
}

const companyIcons: Record<string, { src: string; invertOnDark?: boolean }> = {
  "amazon": { src: awsIcon },
  "anthropic": { src: anthropicIcon, invertOnDark: true },
  "baidu": { src: baiduIcon },
  "cohere": { src: cohereIcon },
  "deepseek": { src: deepSeekIcon },
  "google": { src: googleIcon },
  "meta": { src: metaIcon },
  "microsoft": { src: microsoftIcon },
  "minimax": { src: miniMaxIcon },
  "mistral ai": { src: mistralIcon },
  "nvidia": { src: nvidiaIcon },
  "openai": { src: openAIIcon, invertOnDark: true },
  "xai": { src: xAIIcon, invertOnDark: true },
  "商汤": { src: senseNovaIcon },
  "字节跳动": { src: byteDanceIcon },
  "科大讯飞": { src: iflytekIcon },
  "腾讯": { src: tencentIcon },
  "小米": { src: xiaomiIcon, invertOnDark: true },
  "华为": { src: huaweiIcon },
  "百度": { src: baiduIcon },
  "百川智能": { src: baichuanIcon },
  "阶跃星辰": { src: stepFunIcon },
  "零一万物": { src: zeroOneIcon },
  "智谱 ai": { src: zhipuIcon },
  "月之暗面": { src: moonshotIcon, invertOnDark: true },
  "阿里云": { src: alibabaCloudIcon },
}

const companyOrder = ["openai", "anthropic", "xai", "google", "meta", "mistral ai", "cohere", "amazon", "microsoft", "nvidia", "deepseek", "阿里云", "字节跳动", "腾讯", "百度", "华为", "智谱 ai", "月之暗面", "minimax", "小米", "百川智能", "阶跃星辰", "零一万物", "科大讯飞", "商汤"]
const companyRank = new Map(companyOrder.map((name, index) => [name, index]))

function normalizedCompanyName(company: string) {
  return company.trim().toLowerCase()
}

function compareCompanies(left: string, right: string) {
  const leftRank = companyRank.get(normalizedCompanyName(left)) ?? Number.MAX_SAFE_INTEGER
  const rightRank = companyRank.get(normalizedCompanyName(right)) ?? Number.MAX_SAFE_INTEGER
  return leftRank - rightRank || left.localeCompare(right, "zh-CN")
}

function formatPrice(value: number | null | undefined, perRequest = false) {
  if (value == null || !Number.isFinite(value)) return "-"
  const normalized = perRequest ? value : value * tokenPriceScale
  return normalized.toLocaleString("zh-CN", { maximumFractionDigits: 6, minimumFractionDigits: 0 })
}

function PriceCell({ label, value, perRequest = false }: { label: string; value?: number | null; perRequest?: boolean }) {
  return <div className="min-w-0 border-l-2 border-border pl-3"><p className="text-xs font-medium leading-5 text-foreground/70">{label}</p><p className="mt-0.5 truncate font-mono text-sm font-semibold leading-5 tabular-nums text-foreground" title={formatPrice(value, perRequest)}>{formatPrice(value, perRequest)}</p></div>
}

function CompanyLogo({ company }: { company: string }) {
  const icon = companyIcons[normalizedCompanyName(company)]
  if (!icon) return <Building2 className="size-5 text-brand" aria-hidden="true" />
  return <img src={icon.src} alt="" className={cn("size-5 shrink-0 object-contain", icon.invertOnDark && "dark:invert")} aria-hidden="true" />
}

function PricingBadges({ items }: { items: PublicModelPricingItem[] }) {
  const sources = [...new Set(items.map((item) => item.price_source))]
  const modes = [...new Set(items.map((item) => item.billing_mode || "token"))]
  return <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
    {sources.map((source) => <span key={source} className={cn("rounded border px-2 py-1 text-[11px] font-semibold", source === "channel_override" || source === "group_image_pricing" ? "border-brand/20 bg-brand/10 text-brand" : source === "channel_override_with_default" ? "border-warning/20 bg-warning/10 text-warning" : source === "unavailable" ? "border-danger/20 bg-danger/10 text-danger" : "border-border bg-background text-foreground/70")}>{sourceLabels[source]}</span>)}
    {modes.map((mode) => <span key={mode} className="rounded border border-border bg-background px-2 py-1 text-[11px] font-semibold text-foreground/70">{billingModeLabels[mode] || mode}</span>)}
  </div>
}

function ChannelPricing({ item }: { item: PublicModelPricingItem }) {
  const details = Boolean(item.intervals?.length || item.time_pricing || item.fast_multiplier || item.flex_multiplier)
  const resolutionImagePricing = item.image_price_1k != null || item.image_price_2k != null || item.image_price_4k != null
  const showChannel = item.channel_id > 0 && (item.price_source === "channel_override" || item.price_source === "channel_override_with_default" || item.price_source === "group_image_pricing")
  return <section className="px-5 py-5">
    {resolutionImagePricing ? <div className="grid grid-cols-3 gap-x-4"><PriceCell label="1K / 次" value={item.image_price_1k} perRequest /><PriceCell label="2K / 次" value={item.image_price_2k} perRequest /><PriceCell label="4K / 次" value={item.image_price_4k} perRequest /></div> : <div className="grid grid-cols-2 gap-x-4 gap-y-4 sm:grid-cols-4"><PriceCell label="输入 / 1M tokens" value={item.input_price} /><PriceCell label="输出 / 1M tokens" value={item.output_price} /><PriceCell label="缓存写入 / 1M" value={item.cache_write_price} /><PriceCell label="缓存读取 / 1M" value={item.cache_read_price} /><PriceCell label="图片输入 / 1M" value={item.image_input_price} /><PriceCell label="图片输出 / 1M" value={item.image_output_price} /><PriceCell label="按次" value={item.per_request_price} perRequest /></div>}
    <div className="mt-4 flex min-w-0 flex-wrap items-center gap-1.5 border-t border-border/80 pt-3">{showChannel ? <span className="max-w-full truncate rounded border border-brand/20 bg-brand/10 px-2 py-1 text-[11px] font-semibold text-brand" title={item.channel_name}>渠道：{item.channel_name}</span> : null}{item.billing_model && item.billing_model !== item.model ? <span className="max-w-full truncate rounded border border-warning/25 bg-warning/10 px-2 py-1 text-[11px] font-semibold text-warning" title={item.billing_model}>计费模型：{item.billing_model}</span> : null}{item.provider ? <span className="max-w-full truncate rounded border border-border bg-muted px-2 py-1 text-[11px] font-semibold text-foreground/75" title={item.provider}>Provider：{item.provider}</span> : null}{item.groups?.length ? <span className="max-w-full truncate rounded border border-success/25 bg-success/10 px-2 py-1 text-[11px] font-semibold text-success" title={item.groups.join("、")}>分组：{item.groups.join("、")}</span> : null}</div>
    {details ? <details className="mt-4 text-xs text-foreground/70"><summary className="cursor-pointer font-medium transition-colors hover:text-foreground">高级定价规则</summary><div className="mt-2 space-y-2 rounded-md bg-muted/60 p-3">{item.fast_multiplier ? <p>Fast 倍率：<span className="font-mono">{item.fast_multiplier}</span></p> : null}{item.flex_multiplier ? <p>Flex 倍率：<span className="font-mono">{item.flex_multiplier}</span></p> : null}{item.intervals?.length ? <pre className="max-h-40 overflow-auto font-mono text-xs">{JSON.stringify(item.intervals, null, 2)}</pre> : null}{item.time_pricing ? <pre className="max-h-40 overflow-auto font-mono text-xs">{JSON.stringify(item.time_pricing, null, 2)}</pre> : null}</div></details> : null}
  </section>
}

function ModelPricingCard({ model, items }: { model: string; items: PublicModelPricingItem[] }) {
  return <article className="overflow-hidden rounded-md border border-border bg-card shadow-sm transition-[border-color,box-shadow] duration-200 hover:border-brand/50 hover:shadow-md">
    <header className="flex min-h-14 flex-wrap items-center justify-between gap-3 border-b border-border bg-muted/60 px-5 py-3.5">
      <h3 className="min-w-0 truncate text-base font-bold text-foreground" title={model}>{model}</h3>
      <div className="flex min-w-0 flex-wrap items-center justify-end gap-1.5">{items.length > 1 ? <span className="shrink-0 rounded-md border border-border bg-background px-2 py-1 text-xs font-semibold text-foreground/70">{items.length} 个渠道</span> : null}<PricingBadges items={items} /></div>
    </header>
    <div className="divide-y divide-border">{items.map((item, index) => <ChannelPricing key={`${item.station_id}-${item.channel_id}-${item.billing_model}-${index}`} item={item} />)}</div>
  </article>
}

export default function PublicModelPricingPage() {
  const { stationID } = useParams()
  const numericStationID = Number(stationID)
  const initialCache = useMemo(() => readBrowserCache(numericStationID), [numericStationID])
  const [data, setData] = useState<PublicModelPricingView | null>(initialCache?.data ?? null)
  const [cacheUpdatedAt, setCacheUpdatedAt] = useState(initialCache?.cached_at ?? 0)
  const [loading, setLoading] = useState(!initialCache)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [company, setCompany] = useState("all")
  const [billingMode, setBillingMode] = useState("all")

  const load = useCallback(async (silent = false) => {
    if (!Number.isInteger(numericStationID) || numericStationID <= 0) {
      setError("模型价格页面地址无效")
      setLoading(false)
      return
    }
    if (silent) setRefreshing(true); else setLoading(true)
    try { const nextData = await apiFetch<PublicModelPricingView>(`/public/relay-stations/${numericStationID}/model-pricing`, { skipAuthErrorHandler: true }); const cachedAt = Date.now(); setData(nextData); setCacheUpdatedAt(cachedAt); writeBrowserCache(numericStationID, nextData, cachedAt); setError(null) } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "价格读取失败") } finally { setLoading(false); setRefreshing(false) }
  }, [numericStationID])

  useEffect(() => {
    const age = Date.now() - cacheUpdatedAt
    if (!data || cacheUpdatedAt <= 0 || age >= browserCacheTTL) { void load(Boolean(data)); return }
    const timer = window.setTimeout(() => void load(true), browserCacheTTL - age)
    return () => window.clearTimeout(timer)
  }, [cacheUpdatedAt, data, load])
  useEffect(() => { document.title = data?.station_name ? `${data.station_name} · 模型价格` : "模型价格 · GatewayOps" }, [data?.station_name])

  const items = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return (data?.items ?? []).filter((item) => (!normalized || [item.model, item.billing_model, item.company, item.provider, item.channel_name].some((value) => value?.toLowerCase().includes(normalized))) && (company === "all" || item.company === company) && (billingMode === "all" || item.billing_mode === billingMode))
  }, [data?.items, query, company, billingMode])

  const grouped = useMemo(() => {
    const companies = new Map<string, Map<string, PublicModelPricingItem[]>>()
    for (const item of items) { if (!companies.has(item.company)) companies.set(item.company, new Map()); const models = companies.get(item.company)!; models.set(item.model, [...(models.get(item.model) ?? []), item]) }
    return [...companies.entries()].sort(([left], [right]) => compareCompanies(left, right))
  }, [items])

  const orderedCompanies = useMemo(() => [...(data?.summary.companies_list ?? [])].sort(compareCompanies), [data?.summary.companies_list])

  return <main className="min-h-screen bg-background text-foreground"><div className="mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
    <header className="flex min-h-16 flex-wrap items-start justify-between gap-4 border-b border-border pb-5"><div className="min-w-0"><h1 className="flex items-center gap-2 text-xl font-bold sm:text-2xl"><BadgeDollarSign className="size-5 shrink-0 text-brand sm:size-6" /><span className="truncate">{data?.station_name || "模型价格"}</span></h1><p className="mt-1.5 text-sm text-muted-foreground">仅展示该中转站当前可用模型；Token 价格按每 1M tokens，按次价格按单次请求及分辨率展示</p></div><div className="ml-auto flex items-center gap-2"><span className="whitespace-nowrap text-xs font-medium text-foreground/65">最新更新：{data ? new Date(data.updated_at).toLocaleString("zh-CN", { hour12: false }) : "暂无"}</span><Button type="button" variant="outline" size="icon" className="size-11 sm:size-9" aria-label="刷新模型价格" disabled={refreshing} onClick={() => void load(true)}><RefreshCw className={cn("size-4", refreshing && "animate-spin")} /></Button></div></header>
    {data ? <section className="grid grid-cols-3 border-b border-border" aria-label="价格汇总"><div className="py-4 sm:px-4 sm:first:pl-0"><p className="text-[11px] text-muted-foreground">当前模型</p><p className="mt-1 font-mono text-xl font-bold tabular-nums">{data.summary.models}</p></div><div className="py-4 sm:px-4"><p className="text-[11px] text-muted-foreground">所属平台</p><p className="mt-1 font-mono text-xl font-bold tabular-nums">{data.summary.companies}</p></div><div className="py-4 sm:px-4"><p className="text-[11px] text-muted-foreground">渠道定价</p><p className="mt-1 font-mono text-xl font-bold tabular-nums">{data.summary.channels}</p></div></section> : null}
    <section className="mt-4 flex flex-wrap items-center gap-2 border-b border-border pb-3"><div className="relative min-w-[240px] flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型、平台、Provider 或渠道" className="h-11 pl-9 text-base sm:h-10 sm:text-sm" /></div><Select value={company} onValueChange={setCompany}><SelectTrigger className="h-11 w-[calc(50%-4px)] border-brand/40 bg-card text-xs font-semibold hover:border-brand/70 sm:h-10 sm:w-44"><Layers3 className="mr-1.5 size-3.5 text-brand" /><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部平台</SelectItem>{orderedCompanies.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select><Select value={billingMode} onValueChange={setBillingMode}><SelectTrigger className="h-11 w-[calc(50%-4px)] text-xs sm:h-10 sm:w-40"><Tags className="mr-1.5 size-3.5 text-muted-foreground" /><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部计费模式</SelectItem>{(data?.summary.billing_modes ?? []).map((value) => <SelectItem key={value} value={value}>{billingModeLabels[value] || value}</SelectItem>)}</SelectContent></Select></section>
    {error ? <p className="mt-4 border border-danger/30 bg-danger/5 px-3 py-2 text-sm font-medium text-danger">{error}</p> : null}
    <section className="mt-5" aria-label="模型价格列表">{loading ? <div className="grid gap-3 min-[1000px]:grid-cols-2">{Array.from({ length: 8 }).map((_, index) => <div key={index} className="rounded-md border border-border p-5 shadow-sm"><Skeleton className="h-5 w-48" /><Skeleton className="mt-5 h-20 w-full" /></div>)}</div> : grouped.length === 0 ? <div className="border border-dashed border-border px-4 py-14 text-center text-sm text-muted-foreground"><Database className="mx-auto mb-2 size-5" />当前中转站暂无匹配的可用模型价格</div> : <div className="space-y-10">{grouped.map(([companyName, models]) => <section key={companyName} className="space-y-4" aria-labelledby={`company-${companyName}`}><div className="flex min-h-16 items-center gap-3 border-b-2 border-brand/60 pb-3"><span className="flex size-11 shrink-0 items-center justify-center rounded-md bg-muted shadow-sm"><CompanyLogo company={companyName} /></span><div className="min-w-0"><p className="text-xs font-semibold text-brand">平台分类</p><h2 id={`company-${companyName}`} className="truncate text-xl font-bold text-foreground">{companyName}</h2></div><span className="ml-auto shrink-0 text-sm font-semibold text-foreground/70">{models.size} 个模型</span></div><div className="grid items-start gap-4 min-[1000px]:grid-cols-2">{[...models.entries()].sort(([left], [right]) => left.localeCompare(right, "en", { numeric: true, sensitivity: "base" })).map(([model, modelItems]) => <ModelPricingCard key={model} model={model} items={modelItems} />)}</div></section>)}</div>}</section>
  </div></main>
}
