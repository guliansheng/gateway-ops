"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { Activity, AlertCircle, ArrowUpRight, CheckCircle2, ChevronDown, ChevronRight, CircleHelp, CircleX, Clock3, ExternalLink, Info, RefreshCw, TriangleAlert } from "lucide-react"
import openAIIcon from "@lobehub/icons-static-svg/icons/openai.svg"
import anthropicIcon from "@lobehub/icons-static-svg/icons/anthropic.svg"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import type { PublicServiceStatus, PublicServiceStatusComponent, PublicServiceStatusDay, PublicServiceStatusGroup, PublicServiceStatusIncident, PublicServiceStatusView } from "@/lib/api-types"
import { cn } from "@/lib/utils"

const refreshIntervalSeconds = 60
const refreshIntervalMS = refreshIntervalSeconds * 1_000
const browserCacheKey = "gatewayops:public-service-status:v1"

type StatusCache = { cached_at: number; data: PublicServiceStatusView }

function readCache(): StatusCache | null {
  if (typeof window === "undefined") return null
  try {
    const raw = window.localStorage.getItem(browserCacheKey)
    if (!raw) return null
    const cached = JSON.parse(raw) as StatusCache
    if (!cached.data || !Array.isArray(cached.data.services) || !Number.isFinite(cached.cached_at)) return null
    return cached
  } catch {
    return null
  }
}

function writeCache(data: PublicServiceStatusView) {
  try {
    window.localStorage.setItem(browserCacheKey, JSON.stringify({ cached_at: Date.now(), data } satisfies StatusCache))
  } catch {
    // 浏览器禁用 localStorage 时仍可正常在线读取。
  }
}

const indicatorMeta: Record<string, { label: string; className: string; pillClassName: string; icon: typeof CheckCircle2 }> = {
  none: { label: "全部正常", className: "text-success", pillClassName: "border-success/25 bg-success/10", icon: CheckCircle2 },
  minor: { label: "轻微故障", className: "text-warning", pillClassName: "border-warning/25 bg-warning/10", icon: TriangleAlert },
  major: { label: "部分中断", className: "text-orange-600 dark:text-orange-400", pillClassName: "border-orange-500/25 bg-orange-500/10", icon: TriangleAlert },
  critical: { label: "严重故障", className: "text-danger", pillClassName: "border-danger/25 bg-danger/10", icon: CircleX },
  unknown: { label: "状态未知", className: "text-muted-foreground", pillClassName: "border-border bg-muted/60", icon: CircleHelp },
}

const componentMeta: Record<string, { label: string; color: string }> = {
  operational: { label: "正常", color: "#10b981" },
  degraded_performance: { label: "性能下降", color: "#f59e0b" },
  partial_outage: { label: "部分中断", color: "#f97316" },
  major_outage: { label: "严重中断", color: "#ef4444" },
  full_outage: { label: "完全中断", color: "#dc2626" },
  under_maintenance: { label: "维护中", color: "#8b5cf6" },
  unknown: { label: "未知", color: "#94a3b8" },
}

const incidentStatusLabels: Record<string, string> = {
  investigating: "调查中",
  identified: "已定位",
  monitoring: "监控中",
  resolved: "已恢复",
  scheduled: "计划维护",
  in_progress: "进行中",
  verified: "已验证",
  completed: "已完成",
}

const impactLabels: Record<string, string> = { none: "无影响", minor: "轻微", major: "重大", critical: "严重" }

function formatTime(value: string | null | undefined) {
  if (!value) return "暂无"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "暂无"
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(date)
}

function formatDay(value: string) {
  const [year, month, day] = value.split("-")
  return year && month && day ? `${year}/${month}/${day}` : value
}

function statusInfo(status: string | undefined) {
  return componentMeta[status || ""] || { label: status || "未知", color: "#94a3b8" }
}

function serviceMeta(service: PublicServiceStatus) {
  const indicator = service.status?.indicator || "unknown"
  return indicatorMeta[indicator] || indicatorMeta.unknown
}

function fallbackGroups(service: PublicServiceStatus): PublicServiceStatusGroup[] {
  if (service.groups?.length) return service.groups
  return (service.components || []).map((component) => ({ id: component.id, name: component.name, description: component.description, status: component.status, uptime: 100, history: component.history || [], components: [component] }))
}

function ServiceLogo({ id }: { id: string }) {
  const src = id === "openai" ? openAIIcon : id === "claude" ? anthropicIcon : null
  return src ? <img src={src} alt="" aria-hidden="true" className={cn("size-6 object-contain", id === "openai" && "dark:invert")} /> : <AlertCircle className="size-5 text-brand" aria-hidden="true" />
}

