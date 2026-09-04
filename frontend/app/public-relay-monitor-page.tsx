"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { Activity, Ban, CheckCircle2, CircleHelp, CircleX, Clock3, Info, RefreshCw, TriangleAlert } from "lucide-react"
import { useParams } from "react-router-dom"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import type { PublicGroupMonitorCall, PublicGroupMonitorGroup, PublicGroupMonitorStatus, PublicGroupMonitorView } from "@/lib/api-types"
import { cn } from "@/lib/utils"
import { PlatformBadge } from "@/components/relay/platform-badge"
import { RecentUsageCard, RecentUsageTooltip, recentUsageTooltipNarrowClassName } from "@/components/monitor/recent-usage-tooltip"

const refreshIntervalSeconds = 30
const refreshIntervalMS = refreshIntervalSeconds * 1_000
const monitorBrowserCacheVersion = 1

type MonitorBrowserCache = {
  version: number
  data: PublicGroupMonitorView
}

function monitorBrowserCacheKey(stationID: number) {
  return `gatewayops:group-monitor:v${monitorBrowserCacheVersion}:${stationID}`
}

function readMonitorBrowserCache(stationID: number): PublicGroupMonitorView | null {
  if (typeof window === "undefined" || !Number.isInteger(stationID) || stationID <= 0) return null
  try {
    const raw = window.localStorage.getItem(monitorBrowserCacheKey(stationID))
    if (!raw) return null
    const cached = JSON.parse(raw) as MonitorBrowserCache
    if (cached.version !== monitorBrowserCacheVersion || cached.data?.station_id !== stationID || !Array.isArray(cached.data?.groups)) return null
    return cached.data
  } catch {
    return null
  }
}

function writeMonitorBrowserCache(stationID: number, data: PublicGroupMonitorView) {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(monitorBrowserCacheKey(stationID), JSON.stringify({ version: monitorBrowserCacheVersion, data } satisfies MonitorBrowserCache))
  } catch {
    // Storage can be unavailable in private browsing; live refresh remains available.
  }
}

const statusMeta: Record<PublicGroupMonitorStatus, { label: string; className: string; pillClassName: string; icon: typeof CheckCircle2 }> = {
  available: { label: "可用", className: "text-success", pillClassName: "border-success/25 bg-success/10", icon: CheckCircle2 },
  degraded: { label: "不稳定", className: "text-warning", pillClassName: "border-warning/25 bg-warning/10", icon: TriangleAlert },
  unavailable: { label: "不可用", className: "text-danger", pillClassName: "border-danger/25 bg-danger/10", icon: CircleX },
  idle: { label: "暂无调用", className: "text-muted-foreground", pillClassName: "border-border bg-muted/60", icon: Clock3 },
  disabled: { label: "已停用", className: "text-muted-foreground", pillClassName: "border-border bg-muted/60", icon: Ban },
  unknown: { label: "状态未知", className: "text-warning", pillClassName: "border-warning/25 bg-warning/10", icon: CircleHelp },
}

type LatencyTone = "good" | "warn" | "slow" | "critical"

function latencyTone(value: number | null | undefined, metric: "first" | "duration"): LatencyTone {
  if (value == null || !Number.isFinite(value) || value < 0) return "critical"
  const thresholds = metric === "first"
    ? { warn: 10_000, slow: 30_000, critical: 60_000 }
    : { warn: 60_000, slow: 180_000, critical: 300_000 }
  if (value >= thresholds.critical) return "critical"
  if (value >= thresholds.slow) return "slow"
  if (value >= thresholds.warn) return "warn"
  return "good"
}

const latencyColor: Record<LatencyTone, string> = {
  good: "#10b981",
  warn: "#f59e0b",
  slow: "#f97316",
  critical: "#ef4444",
}

const latencyTextClass: Record<LatencyTone, string> = {
  good: "text-emerald-600 dark:text-emerald-400",
  warn: "text-amber-600 dark:text-amber-400",
  slow: "text-orange-600 dark:text-orange-400",
  critical: "text-red-600 dark:text-red-400",
}

