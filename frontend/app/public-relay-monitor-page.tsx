"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Activity, TriangleAlert } from "lucide-react"
import { useParams } from "react-router-dom"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { GroupDegradationStatus } from "@/components/monitor/group-degradation-settings"
import { PlatformBadge } from "@/components/relay/platform-badge"
import { apiFetch } from "@/lib/api"
import type { PublicGroupMonitorBucket, PublicGroupMonitorGroup, PublicGroupMonitorStatus, PublicGroupMonitorView } from "@/lib/api-types"
import { cn } from "@/lib/utils"
import { PublicPageHeader, PublicPageTabs } from "@/components/public/public-page-header"
import { availabilityBand, availabilityBands, availabilityBar } from "@/lib/group-monitor"

const refreshSeconds = 30
const cacheVersion = 6
const monitorRanges = ["90m", "24h", "7d", "30d"] as const
type MonitorRange = (typeof monitorRanges)[number]

const rangeConfig: Record<MonitorRange, { label: string; description: string; bucketMS: number; bucketCount: number }> = {
  "90m": { label: "90m", description: "近 90 分钟 · 每格 5 分钟", bucketMS: 5 * 60 * 1000, bucketCount: 18 },
  "24h": { label: "24h", description: "近 24 小时 · 每格 1 小时", bucketMS: 60 * 60 * 1000, bucketCount: 24 },
  "7d": { label: "7d", description: "近 7 天 · 每格 6 小时", bucketMS: 6 * 60 * 60 * 1000, bucketCount: 28 },
  "30d": { label: "30d", description: "近 30 天 · 每格 1 天", bucketMS: 24 * 60 * 60 * 1000, bucketCount: 30 },
}

function cacheKey(stationID: number, range: MonitorRange) {
  return `gatewayops:group-monitor:v${cacheVersion}:${stationID}:${range}`
}

function readCache(stationID: number, range: MonitorRange): PublicGroupMonitorView | null {
  try {
    const data = JSON.parse(window.localStorage.getItem(cacheKey(stationID, range)) || "null") as PublicGroupMonitorView | null
    const at = new Date(data?.updated_at || "").getTime()
    return data?.station_id === stationID && data.range === range && Array.isArray(data.groups) && Date.now() - at < 5 * 60 * 1000 ? data : null
  } catch {
    return null
  }
}

const statuses: Record<PublicGroupMonitorStatus, { label: string; tone: string }> = {
  available: { label: "正常", tone: "bg-success/10 text-success" },
  degraded: { label: "不稳定", tone: "bg-warning/10 text-warning" },
  unavailable: { label: "不可用", tone: "bg-danger/10 text-danger" },
  idle: { label: "暂无请求", tone: "bg-muted text-muted-foreground" },
  disabled: { label: "已停用", tone: "bg-muted text-muted-foreground" },
  unknown: { label: "数据不完整", tone: "bg-muted text-muted-foreground" },
}

function percent(value: number | null | undefined) {
  return value == null || !Number.isFinite(value) ? "—" : `${(value * 100).toFixed(1)}%`
}

function latency(value: number | null | undefined) {
  return value == null || !Number.isFinite(value) || value < 0 ? "—" : value < 1000 ? `${Math.round(value)}ms` : `${(value / 1000).toFixed(1)}s`
}

function cacheStatusLabel(status?: PublicGroupMonitorGroup["cache_sample_status"]) {
  if (status === "ready") return "样本充足"
  return status === "incomplete" ? "采集不完整" : "样本不足"
}

function time(raw?: string | null, full = false) {
  const date = new Date(raw || "")
  return Number.isNaN(date.getTime()) ? "暂无" : date.toLocaleString("zh-CN", {
    ...(full ? { month: "2-digit", day: "2-digit" } as const : {}),
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
}

function rangeTime(raw: string | null | undefined, range: MonitorRange) {
  const date = new Date(raw || "")
  if (Number.isNaN(date.getTime())) return "暂无"
  if (range === "30d" || range === "7d") return date.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false })
}