function StatusBadge({ service }: { service: PublicServiceStatus }) {
  const meta = serviceMeta(service)
  const Icon = meta.icon
  return <span className={cn("inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs font-semibold", meta.className, meta.pillClassName)}><Icon className="size-3.5" />{meta.label}</span>
}

function HistoryBar({ history, label }: { history: PublicServiceStatusDay[]; label: string }) {
  if (!history.length) return <div className="flex h-8 items-center justify-center rounded-sm bg-muted text-[11px] text-muted-foreground">暂无历史数据</div>
  return <div className="min-w-0 flex-1"><div className="flex h-8 min-w-0 items-stretch gap-px overflow-hidden rounded-sm bg-muted/60" role="list" aria-label={`${label}过去 ${history.length} 天状态`}>{history.map((day) => { const meta = statusInfo(day.status); const incidents = day.incidents || []; return <Tooltip key={`${label}-${day.date}`} delayDuration={100}><TooltipTrigger asChild><span role="listitem" tabIndex={0} className="min-w-0 flex-1 cursor-default rounded-[2px] opacity-90 outline-none transition-opacity hover:opacity-100 focus-visible:ring-2 focus-visible:ring-ring" style={{ backgroundColor: meta.color }} aria-label={`${formatDay(day.date)}：${meta.label}`} /></TooltipTrigger><TooltipContent side="top" className="max-w-[min(20rem,calc(100vw-2rem))]"><p className="font-semibold">{formatDay(day.date)}</p><p className="text-xs text-background/70">状态：{meta.label}</p>{incidents.length ? <div className="mt-1 space-y-0.5 border-t border-background/20 pt-1 text-xs"><p className="font-medium">关联事件</p>{incidents.map((incident) => <p key={incident.id} className="truncate">{incident.name}</p>)}</div> : <p className="mt-1 text-xs text-background/70">当天没有公开事件</p>}</TooltipContent></Tooltip> })}</div><div className="mt-1 flex justify-between text-[10px] text-muted-foreground"><span>{formatDay(history[0].date)}</span><span>今天</span></div></div>
}

function IncidentCard({ incident, past = false }: { incident: PublicServiceStatusIncident; past?: boolean }) {
  const updates = incident.incident_updates ?? []
  const latest = [...updates].sort((left, right) => new Date(right.updated_at || right.created_at || 0).getTime() - new Date(left.updated_at || left.created_at || 0).getTime())[0]
  const statusLabel = incidentStatusLabels[incident.status] || incident.status || "状态更新"
  const componentNames = incident.components?.map((component) => component.name).filter(Boolean) || []
  return <article className={cn("rounded-md border p-4", past ? "border-border/70 bg-muted/20" : "border-warning/30 bg-warning/5")}><div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><h4 className="font-semibold leading-5 text-foreground">{incident.name}</h4><p className="mt-1 text-xs text-muted-foreground">{incident.created_at ? `开始于 ${formatTime(incident.created_at)}` : "时间未知"}{incident.resolved_at ? ` · 恢复于 ${formatTime(incident.resolved_at)}` : ""}</p></div><div className="flex shrink-0 flex-wrap items-center gap-1.5"><span className={cn("rounded border px-2 py-1 text-[11px] font-semibold", past ? "border-success/25 bg-success/10 text-success" : "border-warning/25 bg-warning/10 text-warning")}>{statusLabel}</span><span className="rounded border border-border bg-muted px-2 py-1 text-[11px] font-semibold text-foreground/70">影响：{impactLabels[incident.impact] || incident.impact || "未知"}</span></div></div>{componentNames.length ? <p className="mt-2 text-xs text-muted-foreground">影响组件：{componentNames.join("、")}</p> : null}{latest?.body ? <p className="mt-3 whitespace-pre-line border-l-2 border-warning/60 pl-3 text-sm leading-6 text-foreground/80">{latest.body}</p> : null}<div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border/70 pt-2.5 text-[11px] text-muted-foreground"><span>{updates.length ? `${updates.length} 条进展更新` : "暂无进展详情"}</span><span>{latest ? `最近更新 ${formatTime(latest.updated_at || latest.created_at)}` : ""}</span>{incident.shortlink ? <a href={incident.shortlink} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-brand hover:underline">官方详情<ExternalLink className="size-3" /></a> : null}</div></article>
}