function formatLatency(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value) || value < 0) return "-"
  const milliseconds = Math.round(value)
  if (milliseconds < 1_000) return `${milliseconds}ms`
  const totalSeconds = milliseconds / 1_000
  if (totalSeconds < 60) return `${totalSeconds.toFixed(totalSeconds < 10 ? 1 : 0)}秒`
  const hours = Math.floor(totalSeconds / 3_600)
  const minutes = Math.floor((totalSeconds % 3_600) / 60)
  const seconds = totalSeconds % 60
  const secondsText = seconds.toFixed(seconds < 10 && seconds % 1 !== 0 ? 1 : 0).padStart(2, "0")
  if (hours > 0) return `${hours}时 ${String(minutes).padStart(2, "0")}分 ${secondsText}秒`
  return `${minutes}分 ${secondsText}秒`
}

function formatTime(value: string | null | undefined) {
  if (!value) return "暂无"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "暂无"
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date)
}

function LatencyBars({ calls, groupName }: { calls: PublicGroupMonitorCall[]; groupName: string }) {
  const ordered = useMemo(() => [...calls].reverse(), [calls])
  if (ordered.length === 0) {
    return <span className="block w-full text-xs text-muted-foreground">暂无调用记录</span>
  }
  return <Tooltip delayDuration={160}>
    <TooltipTrigger asChild>
      <button type="button" className="block h-10 w-full rounded-sm px-1 py-1 transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`查看${groupName}最近${ordered.length}次调用`}>
        <span className="flex h-8 w-full items-stretch gap-px overflow-hidden" aria-hidden="true">
          {ordered.map((call, index) => call.success ? <span key={`${call.created_at}-${index}`} className="min-w-0 flex-1 rounded-[3px] opacity-90 transition-opacity hover:opacity-100" style={{ background: `linear-gradient(to top, ${latencyColor[latencyTone(call.duration_ms, "duration")]} 0 50%, ${latencyColor[latencyTone(call.first_token_ms, "first")]} 50% 100%)` }} /> : <span key={`${call.created_at}-${index}`} className="min-w-0 flex-1 rounded-[3px] bg-red-500 opacity-90 transition-opacity hover:opacity-100" />)}
        </span>
      </button>
    </TooltipTrigger>
    <TooltipContent side="bottom" align="end" className={recentUsageTooltipNarrowClassName}>
      <RecentUsageTooltip count={ordered.length}>
        {[...ordered].reverse().map((call, index) => {
          const first = latencyTone(call.first_token_ms, "first")
          const duration = latencyTone(call.duration_ms, "duration")
          return (
            <RecentUsageCard key={`${call.created_at}-detail-${index}`}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <time dateTime={call.created_at} className="whitespace-nowrap font-mono text-xs font-semibold tabular-nums text-slate-700">{formatTime(call.created_at)}</time>
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[10px] font-medium text-slate-500">#{ordered.length - index}</span>
              </div>
              <div className="mt-2 min-w-0 rounded-md bg-slate-50 px-2.5 py-2 ring-1 ring-slate-200/70">
                <p className="truncate font-mono text-sm font-semibold leading-5 text-slate-950" title={call.model || "-"}>{call.model || "-"}</p>
              </div>
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 rounded-md bg-slate-50 px-2.5 py-2 font-mono text-xs font-semibold tabular-nums ring-1 ring-slate-200/70">
                {call.success ? <><span className={latencyTextClass[first]}>首字 {formatLatency(call.first_token_ms)}</span><span className={latencyTextClass[duration]}>总耗时 {formatLatency(call.duration_ms)}</span></> : <span className="text-red-600">调用失败{call.duration_ms != null ? ` · 总耗时 ${formatLatency(call.duration_ms)}` : ""}</span>}
              </div>
            </RecentUsageCard>
          )
        })}
      </RecentUsageTooltip>
    </TooltipContent>
  </Tooltip>
}

function Status({ status }: { status: PublicGroupMonitorStatus }) {
  const meta = statusMeta[status]
  const Icon = meta.icon
  return <span className={cn("inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs font-semibold", meta.className, meta.pillClassName)}><Icon className="size-3.5" />{meta.label}</span>
}

function successRate(group: PublicGroupMonitorGroup) {
  const total = group.success_count + group.failure_count
  return total > 0 ? `${((group.success_count / total) * 100).toFixed(1)}%` : "-"
}