function bucketsFor(group: PublicGroupMonitorGroup, range: MonitorRange): PublicGroupMonitorBucket[] {
  if (group.buckets?.length) return group.buckets
  const config = rangeConfig[range]
  const at = new Date(group.updated_at).getTime()
  const current = Math.floor((Number.isFinite(at) ? at : Date.now()) / config.bucketMS) * config.bucketMS
  return Array.from({ length: config.bucketCount }, (_, index) => ({
    start_at: new Date(current - (config.bucketCount - 1 - index) * config.bucketMS).toISOString(),
    end_at: new Date(current - (config.bucketCount - 2 - index) * config.bucketMS).toISOString(),
    total: 0,
    success_count: 0,
    failure_count: 0,
    data_complete: false,
  }))
}


function Timeline({ group, range }: { group: PublicGroupMonitorGroup; range: MonitorRange }) {
  const buckets = bucketsFor(group, range)
  return <section className="mt-4" aria-label={`${group.name}${rangeConfig[range].description}`}>
    <div className="mb-2 flex items-center justify-between gap-2 text-[11px] font-medium text-muted-foreground">
      <span>{rangeConfig[range].description}</span>
      <span>被动监控</span>
    </div>
    <div className="flex h-6 items-end gap-1" role="list" aria-label={`${group.name}时间趋势，可用率越高柱形越高`}>
      {buckets.map((bucket) => {
        const { shape, label: status } = availabilityBar(bucket)
        return <Tooltip key={bucket.start_at} delayDuration={120}>
          <TooltipTrigger asChild>
            <button type="button" role="listitem" className={cn("min-w-0 flex-1 rounded-[3px] transition-[filter] hover:brightness-110 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring", shape)} aria-label={`${rangeTime(bucket.start_at, range)} 至 ${rangeTime(bucket.end_at, range)}，${status}，${bucket.total} 次请求`} />
          </TooltipTrigger>
          <TooltipContent side="top" className="max-w-[min(23rem,calc(100vw-2rem))] border-0 bg-zinc-900 px-3 py-2 text-xs text-white shadow-lg">
            <p className="font-semibold">{time(bucket.start_at, true)}–{time(bucket.end_at)} · {status}</p>
            {bucket.total ? <p className="mt-1 leading-5">{bucket.total} 次请求 · 可用率 {percent(bucket.availability)} · 首 Token {latency(bucket.first_token_ms)}</p> : null}
            {!bucket.data_complete ? <p className="mt-1 text-zinc-300">日志源读取失败或超过采集上限，指标仅为部分样本。</p> : null}
          </TooltipContent>
        </Tooltip>
      })}
    </div>
    <div className="mt-1 flex justify-between text-[10px] tabular-nums text-muted-foreground">
      <time>{rangeTime(buckets[0]?.start_at, range)}</time>
      <time>{rangeTime(buckets[buckets.length - 1]?.end_at || group.updated_at, range)}</time>
    </div>
  </section>
}