function CurrentIncidents({ service }: { service: PublicServiceStatus }) {
  const incidents = service.incidents ?? []
  const maintenances = service.scheduled_maintenances ?? []
  if (!incidents.length && !maintenances.length) return <div className="flex items-center gap-2 rounded-md border border-success/25 bg-success/5 px-4 py-3 text-sm text-success"><CheckCircle2 className="size-4 shrink-0" />当前没有公开报警或计划维护</div>
  return <section aria-labelledby={`${service.id}-current-events`} className="space-y-3"><div className="flex flex-wrap items-center justify-between gap-2"><h3 id={`${service.id}-current-events`} className="flex items-center gap-2 text-base font-bold text-foreground"><TriangleAlert className="size-4 text-warning" />当前报警</h3><span className="text-xs text-muted-foreground">{incidents.length ? `${incidents.length} 个进行中的事件` : "暂无进行中的事件"}</span></div>{incidents.length ? <div className="space-y-3">{incidents.map((incident) => <IncidentCard key={incident.id} incident={incident} />)}</div> : <div className="rounded-md border border-dashed border-border px-4 py-3 text-sm text-muted-foreground">暂无进行中的事件</div>}{maintenances.length ? <div className="space-y-3 pt-2"><div className="flex items-center gap-2 text-sm font-semibold text-foreground"><Clock3 className="size-4 text-brand" />计划维护</div>{maintenances.map((incident) => <IncidentCard key={incident.id} incident={incident} />)}</div> : null}</section>
}

function HistoryGroupRow({ group, open, canExpand, onToggle }: { group: PublicServiceStatusGroup; open: boolean; canExpand: boolean; onToggle: () => void }) {
  const meta = statusInfo(group.status)
  return <div className="border-b border-border/70 py-4 last:border-b-0"><div className="flex min-w-0 items-start gap-3">{canExpand ? <button type="button" onClick={onToggle} className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`${open ? "收起" : "展开"}${group.name}子组件`} aria-expanded={open}>{open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}</button> : <span className="mt-2 size-2 shrink-0 rounded-full" style={{ backgroundColor: meta.color }} />}<div className="min-w-0 flex-1"><div className="mb-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-1"><div className="min-w-0"><span className="font-semibold text-foreground">{group.name}</span>{group.description ? <span className="ml-2 text-xs text-muted-foreground">{group.description}</span> : null}</div><span className="shrink-0 text-xs text-muted-foreground"><span className="font-semibold text-foreground">{group.uptime.toFixed(2)}%</span> 可用</span></div><HistoryBar history={group.history || []} label={group.name} />{canExpand && open ? <div className="mt-3 space-y-3 border-l-2 border-border pl-3 sm:pl-4">{group.components.map((component) => <ComponentHistoryRow key={component.id} component={component} />)}</div> : null}</div></div></div>
}

function ComponentHistoryRow({ component }: { component: PublicServiceStatusComponent }) {
  const meta = statusInfo(component.status)
  return <div className="min-w-0"><div className="mb-1.5 flex flex-wrap items-center justify-between gap-2"><span className="min-w-0 truncate text-sm text-foreground" title={component.name}>{component.name}</span><span className="inline-flex shrink-0 items-center gap-1.5 text-[11px] font-medium text-muted-foreground"><span className="size-1.5 rounded-full" style={{ backgroundColor: meta.color }} />{meta.label}</span></div><HistoryBar history={component.history || []} label={component.name} /></div>
}