function MonitorRow({ group }: { group: PublicGroupMonitorGroup }) {
  return <article className="group overflow-hidden rounded-[5px] border border-border/80 bg-card shadow-sm transition-[border-color,box-shadow] duration-200 hover:border-brand/40 hover:shadow-md">
    <div className="p-4 sm:p-5">
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2.5">
            <h2 className="min-w-0 truncate text-base font-semibold text-foreground" title={group.name}>{group.name}</h2>
            <span className="inline-flex shrink-0 items-center rounded-md border border-brand/20 bg-brand/5 px-2 py-1 font-mono text-[11px] font-semibold tabular-nums text-brand">倍率 {group.rate_multiplier.toFixed(3)}</span>
            <PlatformBadge platform={group.platform} />
          </div>
          <p className="mt-2 line-clamp-2 text-xs leading-5 text-muted-foreground" title={group.description || "暂无描述"}>{group.description || "暂无描述"}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Status status={group.status} />
          <span className="inline-flex items-center rounded-md border border-brand/20 bg-brand/5 px-2.5 py-1.5 font-mono text-xs font-semibold tabular-nums text-brand">成功率 {successRate(group)}</span>
        </div>
      </div>

      <div className="mt-4 border-t border-border/70 pt-3.5">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2 text-[11px]">
          <span className="flex min-w-0 items-center gap-2 text-muted-foreground"><span className="font-medium text-foreground/80">最近调用</span>{group.failure_summary ? <span className="truncate text-xs font-semibold text-warning" title={group.failure_summary}>{group.failure_summary}</span> : null}</span>
          <span className="shrink-0 font-mono text-sm font-semibold tabular-nums text-foreground">{formatTime(group.latest_call_at)}</span>
        </div>
        <div className="mt-2 rounded-md bg-muted/35 px-2 py-1.5">
          <LatencyBars calls={group.calls} groupName={group.name} />
        </div>
      </div>

    </div>
  </article>
}

function LoadingCards() {
  return <div className="grid min-h-[420px] gap-3 min-[1150px]:grid-cols-2">{Array.from({ length: 6 }).map((_, index) => <div key={index} className="overflow-hidden rounded-[5px] border border-border/80 bg-card p-4 shadow-sm sm:p-5"><div className="flex items-start justify-between gap-3"><div><Skeleton className="h-4 w-32" /><Skeleton className="mt-2 h-3 w-24" /></div><div className="flex gap-2"><Skeleton className="h-7 w-16 rounded-md" /><Skeleton className="h-7 w-20 rounded-md" /></div></div><div className="mt-5 border-t border-border/70 pt-3.5"><Skeleton className="mb-2 h-3 w-24" /><Skeleton className="h-10 w-full rounded-md" /></div></div>)}</div>
}