function MonitorCard({ group, range, stationID }: { group: PublicGroupMonitorGroup; range: MonitorRange; stationID: number }) {
  const buckets = bucketsFor(group, range)
  const total = buckets.reduce((sum, bucket) => sum + bucket.total, 0)
  const success = buckets.reduce((sum, bucket) => sum + bucket.success_count, 0)
  const meta = statuses[group.status] ?? statuses.unknown
  const availability = total ? success / total : null
  const band = group.data_complete ? availabilityBand(availability) : null
  return <article className="flex h-full min-w-0 flex-col rounded-2xl border border-border/70 bg-card p-5 shadow-md shadow-slate-900/5 transition-shadow hover:shadow-lg motion-reduce:transition-none">
    <header className="flex items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="break-words text-base font-semibold">{group.name}</h3>
        <div className="mt-1.5 flex flex-wrap items-center gap-2"><PlatformBadge platform={group.platform} compact /><span className="text-[11px] font-medium text-muted-foreground">倍率 {group.rate_multiplier.toFixed(3)}x</span></div>
      </div>
      <span className={cn("shrink-0 rounded-full px-2.5 py-1 text-xs font-medium", meta.tone)}>{meta.label}</span>
    </header>
    {group.description ? <p className="mt-2 line-clamp-2 text-xs leading-5 text-muted-foreground">{group.description}</p> : null}
    <div className="pt-4">
      <div data-public-monitor-metrics className="grid grid-cols-3 gap-2"><Metric label="可用率" value={percent(availability)} tone={band?.textTone} /><Metric label="缓存率" value={group.cache_sample_status === "ready" ? percent(group.cache_rate) : cacheStatusLabel(group.cache_sample_status)} /><Metric label="首 Token" value={latency(group.first_token_ms)} /></div>
      <Timeline group={group} range={range} />
    </div>
    <GroupDegradationStatus stationID={stationID} groupID={group.external_id} />
  </article>
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return <div className="min-w-0 rounded-md border border-border/70 bg-muted/15 px-3 py-3"><p className="text-[11px] font-medium text-muted-foreground">{label}</p><p className={cn("mt-1.5 break-words font-mono text-base font-semibold tabular-nums", tone)}>{value}</p></div>
}

function LoadingCards() {
  return <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">{Array.from({ length: 6 }, (_, index) => <div key={index} className="rounded-2xl border border-border/70 bg-card p-5 shadow-md shadow-slate-900/5"><Skeleton className="h-5 w-32" /><Skeleton className="mt-6 h-16 w-full" /><Skeleton className="mt-5 h-5 w-full" /></div>)}</div>
}