function ServicePanel({ service, days }: { service: PublicServiceStatus; days: number }) {
  const groups = fallbackGroups(service)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const componentCount = service.components?.length || groups.reduce((total, group) => total + group.components.length, 0)
  return <div className="space-y-6">{service.error ? <Alert variant="destructive"><TriangleAlert /><AlertTitle>{service.name} 状态读取失败</AlertTitle><AlertDescription>{service.error}。当前没有可用的官方摘要数据，请稍后重试或打开官方状态页。</AlertDescription></Alert> : null}<section className="rounded-md border border-border/80 bg-card p-4 shadow-sm sm:p-5"><div className="flex flex-wrap items-start justify-between gap-4"><div className="flex min-w-0 flex-1 items-center gap-3"><span className="flex size-11 shrink-0 items-center justify-center rounded-lg bg-muted"><ServiceLogo id={service.id} /></span><div className="min-w-0"><h2 className="truncate text-lg font-bold text-foreground">{service.name}</h2><p className="mt-1 truncate text-xs text-muted-foreground">来源：{service.url.replace(/^https?:\/\//, "").replace(/\/$/, "")}</p></div></div><div className="flex w-full flex-wrap items-center gap-2 sm:w-auto sm:flex-nowrap"><StatusBadge service={service} /><Button asChild variant="outline" size="sm" className="h-9 gap-1.5"><a href={service.url} target="_blank" rel="noopener noreferrer">状态页<ExternalLink className="size-3.5" /></a></Button></div></div><div className="mt-5 grid grid-cols-2 gap-4 border-t border-border pt-4 sm:grid-cols-4"><div><p className="text-[11px] text-muted-foreground">状态说明</p><p className="mt-1 text-sm font-semibold text-foreground">{service.status?.description || "暂无说明"}</p></div><div><p className="text-[11px] text-muted-foreground">组件总数</p><p className="mt-1 font-mono text-xl font-bold tabular-nums">{componentCount}</p></div><div><p className="text-[11px] text-muted-foreground">当前事件</p><p className={cn("mt-1 font-mono text-xl font-bold tabular-nums", service.incidents?.length ? "text-warning" : "text-success")}>{service.incidents?.length || 0}</p></div><div><p className="text-[11px] text-muted-foreground">状态页更新</p><p className="mt-1 text-sm font-semibold text-foreground">{formatTime(service.updated_at)}</p></div></div></section><CurrentIncidents service={service} /><section aria-labelledby={`${service.id}-history`} className="rounded-md border border-border/80 bg-card px-4 shadow-sm sm:px-5"><div className="flex flex-wrap items-end justify-between gap-2 border-b border-border py-4"><div><h3 id={`${service.id}-history`} className="text-base font-bold text-foreground">状态历史</h3><p className="mt-1 text-xs text-muted-foreground">按官网分类展示最近 {days} 天，每格代表一天；悬停查看日期、状态和关联事件</p></div><div className="flex items-center gap-3 text-[11px] text-muted-foreground"><span className="inline-flex items-center gap-1"><span className="size-2 rounded-sm bg-[#10b981]" />正常</span><span className="inline-flex items-center gap-1"><span className="size-2 rounded-sm bg-[#f59e0b]" />性能下降</span><span className="inline-flex items-center gap-1"><span className="size-2 rounded-sm bg-[#ef4444]" />中断</span></div></div><div>{groups.map((group) => <HistoryGroupRow key={group.id} group={group} open={Boolean(expanded[group.id])} canExpand={service.id === "openai" && group.components.length > 0} onToggle={() => setExpanded((current) => ({ ...current, [group.id]: !current[group.id] }))} />)}</div></section><section aria-labelledby={`${service.id}-past-incidents`}><div className="mb-3 flex flex-wrap items-center justify-between gap-2"><div><h3 id={`${service.id}-past-incidents`} className="text-base font-bold text-foreground">历史事件</h3><p className="mt-1 text-xs text-muted-foreground">仅保留最近两个月已结束的公开事件</p></div><a href={service.history_url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs font-medium text-brand hover:underline">查看官方历史<ArrowUpRight className="size-3.5" /></a></div>{service.past_incidents?.length ? <div className="space-y-3">{service.past_incidents.map((incident) => <IncidentCard key={incident.id} incident={incident} past />)}</div> : <div className="flex min-h-24 items-center justify-center rounded-md border border-dashed border-border bg-card px-4 text-sm text-muted-foreground"><Info className="mr-2 size-4" />最近两个月没有已结束事件</div>}</section></div>
}

function ServiceTabs({ services, selectedID, onSelect }: { services: PublicServiceStatus[]; selectedID: string; onSelect: (id: string) => void }) {
  return <div className="flex w-full max-w-full overflow-x-auto rounded-lg border border-border bg-muted/50 p-1 sm:w-auto"><div className="flex min-w-full gap-1 sm:min-w-0">{services.map((service) => <button key={service.id} type="button" onClick={() => onSelect(service.id)} className={cn("flex min-h-11 min-w-[min(46vw,12rem)] cursor-pointer items-center justify-center gap-2 rounded-md px-4 text-sm font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:min-w-40", selectedID === service.id ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:bg-background/70 hover:text-foreground")} aria-pressed={selectedID === service.id}><ServiceLogo id={service.id} /><span>{service.name}</span>{service.error ? <TriangleAlert className="size-3.5 text-warning" aria-label="读取失败" /> : null}</button>)}</div></div>
}

function LoadingPanel() {
  return <div className="space-y-4"><div className="rounded-md border border-border p-5"><div className="flex items-center justify-between"><Skeleton className="h-8 w-48" /><Skeleton className="h-8 w-24" /></div><Skeleton className="mt-6 h-24 w-full" /><Skeleton className="mt-5 h-10 w-full" /></div><div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">{Array.from({ length: 6 }).map((_, index) => <Skeleton key={index} className="h-12 w-full" />)}</div></div>
}

export default function PublicServiceStatusPage() {
  const initialCache = useMemo(readCache, [])
  const [data, setData] = useState<PublicServiceStatusView | null>(initialCache?.data ?? null)
  const [selectedID, setSelectedID] = useState(initialCache?.data.services[0]?.id ?? "openai")
  const [loading, setLoading] = useState(!initialCache)
  const [refreshing, setRefreshing] = useState(false)
  const [countdown, setCountdown] = useState(refreshIntervalSeconds)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true); else setLoading(true)
    try {
      const next = await apiFetch<PublicServiceStatusView>("/public/service-status", { skipAuthErrorHandler: true })
      setData(next)
      writeCache(next)
      setSelectedID((current) => next.services.some((service) => service.id === current) ? current : next.services[0]?.id ?? "openai")
      setError(null)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "服务状态读取失败")
    } finally {
      setLoading(false)
      setRefreshing(false)
      setCountdown(refreshIntervalSeconds)
    }
  }, [])

  useEffect(() => {
    void load(Boolean(initialCache))
    const timer = window.setInterval(() => void load(true), refreshIntervalMS)
    const countdownTimer = window.setInterval(() => setCountdown((current) => current <= 1 ? refreshIntervalSeconds : current - 1), 1_000)
    return () => { window.clearInterval(timer); window.clearInterval(countdownTimer) }
  }, [initialCache, load])

  useEffect(() => { document.title = "服务状态 · GatewayOps" }, [])

  const selectedService = data?.services.find((service) => service.id === selectedID) ?? data?.services[0]
  return <main className="min-h-screen bg-background text-foreground"><div className="relative mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">{refreshing && data ? <div className="pointer-events-none absolute inset-x-0 top-3 z-10 flex justify-center"><span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><RefreshCw className="size-3.5 animate-spin text-brand" />正在刷新服务状态</span></div> : null}<header className="flex min-h-16 flex-wrap items-start justify-between gap-4 border-b border-border pb-5"><div className="min-w-0"><h1 className="flex items-center gap-2 text-xl font-bold sm:text-2xl"><Activity className="size-5 shrink-0 text-brand sm:size-6" /><span>AI 服务状态</span></h1><p className="mt-1.5 text-sm text-muted-foreground">公开状态页实时摘要，每 {refreshIntervalSeconds} 秒自动刷新</p></div><div className="flex items-center gap-3"><span className="hidden whitespace-nowrap text-xs text-muted-foreground sm:inline">{refreshing ? "刷新中" : `${countdown} 秒后刷新`} · 更新于 {formatTime(data?.updated_at)}</span><Button type="button" variant="outline" size="icon" className="size-11 sm:size-9" aria-label="刷新服务状态" disabled={refreshing} onClick={() => void load(true)}><RefreshCw className={cn("size-4", refreshing && "animate-spin")} /></Button></div></header><div className="mt-5 flex flex-wrap items-center justify-between gap-3"><ServiceTabs services={data?.services ?? []} selectedID={selectedService?.id ?? selectedID} onSelect={setSelectedID} /><div className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><Info className="size-3.5" />数据来自 OpenAI、Claude 官方状态页</div></div>{error ? <Alert variant="destructive" className="mt-4"><TriangleAlert /><AlertTitle>状态更新失败</AlertTitle><AlertDescription>{error}{data ? "，当前展示最近一次成功读取的数据。" : "，请稍后重试。"}</AlertDescription></Alert> : null}<section className="mt-5" aria-label="服务状态详情">{loading && !data ? <LoadingPanel /> : selectedService ? <ServicePanel service={selectedService} days={data?.days || 60} /> : <div className="flex min-h-64 items-center justify-center border border-dashed border-border text-sm text-muted-foreground">暂无服务状态数据</div>}</section><div className="mt-6 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-4 text-xs text-muted-foreground"><span>状态信息由第三方官方状态页提供，仅供运维参考。</span>{selectedService?.url ? <a className="inline-flex items-center gap-1 font-medium text-brand hover:underline" href={selectedService.url} target="_blank" rel="noopener noreferrer">打开 {selectedService.name} 官方状态页<ArrowUpRight className="size-3.5" /></a> : null}</div><span className="sr-only" aria-live="polite">{refreshing ? "正在刷新服务状态" : error ? "服务状态刷新失败" : "服务状态已更新"}</span></div></main>
}