export default function PublicRelayMonitorPage() {
  const { stationID } = useParams()
  const numericStationID = Number(stationID)
  const initialCache = useMemo(() => readMonitorBrowserCache(numericStationID), [numericStationID])
  const [data, setData] = useState<PublicGroupMonitorView | null>(initialCache)
  const [loading, setLoading] = useState(!initialCache)
  const [refreshing, setRefreshing] = useState(false)
  const [countdown, setCountdown] = useState(refreshIntervalSeconds)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (silent = false) => {
    if (!Number.isInteger(numericStationID) || numericStationID <= 0) {
      setError("监控页面地址无效")
      setLoading(false)
      return
    }
    if (silent) setRefreshing(true)
    try {
      const next = await apiFetch<PublicGroupMonitorView>(`/public/relay-stations/${numericStationID}/group-monitor`, { skipAuthErrorHandler: true })
      setData(next)
      writeMonitorBrowserCache(numericStationID, next)
      setError(null)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "监控数据读取失败")
    } finally {
      setLoading(false)
      setRefreshing(false)
      setCountdown(refreshIntervalSeconds)
    }
  }, [numericStationID])

  useEffect(() => {
    void load(Boolean(initialCache))
    const timer = window.setInterval(() => void load(true), refreshIntervalMS)
    const countdownTimer = window.setInterval(() => setCountdown((current) => current <= 1 ? refreshIntervalSeconds : current - 1), 1_000)
    return () => {
      window.clearInterval(timer)
      window.clearInterval(countdownTimer)
    }
  }, [initialCache, load])

  useEffect(() => {
    if (data?.station_name) document.title = `${data.station_name} · 分组调用监控`
  }, [data?.station_name])

  const summary = data?.summary
  return <main className="min-h-screen bg-background text-foreground">
    <div className="relative mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      {refreshing && data ? <div className="pointer-events-none absolute inset-x-0 top-3 z-50 flex justify-center"><span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><RefreshCw className="size-3.5 animate-spin text-brand" />正在刷新分组监控</span></div> : null}
      <header className="flex min-h-16 flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
        <div className="min-w-0"><h1 className="flex items-center gap-2 text-xl font-bold sm:text-2xl"><Activity className="size-5 shrink-0 text-brand sm:size-6" /><span className="truncate">{data?.station_name || "分组调用监控"}</span></h1><p className="mt-1.5 text-sm text-muted-foreground">公开分组的近期调用结果与响应耗时</p></div>
        <div className="flex items-center gap-3"><span className="whitespace-nowrap font-mono text-xs tabular-nums text-muted-foreground">{refreshing ? "刷新中" : `${countdown} 秒后刷新`}</span><span className="hidden text-xs text-muted-foreground sm:inline">更新于 {formatTime(data?.updated_at)}</span><Button type="button" variant="outline" size="icon" className="size-11 sm:size-9" aria-label="刷新监控数据" disabled={refreshing} onClick={() => void load(true)}><RefreshCw className={cn("size-4", refreshing && "animate-spin")} /></Button></div>
      </header>

      {summary ? <section className="grid grid-cols-2 border-b border-border sm:grid-cols-4" aria-label="分组状态统计"><div className="py-4 sm:px-4 sm:first:pl-0"><p className="text-[11px] text-muted-foreground">监控分组</p><p className="mt-1 font-mono text-xl font-bold tabular-nums">{summary.total}</p></div><div className="py-4 sm:px-4"><p className="text-[11px] text-muted-foreground">可用</p><p className="mt-1 font-mono text-xl font-bold tabular-nums text-success">{summary.available}</p></div><div className="py-4 sm:px-4"><p className="text-[11px] text-muted-foreground">不稳定</p><p className="mt-1 font-mono text-xl font-bold tabular-nums text-warning">{summary.degraded}</p></div><div className="py-4 sm:px-4"><p className="text-[11px] text-muted-foreground">不可用</p><p className="mt-1 font-mono text-xl font-bold tabular-nums text-danger">{summary.unavailable}</p></div></section> : null}

      <div className="mt-4 flex items-center gap-3 rounded-lg border border-brand/20 bg-brand/5 px-3.5 py-2.5 text-xs text-foreground" role="note"><span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-brand/10 text-brand"><Info className="size-4" aria-hidden="true" /></span><p className="min-w-0 leading-5"><strong className="font-semibold text-brand">监控说明</strong><span className="mx-2 text-muted-foreground/60" aria-hidden="true">·</span><span className="font-semibold text-warning">仅供趋势参考；状态基于最近调用，不代表当前实际可用性。</span></p></div>

      <section className="mt-5" aria-label="监控分组列表">
        {error ? <Alert variant="destructive" className="mb-4"><TriangleAlert /><AlertTitle>数据更新失败</AlertTitle><AlertDescription>{error}</AlertDescription></Alert> : null}
        {loading && !data ? <LoadingCards /> : data ? data.groups.length ? <div className="grid min-h-[420px] gap-3 min-[1150px]:grid-cols-2">{data.groups.map((group) => <MonitorRow key={group.external_id} group={group} />)}</div> : <div className="flex min-h-[420px] items-center justify-center border border-border px-6 text-center text-sm text-muted-foreground">当前没有开启监控的分组</div> : <div className="flex min-h-[420px] items-center justify-center border border-border px-6 text-sm text-muted-foreground">暂无监控数据</div>}
      </section>
      <span className="sr-only" aria-live="polite">{refreshing ? "正在刷新监控数据" : error ? "监控数据刷新失败" : "监控数据已更新"}</span>
    </div>
  </main>
}