function StationMonitor({ stationID }: { stationID: number }) {
  const [range, setRange] = useState<MonitorRange>("90m")
  const [data, setData] = useState<PublicGroupMonitorView | null>(() => readCache(stationID, "90m"))
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [countdown, setCountdown] = useState(refreshSeconds)
  const [error, setError] = useState("")
  const inFlight = useRef(false)
  const controller = useRef<AbortController | null>(null)

  const load = useCallback(async () => {
    if (inFlight.current) return
    inFlight.current = true
    setRefreshing(true)
    const abort = new AbortController()
    controller.current = abort
    try {
      const next = await apiFetch<PublicGroupMonitorView>(`/public/relay-stations/${stationID}/group-monitor?range=${encodeURIComponent(range)}`, { skipAuthErrorHandler: true, signal: abort.signal })
      if (abort.signal.aborted) return
      if (next.range !== range) throw new Error("监控时间范围响应不一致")
      setData(next)
      setError("")
      try { window.localStorage.setItem(cacheKey(stationID, range), JSON.stringify(next)) } catch { /* Live data works without storage. */ }
    } catch (e) {
      if (!abort.signal.aborted) setError(e instanceof Error ? e.message : "监控数据读取失败")
    } finally {
      if (controller.current === abort) {
        inFlight.current = false
        if (!abort.signal.aborted) { setLoading(false); setRefreshing(false); setCountdown(refreshSeconds) }
      }
    }
  }, [range, stationID])

  useEffect(() => {
    setData(readCache(stationID, range))
    setLoading(true)
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, refreshSeconds * 1000)
    const clock = window.setInterval(() => setCountdown((current) => Math.max(current - 1, 0)), 1000)
    return () => { controller.current?.abort(); inFlight.current = false; window.clearInterval(timer); window.clearInterval(clock) }
  }, [load, range, stationID])

  useEffect(() => { document.title = "渠道状态 · 分组监控" }, [])

  const platforms = useMemo(() => {
    const groups = new Map<string, PublicGroupMonitorGroup[]>()
    for (const group of data?.groups ?? []) {
      const platform = group.platform || "其他"
      groups.set(platform, [...(groups.get(platform) ?? []), group])
    }
    return [...groups.entries()]
  }, [data?.groups])

  const summary = useMemo(() => {
    let total = 0
    let success = 0
    let cachePromptTokens = 0
    let cacheReadTokens = 0
    let cacheGroupCount = 0
    for (const group of data?.groups ?? []) {
      const buckets = bucketsFor(group, range)
      total += buckets.reduce((sum, bucket) => sum + bucket.total, 0)
      success += buckets.reduce((sum, bucket) => sum + bucket.success_count, 0)
      if (group.cache_sample_status === "ready") {
        cacheGroupCount++
        cachePromptTokens += group.cache_rate_prompt_tokens
        cacheReadTokens += group.cache_rate_read_tokens
      }
    }
    return { availability: total ? success / total : null, cacheRate: cachePromptTokens > 0 ? cacheReadTokens / cachePromptTokens : null, cacheGroupCount }
  }, [data?.groups, range])

  return <main data-public-page className="min-h-screen min-w-0 bg-transparent text-foreground"><div className="w-full min-w-0">
    <PublicPageHeader
      icon={Activity}
      title="渠道状态"
      description="分组可用率、缓存率与首 Token 响应趋势"
      updatedAt={data?.updated_at}
      refreshing={refreshing}
      refreshIn={countdown}
      refreshLabel="刷新监控"
      onRefresh={() => void load()}
      controls={<PublicPageTabs items={monitorRanges.map((value) => ({ value, label: rangeConfig[value].label }))} value={range} label="监控时间范围" onValueChange={(value) => { if (value !== range) { setRange(value as MonitorRange); setCountdown(refreshSeconds) } }} />}
      metadata={<><span>{data?.groups.length ?? 0} 组</span><span>可用率 {percent(summary.availability)} · 缓存率 {summary.cacheRate == null ? "样本不足" : `${percent(summary.cacheRate)} · 有效组 ${summary.cacheGroupCount}/${data?.groups.length ?? 0}`}</span></>}
    />
    <div className="mb-3 flex flex-wrap items-center justify-between gap-3 text-[11px] text-muted-foreground"><p>{rangeConfig[range].description}，数据来自已有调用日志，不主动探测。柱形越高代表可用率越高；无请求或采集不完整时不参与颜色分级。</p><div className="flex flex-wrap gap-3" aria-label="可用率分级图例">{availabilityBands.map((band) => <span key={band.minimum} className="flex items-end gap-1"><i aria-hidden="true" className={cn("w-2 rounded-sm", band.shape)} />{band.label}</span>)}<span className="flex items-end gap-1"><i aria-hidden="true" className="mb-0.5 h-[3px] w-2 rounded-sm bg-zinc-300" />无请求或未知</span></div></div>
    {error ? <p role="alert" className="mb-4 flex items-center gap-2 rounded-md border border-danger/30 bg-danger/5 p-3 text-sm text-danger"><TriangleAlert className="size-4 shrink-0" />{error}{data ? "，正在显示上次数据" : ""}</p> : null}
    {loading && !data ? <LoadingCards /> : !data?.groups.length ? <p className="rounded-lg border border-dashed border-border px-5 py-16 text-center text-sm text-muted-foreground">当前没有开启监控的分组</p> : <div className="space-y-7">{platforms.map(([platform, groups]) => <section key={platform} aria-label={`${platform}分组`}><div className="mb-3 flex items-center gap-2"><PlatformBadge platform={platform} /><span className="text-xs text-muted-foreground">{groups.length}</span></div><div className="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">{groups.map((group) => <MonitorCard key={group.external_id} group={group} range={range} stationID={stationID} />)}</div></section>)}</div>}
  </div></main>
}

export default function PublicRelayMonitorPage() {
  const { stationID } = useParams()
  const id = Number(stationID)
  return Number.isInteger(id) && id > 0 ? <StationMonitor key={id} stationID={id} /> : <main data-public-page className="min-w-0 bg-transparent px-0 py-8 text-center text-sm text-danger">监控页面地址无效</main>
}
