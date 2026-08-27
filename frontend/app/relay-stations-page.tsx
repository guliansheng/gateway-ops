"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { Activity, Archive, ArrowDown, ArrowUp, ArrowUpDown, Box, Check, ChevronDown, CircleAlert, CircleDollarSign, CircleHelp, Clock3, Cog, ExternalLink, Gauge, GripVertical, History, Layers3, ListChecks, PencilLine, Play, Plus, Power, PowerOff, RefreshCw, RotateCcw, Save, Search, Server, ShieldAlert, ShieldCheck, SlidersHorizontal, Trash2, TrendingDown, Users, X } from "lucide-react"
import { toast } from "sonner"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { apiFetch } from "@/lib/api"
import type { RelayAccountBatchActionResult, RelayUsageRange, RelayAccountTestResult, RelayAccountView, RelayGroupOption, RelayGroupTestResult, RelayGroupView, RelayOverview, RelayRecentUsage, RelayStation } from "@/lib/api-types"
import { useRelayOverview, useRelayRecentUsage, useRelayStations, useRelayUsage, useRelayUserBalanceHistory, useSyncSettings } from "@/lib/queries"
import { RelayAdjustmentLog } from "@/components/monitor/relay-adjustment-log"
import { UserManagement } from "@/components/relay/user-management"
import { PlatformBadge } from "@/components/relay/platform-badge"
import { cn } from "@/lib/utils"

type StationForm = { name: string; base_url: string; api_key: string }
type RiskFilter = "all" | "adjustable" | "downgradable" | RelayAccountView["risk_state"]
type BatchMode = "channel_group" | "manual" | "groups" | "concurrency" | "priority" | "retry_count" | "model_type"
type SortDirection = "asc" | "desc"
type GroupSortKey = "name" | "rate"
type AccountSortKey = "latency" | "priority" | "cost" | "usage"
type SortState<K extends string> = { key: K; direction: SortDirection } | null
type RelayDetailModule = "usage" | "users" | "groups" | "accounts"

const emptyForm: StationForm = { name: "", base_url: "", api_key: "" }
const rateIntervals = [5, 10, 15, 30, 60, 180, 360, 720, 1440]
const snapshotIntervals = [5, 10, 30, 60, 120, 180, 300, 600, 900, 1800, 3600, 10800]
const usageRanges: { value: RelayUsageRange; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "today", label: "今天" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
]
const compactNumber = new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 2 })
const modelTypeHelp = "模型类型用于标记分组适用的模型范围。账号绑定模型类型后，在平台、账号类型等条件满足时，只会在设置了相同模型类型的分组之间自动调组；未绑定模型类型的账号不参与自动调组。不同分组可以设置相同模型类型，设置相同后会进入同一自动调组范围。"

function isMissingGroupAPIKeyError(error: unknown) {
  if (!error || typeof error !== "object") return false
  const candidate = error as { status?: unknown; body?: unknown }
  if (candidate.status !== 422 || !candidate.body || typeof candidate.body !== "object") return false
  return (candidate.body as { code?: unknown }).code === "group_admin_api_key_missing"
}

function ModelTypeHelp() {
  return <Tooltip delayDuration={200}><TooltipTrigger asChild><button type="button" className="cursor-help rounded text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label="查看模型类型说明"><CircleHelp className="size-3.5" /></button></TooltipTrigger><TooltipContent side="top" className="max-w-80 text-xs leading-5">{modelTypeHelp}</TooltipContent></Tooltip>
}

function usePersistedOpen(storageKey: string, defaultOpen = true) {
  const [open, setOpen] = useState(() => {
    try {
      const stored = window.localStorage.getItem(storageKey)
      return stored === "true" || stored === "false" ? stored === "true" : defaultOpen
    } catch {
      return defaultOpen
    }
  })
  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, String(open))
    } catch {
      // The disclosure still works for the current session without persistence.
    }
  }, [open, storageKey])
  return [open, setOpen] as const
}

function usePersistedDetailModule(storageKey: string, defaultModule: RelayDetailModule = "accounts") {
  const [module, setModule] = useState<RelayDetailModule>(() => {
    try {
      const stored = window.localStorage.getItem(storageKey)
      if (stored === "usage" || stored === "users" || stored === "groups" || stored === "accounts") return stored
    } catch {
      // Keep the default module when localStorage is unavailable.
    }
    return defaultModule
  })
  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, module)
    } catch {
      // The tab still works for the current session without persistence.
    }
  }, [module, storageKey])
  return [module, setModule] as const
}

const rateSyncHelp = "定期读取 API Key 的上游成本倍率，更新账号成本，并用于成本统计、利润判断和自动调组。"
const snapshotSyncHelp = "定期刷新中转站的账号、分组和关联关系快照，仅同步配置与状态，不执行倍率探测。"

function SyncPlanHelp({ label, help }: { label: string; help: string }) {
  return <Tooltip delayDuration={200}><TooltipTrigger asChild><button type="button" className="shrink-0 rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`查看${label}说明`}><CircleHelp className="size-3.5" /></button></TooltipTrigger><TooltipContent side="top" className="max-w-80 text-xs leading-5">{help}</TooltipContent></Tooltip>
}

function SortButton({ label, active, direction, onClick, className, title }: { label: string; active: boolean; direction?: SortDirection; onClick: () => void; className?: string; title?: string }) {
  const Icon = !active ? ArrowUpDown : direction === "asc" ? ArrowUp : ArrowDown
  return <button type="button" onClick={onClick} title={title} className={cn("inline-flex cursor-pointer items-center gap-1 rounded-sm font-medium transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring", className)} aria-label={`${label}排序${active ? `，当前${direction === "asc" ? "升序" : "降序"}` : ""}`}><span>{label}</span><Icon className="size-3" /></button>
}

function latencySmoothnessScore(account: RelayAccountView) {
  const samples = account.latency_samples ?? []
  if (samples.length === 0) return null
  return samples.reduce((sum, sample) => sum + sample.first_token_ms / 10_000 + sample.duration_ms / 60_000, 0) / samples.length
}

function publicFirst(groups: RelayGroupOption[]) {
  return [...groups].sort(
    (a, b) => Number(Boolean(a.is_exclusive)) - Number(Boolean(b.is_exclusive)) || a.name.localeCompare(b.name, "zh-CN"),
  )
}

function accountDowngradeGroups(account: RelayAccountView) {
  if (account.downgrade_groups?.length) return account.downgrade_groups
  return account.downgrade_group ? [account.downgrade_group] : []
}

const stateMeta: Record<RelayAccountView["risk_state"], { label: string; tone: string }> = {
  risk: { label: "亏损风险", tone: "bg-danger/10 text-danger" },
  no_safe_candidate: { label: "无安全候选", tone: "bg-yellow-400/15 text-yellow-700 dark:text-yellow-300" },
  no_profit: { label: "无盈利候选", tone: "bg-yellow-400/15 text-yellow-700 dark:text-yellow-300" },
  cost_unknown: { label: "成本未知", tone: "bg-warning/10 text-warning" },
  protected: { label: "分组安全", tone: "bg-success/10 text-success" },
  unassigned: { label: "未分配销售组", tone: "bg-muted text-muted-foreground" },
  inactive: { label: "账号未启用", tone: "bg-muted text-muted-foreground" },
}

function multiplier(value: number | null | undefined) {
  return value == null ? "-" : value.toFixed(3)
}

function profitPercent(margin: number | null | undefined, cost: number | null | undefined) {
  if (margin == null || cost == null || !Number.isFinite(margin) || !Number.isFinite(cost) || cost <= 0) return "-"
  return `${((margin / cost) * 100).toFixed(1)}%`
}

function tokenAmount(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "-"
  return compactNumber.format(value)
}

function chargeAmount(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "-"
  return `$${value.toLocaleString("en-US", { minimumFractionDigits: 6, maximumFractionDigits: 6 })}`
}

function capacityTone(account: RelayAccountView) {
  if (account.concurrency <= 0 || account.current_concurrency <= 0) return "border-border bg-muted text-muted-foreground"
  if (account.current_concurrency >= account.concurrency) return "border-danger/30 bg-danger/10 text-danger"
  return "border-warning/30 bg-warning/10 text-warning"
}

function intervalLabel(value: number) {
  if (value < 60) return `${value} 分钟`
  if (value === 1440) return "每天"
  return `${value / 60} 小时`
}

function snapshotIntervalLabel(value: number) {
  if (value < 60) return `${value} 秒`
  if (value < 3600) return `${value / 60} 分钟`
  return `${value / 3600} 小时`
}

function GroupNames({ groups }: { groups: RelayGroupOption[] }) {
  if (groups.length === 0) return <>未关联</>
  return <>{groups.map((group, index) => <span key={group.external_id}>{index ? " · " : ""}{group.name} <strong className="font-mono text-sm font-semibold tabular-nums">{multiplier(group.rate_multiplier)}</strong></span>)}</>
}

function sourceLabel(account: RelayAccountView, overview: RelayOverview) {
  if (!account.cost_source) return "未获取"
  if (account.cost_source === "manual") return `手工倍率 ${multiplier(account.cost_multiplier)}`
  if (account.cost_source === "channel_group") {
    const channel = overview.monitor_channels.find((item) => item.id === account.cost_override_channel_id)
    return `渠道 ${channel?.name ?? `#${account.cost_override_channel_id ?? "-"}`} / ${account.cost_override_group ?? "-"}`
  }
  if (account.cost_source === "auto_link") {
    const channel = overview.monitor_channels.find((item) => item.id === account.cost_override_channel_id)
    return `自动关联 ${channel?.name ?? `#${account.cost_override_channel_id ?? "-"}`} / ${account.cost_override_group ?? "-"}`
  }
  return "实时探测"
}

type LatencyTone = "good" | "warn" | "slow" | "critical"

function latencyTone(value: number, metric: "first" | "duration"): LatencyTone {
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

const latencyText: Record<LatencyTone, string> = {
  good: "正常",
  warn: "需关注",
  slow: "较慢",
  critical: "严重延迟",
}

const latencyTextClass: Record<LatencyTone, string> = {
  good: "text-emerald-600 dark:text-emerald-400",
  warn: "text-amber-600 dark:text-amber-400",
  slow: "text-orange-600 dark:text-orange-400",
  critical: "text-red-600 dark:text-red-400",
}

const latencyBarClass: Record<LatencyTone, string> = {
  good: "bg-emerald-500",
  warn: "bg-amber-400",
  slow: "bg-orange-500",
  critical: "bg-red-500",
}

function formatLatency(value: number) {
  if (!Number.isFinite(value) || value < 0) return "-"
  const milliseconds = Math.round(value)
  if (milliseconds < 1000) return `${milliseconds}ms`
  const totalSeconds = milliseconds / 1000
  if (totalSeconds < 60) return `${totalSeconds.toFixed(totalSeconds < 10 ? 1 : 0)}秒`
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  const secondsText = seconds.toFixed(seconds < 10 && seconds % 1 !== 0 ? 1 : 0).padStart(2, "0")
  if (hours > 0) return `${hours}时 ${String(minutes).padStart(2, "0")}分 ${secondsText}秒`
  return `${minutes}分 ${secondsText}秒`
}

function requestTypeLabel(value: string) {
  const normalized = value.trim().toLowerCase().replaceAll("_", "-")
  const labels: Record<string, string> = {
    stream: "流式",
    streaming: "流式",
    "non-stream": "非流式",
    nonstream: "非流式",
    sync: "同步",
    async: "异步",
    image: "图像",
    video: "视频",
  }
  return labels[normalized] ?? (normalized ? normalized.replaceAll("-", " ") : "-")
}

function requestTypeClass(value: string) {
  const normalized = value.trim().toLowerCase().replaceAll("_", "-")
  if (normalized === "stream" || normalized === "streaming") return "border-emerald-300 bg-emerald-100 text-emerald-800 dark:border-emerald-500/40 dark:bg-emerald-500/20 dark:text-emerald-300"
  if (normalized === "sync") return "border-blue-300 bg-blue-100 text-blue-800 dark:border-blue-500/40 dark:bg-blue-500/20 dark:text-blue-300"
  if (normalized === "non-stream" || normalized === "nonstream") return "border-amber-300 bg-amber-100 text-amber-800 dark:border-amber-500/40 dark:bg-amber-500/20 dark:text-amber-300"
  if (normalized === "async") return "border-violet-300 bg-violet-100 text-violet-800 dark:border-violet-500/40 dark:bg-violet-500/20 dark:text-violet-300"
  if (normalized === "image") return "border-pink-300 bg-pink-100 text-pink-800 dark:border-pink-500/40 dark:bg-pink-500/20 dark:text-pink-300"
  if (normalized === "video") return "border-orange-300 bg-orange-100 text-orange-800 dark:border-orange-500/40 dark:bg-orange-500/20 dark:text-orange-300"
  return "border-slate-300 bg-slate-100 text-slate-700 dark:border-slate-500/40 dark:bg-slate-500/20 dark:text-slate-300"
}

function money(value: number | null | undefined) {
  return `$${(value ?? 0).toFixed(6)}`
}

type AccountTestOutputView = {
  model: string
  content: string
  completed: boolean | null
  fallback: string
}

function parseAccountTestOutput(output: string): AccountTestOutputView {
  let model = ""
  let content = ""
  let completed: boolean | null = null
  let parsedEvent = false

  for (const line of output.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed.startsWith("data:")) continue
    const payload = trimmed.slice(5).trim()
    if (!payload || payload === "[DONE]") continue
    try {
      const event = JSON.parse(payload) as Record<string, unknown>
      parsedEvent = true
      if (typeof event.model === "string") model = event.model
      if (event.type === "content" && typeof event.text === "string") content += event.text
      if (event.type === "test_complete" && typeof event.success === "boolean") completed = event.success
    } catch {
      // Unknown SSE frames are retained by the formatted fallback below.
    }
  }

  let fallback = output.trim()
  if (!parsedEvent && fallback) {
    try {
      fallback = JSON.stringify(JSON.parse(fallback), null, 2)
    } catch {
      // Plain-text upstream responses are already readable as-is.
    }
  }
  return { model, content, completed, fallback }
}

function LatencyBars({ samples }: { samples: RelayAccountView["latency_samples"] }) {
  const ordered = [...samples].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()).slice(-30)
  if (ordered.length === 0) return <span className="font-mono text-xs text-muted-foreground">-</span>
  return <Tooltip delayDuration={150}><TooltipTrigger asChild><div className="flex h-8 items-end gap-px" tabIndex={0} aria-label={`最近 ${ordered.length} 次调用延迟`}>
    {ordered.map((sample, index) => { const first = latencyTone(sample.first_token_ms, "first"); const duration = latencyTone(sample.duration_ms, "duration"); return <span key={`${sample.created_at}-${index}`} className="h-full w-1 rounded-sm opacity-90 transition-opacity hover:opacity-100" style={{ background: `linear-gradient(to top, ${latencyColor[duration]} 0 50%, ${latencyColor[first]} 50% 100%)` }} /> })}
  </div></TooltipTrigger><TooltipContent side="top" align="end" className="max-h-96 w-[440px] max-w-[calc(100vw-24px)] overflow-y-auto border border-slate-200 bg-white p-0 text-[11px] text-slate-900 shadow-xl dark:border-slate-200 dark:bg-white dark:text-slate-900 [&>svg]:hidden"><div className="sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium"><span>最近 {ordered.length} 次调用</span><span className="text-slate-500">上半段首字 · 下半段总耗时</span></div><div className="divide-y divide-slate-100 px-3">{[...ordered].reverse().map((sample, index) => { const first = latencyTone(sample.first_token_ms, "first"); const duration = latencyTone(sample.duration_ms, "duration"); return <div key={`${sample.created_at}-detail-${index}`} className="grid grid-cols-[132px_minmax(0,1fr)] gap-2 py-2"><span className="whitespace-nowrap font-mono tabular-nums text-slate-500">{new Date(sample.created_at).toLocaleString()}</span><div className="min-w-0"><p className="truncate font-medium text-slate-900" title={sample.user_email || "用户邮箱未知"}>{sample.user_email || "用户邮箱未知"}</p><p className="mt-0.5 truncate text-slate-700" title={sample.model}>{sample.model || "-"}{sample.request_type ? ` · ${sample.request_type}` : ""}</p><p className="mt-1 flex flex-wrap gap-x-3 gap-y-1 font-mono font-semibold tabular-nums"><span className={first === "good" ? "text-emerald-700" : first === "warn" ? "text-amber-700" : first === "slow" ? "text-orange-700" : "text-red-700"}>首字 {formatLatency(sample.first_token_ms)}</span><span className={duration === "good" ? "text-emerald-700" : duration === "warn" ? "text-amber-700" : duration === "slow" ? "text-orange-700" : "text-red-700"}>总耗时 {formatLatency(sample.duration_ms)}</span></p></div></div> })}</div><div className="sticky bottom-0 flex flex-wrap gap-2 border-t border-slate-200 bg-slate-50 px-3 py-2 text-[10px] text-slate-500"><span><i className="mr-1 inline-block size-1.5 rounded-full bg-emerald-500" />{latencyText.good}</span><span><i className="mr-1 inline-block size-1.5 rounded-full bg-amber-400" />{latencyText.warn}</span><span><i className="mr-1 inline-block size-1.5 rounded-full bg-orange-500" />{latencyText.slow}</span><span><i className="mr-1 inline-block size-1.5 rounded-full bg-red-500" />{latencyText.critical}</span></div></TooltipContent></Tooltip>
}

function AccountName({ account }: { account: RelayAccountView }) {
  const href = account.base_url?.trim() || ""
  const name = href
    ? <a href={href} target="_blank" rel="noopener noreferrer" className="min-w-0 truncate text-sm font-medium text-foreground underline-offset-2 hover:text-brand hover:underline" title={account.name}>{account.name}</a>
    : <span className="min-w-0 truncate text-sm font-medium text-foreground" title={account.name}>{account.name}</span>
  return <Tooltip delayDuration={250}><TooltipTrigger asChild>{name}</TooltipTrigger><TooltipContent side="top" className="max-w-sm break-all text-xs">{account.name}<span className="mt-1 block text-muted-foreground">{href ? `点击打开 ${href}` : "该账号未配置 Base URL"}</span></TooltipContent></Tooltip>
}

function RiskSummary({ overview }: { overview: RelayOverview }) {
  const items = [
    { label: "远端账号", value: overview.summary.account_count, icon: Users, tone: "text-foreground", iconTone: "text-foreground", background: "bg-muted" },
    { label: "亏损风险", value: overview.summary.risk_account_count, icon: CircleAlert, tone: "text-danger", iconTone: "text-danger", background: "bg-danger/10" },
    { label: "无盈利候选", value: overview.summary.no_profit_account_count, icon: CircleAlert, tone: "text-yellow-600 dark:text-yellow-400", iconTone: "text-yellow-600 dark:text-yellow-400", background: "bg-yellow-400/10" },
    { label: "无安全候选", value: overview.summary.no_safe_candidate_count, icon: CircleAlert, tone: "text-yellow-600 dark:text-yellow-400", iconTone: "text-yellow-600 dark:text-yellow-400", background: "bg-muted" },
    { label: "成本未知", value: overview.summary.unknown_cost_count, icon: SlidersHorizontal, tone: "text-yellow-600 dark:text-yellow-400", iconTone: "text-yellow-600 dark:text-yellow-400", background: "bg-muted" },
    { label: "自动处理", value: overview.summary.auto_adjusted_count, icon: ShieldCheck, tone: "text-brand", iconTone: "text-brand", background: "bg-brand/10" },
  ]
  return <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">{items.map(({ label, value, icon: Icon, tone, iconTone, background }) => <Card key={label} className="border border-border p-4 shadow-none"><div className="flex items-start justify-between gap-2"><div><p className="text-xs text-muted-foreground">{label}</p><p className={cn("mt-1 text-xl font-bold tabular-nums", tone)}>{value}</p></div><span className={cn("flex size-8 shrink-0 items-center justify-center rounded-lg", background)}><Icon className={cn("size-4", iconTone)} /></span></div></Card>)}</div>
}

function UserBalanceHistoryDialog({ open, userEmail, userName, data, loading, error, onClose }: { open: boolean; userEmail: string; userName: string; data: { user: { balance?: number; created_at?: string; email?: string; username?: string }; total_recharged: number } | null; loading: boolean; error: string | null; onClose: () => void }) {
  return <Dialog open={open} onOpenChange={(value) => !value && onClose()}><DialogContent className="max-w-md"><DialogHeader><DialogTitle>用户余额</DialogTitle><DialogDescription className="flex flex-wrap items-center gap-2"><span className="min-w-0 truncate">{data?.user.email || userEmail || "-"}</span><span className="inline-flex max-w-full truncate rounded-md border border-brand/20 bg-brand/10 px-2 py-0.5 text-[11px] font-medium text-brand">{data?.user.username || userName || "-"}</span></DialogDescription></DialogHeader>{loading ? <div className="py-10 text-center text-sm text-muted-foreground">正在读取余额...</div> : error ? <p className="py-8 text-center text-sm text-danger">读取失败：{error}</p> : <div className="grid grid-cols-2 gap-3"><div className="rounded-md border border-border bg-muted/30 p-4"><p className="text-xs text-muted-foreground">当前余额</p><p className="mt-2 text-lg font-bold tabular-nums">{money(data?.user.balance)}</p></div><div className="rounded-md border border-border bg-muted/30 p-4"><p className="text-xs text-muted-foreground">累计充值</p><p className="mt-2 text-lg font-bold tabular-nums text-emerald-600 dark:text-emerald-400">{money(data?.total_recharged)}</p></div></div>}</DialogContent></Dialog>
}

function UsageTokenBreakdown({ row }: { row: RelayRecentUsage }) {
  const hasCache = row.cache_read_tokens > 0 || row.cache_creation_tokens > 0
  return <span className="space-y-2 font-mono tabular-nums">
    <span className="flex items-center gap-x-3 whitespace-nowrap">
      <span className="inline-flex items-center gap-1.5"><ArrowDown className="size-3.5 text-emerald-500" /><strong className="font-medium text-foreground">{row.input_tokens.toLocaleString("zh-CN")}</strong><span className="text-[10px] text-muted-foreground">输入</span></span>
      <span className="inline-flex items-center gap-1.5"><ArrowUp className="size-3.5 text-violet-500" /><strong className="font-medium text-foreground">{row.output_tokens.toLocaleString("zh-CN")}</strong><span className="text-[10px] text-muted-foreground">输出</span></span>
    </span>
    {hasCache ? <span className="flex flex-wrap gap-x-3 gap-y-1">
      {row.cache_read_tokens > 0 ? <span className="flex items-center gap-1.5 whitespace-nowrap"><Archive className="size-3.5 text-sky-500" /><strong className="font-medium text-foreground">{row.cache_read_tokens.toLocaleString("zh-CN")}</strong><span className="text-[10px] text-muted-foreground">缓存读</span></span> : null}
      {row.cache_creation_tokens > 0 ? <span className="flex items-center gap-1.5 whitespace-nowrap"><PencilLine className="size-3.5 text-amber-500" /><strong className="font-medium text-foreground">{row.cache_creation_tokens.toLocaleString("zh-CN")}</strong><span className="text-[10px] text-muted-foreground">缓存写</span>{row.cache_creation_1h_tokens > 0 ? <span className="rounded bg-orange-500/15 px-1 py-px text-[9px] font-semibold text-orange-600 dark:text-orange-400">1h</span> : null}{row.cache_ttl_overridden ? <span title="缓存 TTL 已由请求覆盖" className="rounded bg-rose-500/15 px-1 py-px text-[9px] font-semibold text-rose-600 dark:text-rose-400">R</span> : null}</span> : null}
    </span> : null}
  </span>
}

function RecentUsageTable({
  rows,
  loading,
  refreshing,
  error,
  onRefresh,
  groupNames,
  accountNames,
  accountURLs,
  systemCapacity,
  onUserClick,
}: {
  rows: RelayRecentUsage[];
  loading: boolean;
  refreshing: boolean;
  error: string | null;
  onRefresh: () => void;
  groupNames: Map<number, string>;
  accountNames: Map<number, string>;
  accountURLs: Map<number, string>;
  systemCapacity: { current: number; total: number };
  onUserClick: (userID: number, userEmail: string, userName: string) => void;
}) {
  const [open, setOpen] = usePersistedOpen("uh_relay_recent_usage_open");
  const [user, setUser] = useState("");
  const [group, setGroup] = useState("all");
  const groups = useMemo(
    () => [
      ...new Map(
        rows.map((row) => [
          row.group_id,
          {
            id: row.group_id,
            name:
              row.group_name ||
              groupNames.get(row.group_id) ||
              `分组 #${row.group_id}`,
          },
        ]),
      ).values(),
    ],
    [groupNames, rows],
  );
  const filtered = rows.filter((row) => {
    const name = row.user_email || row.user_name || `用户 #${row.user_id}`;
    const keyword = user.trim().toLowerCase();
    return (
      (!keyword ||
        name.toLowerCase().includes(keyword) ||
        row.user_name.toLowerCase().includes(keyword) ||
        String(row.user_id).includes(keyword)) &&
      (group === "all" || group === String(row.group_id))
    );
  });
  return (
    <Card className="recent-usage-table gap-0 overflow-hidden border border-border py-0 shadow-none">
      <CardHeader className="gap-3 px-4 py-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-sm">
              <History className="size-4 text-brand" />
              最近使用记录
            </CardTitle>
            {open ? (
              <p className="mt-1 text-xs text-muted-foreground">
                中转站最近 100 条使用记录。
              </p>
            ) : null}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {open ? (
              <span className="text-xs text-muted-foreground">
                {filtered.length} / {rows.length} 条
              </span>
            ) : null}
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-9"
              aria-label={open ? "收起最近使用记录" : "展开最近使用记录"}
              aria-expanded={open}
              onClick={() => setOpen((value) => !value)}
            >
              <ChevronDown
                className={cn(
                  "size-4 transition-transform duration-200",
                  open && "rotate-180",
                )}
              />
            </Button>
          </div>
        </div>
        {open ? (
          <div className="flex flex-wrap items-center justify-end gap-2">
            <div className="mr-auto flex min-w-36 items-center gap-2">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-brand/10 text-brand">
                <Gauge className="size-4" />
              </span>
              <div>
                <p className="text-[10px] text-muted-foreground">系统并发</p>
                <p className="font-mono text-sm font-semibold tabular-nums text-foreground">
                  {systemCapacity.current} / {systemCapacity.total}
                </p>
              </div>
            </div>
            {error ? (
              <span className="text-xs text-danger">读取失败：{error}</span>
            ) : null}
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={user}
                onChange={(event) => setUser(event.target.value)}
                placeholder="输入用户名或 ID"
                aria-label="输入用户名筛选"
                className="h-9 w-48 pl-8 text-xs"
              />
            </div>
            <Select value={group} onValueChange={setGroup}>
              <SelectTrigger className="h-9 w-40 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部分组</SelectItem>
                {groups.map((item) => (
                  <SelectItem key={String(item.id)} value={String(item.id)}>
                    {item.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="sm"
              className="h-9 gap-1.5"
              onClick={() => {
                setUser("");
                setGroup("all");
              }}
            >
              <RotateCcw className="size-3.5" />
              重置
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-9 gap-1.5"
              disabled={loading || refreshing}
              onClick={onRefresh}
            >
              <RefreshCw
                className={cn(
                  "size-3.5",
                  (loading || refreshing) && "animate-spin",
                )}
              />
              刷新
            </Button>
          </div>
        ) : null}
      </CardHeader>
      {open ? (
        <CardContent className="border-t border-border p-0">
          <div className="relative isolate h-[520px] max-h-[520px]">
            <div className="h-full overflow-auto">
              <div className="min-w-[1484px]">
                <div className="sticky top-0 z-30 grid grid-cols-[180px_160px_180px_150px_76px_240px_170px_160px_168px] gap-0 border-b border-border bg-muted text-[11px] text-foreground/90 font-medium [&>*]:px-4 [&>*]:py-2">
                  <span>用户邮箱</span>
                  <span>账户</span>
                  <span>模型</span>
                  <span>分组</span>
                  <span>类型</span>
                  <span>Token</span>
                  <span>费用</span>
                  <span>延迟</span>
                  <span>时间</span>
                </div>
                {filtered.length ? (
                  filtered.map((row, index) => {
                    const groupLabel =
                      row.group_name ||
                      groupNames.get(row.group_id) ||
                      `分组 #${row.group_id}`;
                    const accountLabel =
                      row.account_name ||
                      accountNames.get(row.account_id) ||
                      `账号 #${row.account_id}`;
                    const accountURL = accountURLs.get(row.account_id);
                    const userName = row.user_name || `用户 #${row.user_id}`;
                    const userEmail = row.user_email || userName;
                    const firstTone = latencyTone(row.first_token_ms, "first");
                    const durationTone = latencyTone(
                      row.duration_ms,
                      "duration",
                    );
                    return (
                      <div
                        key={`${row.id}-${index}`}
                        className="grid grid-cols-[180px_160px_180px_150px_76px_240px_170px_160px_168px] items-center gap-0 border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-2.5 last:[&>*]:border-b-0"
                      >
                        <button
                          type="button"
                          className="flex min-w-0 self-stretch flex-col justify-center text-left text-brand transition-colors hover:text-brand/80"
                          title={`查看 ${userEmail} 的当前余额和累计充值`}
                          onClick={() =>
                            onUserClick(row.user_id, userEmail, userName)
                          }
                        >
                          <span className="block truncate font-medium underline-offset-2 hover:underline">
                            {userEmail}
                          </span>
                          <span className="mt-0.5 block truncate text-[10px] text-muted-foreground">
                            {userName} · #{row.user_id}
                          </span>
                        </button>
                        {accountURL ? (
                          <a
                            href={accountURL}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="truncate font-medium text-brand underline-offset-2 transition-colors hover:text-brand/80 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            title={`打开 ${accountLabel}：${accountURL}`}
                          >
                            {accountLabel}
                          </a>
                        ) : (
                          <span className="truncate" title={accountLabel}>
                            {accountLabel}
                          </span>
                        )}
                        <span
                          className="truncate font-mono text-[11px]"
                          title={row.model}
                        >
                          {row.model || "-"}
                        </span>
                        <span className="min-w-0">
                          <span
                            className="inline-flex max-w-full truncate rounded-md border border-brand/20 bg-brand/10 px-2 py-1 text-[11px] font-medium text-brand"
                            title={groupLabel}
                          >
                            {groupLabel}
                          </span>
                        </span>
                        <span>
                          <span
                            className={cn(
                              "inline-flex whitespace-nowrap rounded border px-2 py-1 text-[10px] font-medium",
                              requestTypeClass(row.request_type),
                            )}
                          >
                            {requestTypeLabel(row.request_type)}
                          </span>
                        </span>
                        <UsageTokenBreakdown row={row} />
                        <span className="space-y-0.5 font-mono tabular-nums">
                          <span className="block whitespace-nowrap font-medium text-brand">
                            扣费 {money(row.user_charge)}
                          </span>
                          <span className="block whitespace-nowrap text-[10px] text-muted-foreground">
                            原始 {money(row.original_cost)}
                          </span>
                        </span>
                        <span className="relative space-y-1 pl-3 font-mono tabular-nums">
                          <i
                            className={cn(
                              "absolute inset-y-0 left-0 w-1 rounded-full",
                              firstTone === durationTone
                                ? latencyBarClass[firstTone]
                                : "bg-gradient-to-b from-40% to-60%",
                              firstTone === durationTone
                                ? ""
                                : firstTone === "good"
                                  ? "from-emerald-500"
                                  : firstTone === "warn"
                                    ? "from-amber-400"
                                    : firstTone === "slow"
                                      ? "from-orange-500"
                                      : "from-red-500",
                              firstTone === durationTone
                                ? ""
                                : durationTone === "good"
                                  ? "to-emerald-500"
                                  : durationTone === "warn"
                                    ? "to-amber-400"
                                    : durationTone === "slow"
                                      ? "to-orange-500"
                                      : "to-red-500",
                            )}
                          />
                          <span className="grid grid-cols-[42px_1fr] items-center gap-1 whitespace-nowrap">
                            <span className="text-muted-foreground">首字</span>
                            <strong
                              className={cn(
                                "font-medium",
                                latencyTextClass[firstTone],
                              )}
                            >
                              {formatLatency(row.first_token_ms)}
                            </strong>
                          </span>
                          <span className="grid grid-cols-[42px_1fr] items-center gap-1 whitespace-nowrap">
                            <span className="text-muted-foreground">
                              总耗时
                            </span>
                            <strong
                              className={cn(
                                "font-medium",
                                latencyTextClass[durationTone],
                              )}
                            >
                              {formatLatency(row.duration_ms)}
                            </strong>
                          </span>
                        </span>
                        <span className="font-mono text-[11px] text-muted-foreground">
                          {new Date(row.created_at).toLocaleString("zh-CN")}
                        </span>
                      </div>
                    );
                  })
                ) : (
                  <p className="py-12 text-center text-sm text-muted-foreground">
                    当前筛选没有使用记录
                  </p>
                )}
              </div>
            </div>
            {refreshing && rows.length > 0 ? (
              <div className="pointer-events-none absolute inset-x-0 top-10 z-40 flex justify-center">
                <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                  <RefreshCw className="size-3.5 animate-spin text-brand" />
                  正在刷新使用记录
                </span>
              </div>
            ) : null}
          </div>
        </CardContent>
      ) : null}
    </Card>
  );
}

function RiskRow({
  account,
  overview,
  selected,
  busy,
  scheduling,
  probing,
  testing,
  deleting,
  onToggle,
  onApply,
  onAddDowngrade,
  onEditGroups,
  onSchedulableChange,
  onProbe,
  onTest,
  onDelete,
}: {
  account: RelayAccountView;
  overview: RelayOverview;
  selected: boolean;
  busy: boolean;
  scheduling: boolean;
  probing: boolean;
  testing: boolean;
  deleting: boolean;
  onToggle: (checked: boolean) => void;
  onApply: () => void;
  onAddDowngrade: () => void;
  onEditGroups: () => void;
  onSchedulableChange: (checked: boolean) => void;
  onProbe: () => void;
  onTest: () => void;
  onDelete: () => void;
}) {
  const state = stateMeta[account.risk_state];
  const marginTone =
    account.margin == null
      ? "text-muted-foreground"
      : account.margin < 0
        ? "text-danger"
        : account.risk_state === "no_profit"
          ? "text-warning"
          : "text-success";
  const downgradeGroups = accountDowngradeGroups(account);
  const downgradeSummary = downgradeGroups
    .map((group) => `${group.name} · ${multiplier(group.rate_multiplier)}`)
    .join("、");
  return (
    <div className="relay-account-grid grid gap-3 border-b border-border px-4 py-2.5 last:border-0 lg:items-center lg:gap-0 lg:p-0 lg:[&>*]:px-4 lg:[&>*]:py-2.5">
      <div className="fixed-column-shadow-desktop fixed-column-shadow-left lg:sticky lg:left-0 lg:z-20 lg:flex lg:self-stretch lg:items-center lg:bg-card">
        <Checkbox
          checked={selected}
          onCheckedChange={(value) => onToggle(value === true)}
          aria-label={`选择账号 ${account.name}`}
        />
      </div>
      <div className="fixed-column-shadow-desktop fixed-column-shadow-left min-w-0 lg:sticky lg:left-[64px] lg:z-20 lg:flex lg:self-stretch lg:flex-col lg:justify-center lg:bg-card">
        <div className="flex items-center gap-2">
          <AccountName account={account} />
          <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
            {account.type || "-"}
          </span>
        </div>
        <p className="mt-1 truncate text-[11px] text-muted-foreground">
          #{account.external_id}
        </p>
      </div>
      <div className="flex items-center justify-between gap-2 lg:block">
        <span className="text-[11px] text-muted-foreground lg:hidden">
          平台
        </span>
        <PlatformBadge platform={account.platform} />
      </div>
      <div className="flex items-center justify-between gap-2 lg:block">
        <span className="text-[11px] text-muted-foreground lg:hidden">
          并发数
        </span>
        <span className="font-mono text-sm font-semibold tabular-nums text-foreground">
          {account.concurrency}
        </span>
      </div>
      <div className="flex items-center justify-between gap-2 lg:block">
        <span className="text-[11px] text-muted-foreground lg:hidden">
          容量
        </span>
        <span
          className={cn(
            "inline-flex items-center gap-1 rounded-md border px-1.5 py-1 font-mono text-[11px] font-semibold tabular-nums",
            capacityTone(account),
          )}
          title="实时并发占用 / 并发上限"
        >
          <Gauge className="size-3" />
          {account.current_concurrency} / {account.concurrency}
        </span>
      </div>
      <div className="flex items-center justify-between gap-2 lg:block">
        <span className="text-[11px] text-muted-foreground lg:hidden">
          优先级
        </span>
        <span className="font-mono text-sm font-semibold tabular-nums text-foreground">
          {account.priority}
        </span>
      </div>
      <div className="flex items-center justify-between gap-2 lg:block">
        <span className="text-[11px] text-muted-foreground lg:hidden">
          重试次数
        </span>
        <span
          className={cn(
            "font-mono text-sm font-semibold tabular-nums",
            account.pool_mode ? "text-foreground" : "text-muted-foreground",
          )}
          title={
            account.pool_mode
              ? "同账号重试次数"
              : "池模式未开启，当前重试次数不生效"
          }
        >
          {account.pool_mode_retry_count}
        </span>
      </div>
      <div className="flex min-h-11 items-center gap-2 lg:min-h-0 lg:flex-col lg:items-start lg:gap-1">
        <Switch
          checked={account.schedulable}
          onCheckedChange={onSchedulableChange}
          disabled={busy || scheduling}
          aria-label={`${account.name}${account.schedulable ? "关闭" : "开启"}调度`}
        />
        <span
          className={cn(
            "text-[11px]",
            account.schedulable ? "text-success" : "text-muted-foreground",
          )}
        >
          {account.schedulable ? "已开启" : "已关闭"}
        </span>
      </div>
      <div className="min-w-0">
        <p className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground lg:hidden">
          模型类型
        </p>
        <span
          className={cn(
            "inline-flex max-w-full truncate rounded-md border px-2 py-1 text-[11px] font-medium",
            account.model_type
              ? "border-brand/20 bg-brand/10 text-brand"
              : "border-border bg-muted text-muted-foreground",
          )}
          title={account.model_type || "未绑定，不参与自动调组"}
        >
          {account.model_type || "未绑定"}
        </span>
      </div>
      <div className="min-w-0">
        <p className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground">
          最近调用
        </p>
        <LatencyBars samples={account.latency_samples ?? []} />
      </div>
      <div className="min-w-0">
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground">
          区间消费
        </p>
        <p className="mt-1 font-mono text-sm font-semibold tabular-nums text-foreground">
          {tokenAmount(account.usage_total_tokens)} Token
        </p>
        <p className="mt-0.5 text-[11px] font-medium tabular-nums text-brand">
          扣费 {chargeAmount(account.user_charge_amount)}
        </p>
      </div>
      <div>
        <p className="font-mono text-base font-semibold tabular-nums text-foreground">
          {multiplier(account.cost_multiplier)}
        </p>
        <p
          className="mt-1 max-w-[150px] truncate text-[10px] text-muted-foreground"
          title={sourceLabel(account, overview)}
        >
          {sourceLabel(account, overview)}
        </p>
      </div>
      <div className="min-w-0">
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground">
          当前销售分组
        </p>
        <p className="mt-1 line-clamp-2 text-xs text-foreground">
          <GroupNames groups={account.current_groups} />
        </p>
      </div>
      <div>
        <span
          className={cn(
            "inline-flex rounded px-2 py-1 text-[11px] font-medium",
            state.tone,
          )}
        >
          {state.label}
        </span>
        <p
          className={cn(
            "mt-1 font-mono text-sm font-semibold tabular-nums",
            marginTone,
          )}
        >
          空间 {account.margin == null ? "-" : multiplier(account.margin)}
        </p>
      </div>
      <div>
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground lg:hidden">
          利润
        </p>
        <p
          className={cn(
            "font-mono text-sm font-semibold tabular-nums",
            marginTone,
          )}
          title="利润率 = (销售倍率 - 成本倍率) / 成本倍率"
        >
          {profitPercent(account.margin, account.cost_multiplier)}
        </p>
      </div>
      <div className="min-w-0">
        <span className="mr-2 text-[11px] text-muted-foreground lg:hidden">
          可降级
        </span>
        <span
          className={cn(
            "inline-flex rounded px-2 py-1 text-[11px] font-medium",
            downgradeGroups.length
              ? "bg-success/10 text-success"
              : "bg-muted text-muted-foreground",
          )}
        >
          {downgradeGroups.length ? `${downgradeGroups.length} 个` : "否"}
        </span>
        {downgradeGroups.length ? (
          <p
            className="mt-1 line-clamp-2 text-[11px] text-muted-foreground"
            title={downgradeSummary}
          >
            {downgradeSummary}
          </p>
        ) : null}
      </div>
      <div className="fixed-column-shadow-desktop fixed-column-shadow-right flex min-w-0 flex-nowrap justify-start gap-1.5 lg:sticky lg:right-0 lg:z-30 lg:self-stretch lg:items-center lg:justify-end lg:bg-card">
        <Tooltip delayDuration={250}>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-8 shrink-0 text-brand"
              aria-label="测试连接"
              disabled={busy || testing || deleting}
              onClick={onTest}
            >
              <Play className={cn("size-3.5", testing && "animate-pulse")} />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="top">测试连接</TooltipContent>
        </Tooltip>
        {account.type?.toLowerCase() === "apikey" ? (
          <Tooltip delayDuration={250}>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="icon"
                className="size-8 shrink-0"
                aria-label="探测倍率"
                disabled={busy || probing || deleting}
                onClick={onProbe}
              >
                <RefreshCw
                  className={cn("size-3.5", probing && "animate-spin")}
                />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">探测倍率</TooltipContent>
          </Tooltip>
        ) : null}
        <Tooltip delayDuration={250}>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-8 shrink-0"
              aria-label="调整分组"
              disabled={busy || probing || deleting}
              onClick={onEditGroups}
            >
              <SlidersHorizontal className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="top">调整分组</TooltipContent>
        </Tooltip>
        {downgradeGroups.length ? (
          <Tooltip delayDuration={250}>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="icon"
                className="size-8 shrink-0 border-success/30 bg-success/10 text-success hover:bg-success/20 hover:text-success"
                aria-label={`加入全部 ${downgradeGroups.length} 个可降级分组`}
                disabled={busy || probing || deleting}
                onClick={onAddDowngrade}
              >
                <Plus className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">加入全部可降级分组</TooltipContent>
          </Tooltip>
        ) : null}
        {account.can_apply ? (
          <Tooltip delayDuration={250}>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="icon"
                className="size-8 shrink-0 border-brand/30 bg-brand/10 text-brand hover:bg-brand/20 hover:text-brand"
                aria-label="应用建议"
                disabled={busy || probing || deleting}
                onClick={onApply}
              >
                <Check className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">应用建议</TooltipContent>
          </Tooltip>
        ) : null}
        <Tooltip delayDuration={250}>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-8 shrink-0 border-danger/30 text-danger hover:bg-danger/10 hover:text-danger"
              aria-label={`删除账号${account.name}`}
              disabled={busy || deleting}
              onClick={onDelete}
            >
              <Trash2 className={cn("size-3.5", deleting && "animate-pulse")} />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="top">删除账号</TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}

type RelayGroupPatch = Partial<Pick<RelayGroupView, "name" | "description" | "rate_multiplier" | "is_exclusive" | "status" | "model_types" | "monitor_enabled">>

function GroupManagementRow({ group, accounts, onUpdate, onTest, onDelete, testing, deleting, dragDisabled, dragging, onDragStart, onDragOver, onDrop }: { group: RelayGroupView; accounts: RelayAccountView[]; onUpdate: (groupID: number, patch: RelayGroupPatch) => Promise<void>; onTest: (group: RelayGroupView) => void; onDelete: (group: RelayGroupView) => void; testing: boolean; deleting: boolean; dragDisabled: boolean; dragging: boolean; onDragStart: (id: number) => void; onDragOver: (id: number) => void; onDrop: (id: number) => void }) {
  const [expanded, setExpanded] = useState(false)
  const [name, setName] = useState(group.name)
  const [description, setDescription] = useState(group.description ?? "")
  const [rate, setRate] = useState(String(group.rate_multiplier))
  const [modelTypes, setModelTypes] = useState((group.model_types ?? []).join(", "))
  const [busy, setBusy] = useState(false)
  const [dirty, setDirty] = useState(false)
  const active = group.status?.toLowerCase() === "active"
  const parsedRate = Number(rate)
  const parsedTypes = Array.from(new Set(modelTypes.split(",").map((value) => value.trim().toLowerCase()).filter(Boolean)))
  const changed = name.trim() !== group.name || description.trim() !== (group.description ?? "") || (Number.isFinite(parsedRate) && parsedRate !== group.rate_multiplier) || JSON.stringify(parsedTypes) !== JSON.stringify(group.model_types ?? [])
  useEffect(() => {
    if (dirty) return
    setName(group.name)
    setDescription(group.description ?? "")
    setRate(String(group.rate_multiplier))
    setModelTypes((group.model_types ?? []).join(", "))
  }, [dirty, group.name, group.description, group.rate_multiplier, group.model_types])
  useEffect(() => { if (dirty && !changed) setDirty(false) }, [changed, dirty])

  async function update(patch: RelayGroupPatch) {
    setBusy(true)
    try {
      await onUpdate(group.external_id, patch)
      return true
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "分组更新失败")
      return false
    } finally {
      setBusy(false)
    }
  }

  async function save() {
    if (!name.trim()) { toast.error("分组名称不能为空"); return }
    if (!Number.isFinite(parsedRate) || parsedRate < 0) { toast.error("分组倍率必须是非负数"); return }
    if (await update({ name: name.trim(), description: description.trim(), rate_multiplier: parsedRate, model_types: parsedTypes })) setDirty(false)
  }

  return <div className={cn("border-b border-border last:border-0", dragging && "bg-brand/5")} onDragOver={(event) => { if (!dragDisabled) { event.preventDefault(); onDragOver(group.external_id) } }} onDrop={(event) => { event.preventDefault(); if (!dragDisabled) onDrop(group.external_id) }}>
    <div className="grid gap-3 px-4 py-2.5 lg:min-w-[1422px] lg:grid-cols-[88px_minmax(152px,.8fr)_132px_minmax(172px,1fr)_114px_minmax(152px,.9fr)_128px_120px_144px_96px_144px] lg:items-center lg:gap-0 lg:p-0 lg:[&>*]:px-4 lg:[&>*]:py-2.5">
      <div className="flex items-center gap-1"><button type="button" draggable={!dragDisabled} onDragStart={() => onDragStart(group.external_id)} aria-label={dragDisabled ? "清除临时排序后可拖拽分组" : `拖拽排序${group.name}`} className={cn("flex size-8 cursor-grab items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground active:cursor-grabbing", dragDisabled && "cursor-not-allowed opacity-40")}><GripVertical className="size-4" /></button><button type="button" aria-expanded={expanded} aria-label={`${expanded ? "收起" : "展开"}${group.name}中的账号`} onClick={() => setExpanded((value) => !value)} className="flex size-9 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"><ChevronDown className={cn("size-4 transition-transform duration-200", expanded && "rotate-180")} /></button></div>
      <div><Input value={name} onChange={(event) => { setName(event.target.value); setDirty(true) }} aria-label={`${group.name}分组名称`} className="h-10 min-w-0 text-sm" disabled={busy} /></div>
      <div className="flex items-center justify-between gap-3 lg:block"><span className="text-xs text-muted-foreground lg:hidden">平台</span><PlatformBadge platform={group.platform} /></div>
      <div><Input value={description} onChange={(event) => { setDescription(event.target.value); setDirty(true) }} aria-label={`${group.name}分组描述`} placeholder="暂无描述" className="h-10 min-w-0 text-sm" disabled={busy} /></div>
      <div><Input value={rate} onChange={(event) => { setRate(event.target.value); setDirty(true) }} aria-label={`${group.name}分组倍率`} inputMode="decimal" className="h-10 font-mono text-sm tabular-nums" disabled={busy} /></div>
      <div><Input value={modelTypes} onChange={(event) => { setModelTypes(event.target.value); setDirty(true) }} aria-label={`${group.name}模型类型`} placeholder="gpt, claude" className="h-10 min-w-0 text-sm" disabled={busy} /></div>
      <div className="flex min-h-11 items-center justify-between gap-3 lg:justify-start"><span className="text-xs text-muted-foreground lg:hidden">类型</span><div className="flex items-center gap-2"><Switch checked={Boolean(group.is_exclusive)} onCheckedChange={(checked) => void update({ is_exclusive: checked, monitor_enabled: !checked })} disabled={busy} aria-label={`${group.name}切换为${group.is_exclusive ? "公开" : "专属"}分组`} title="公开自动开启监控，专属自动关闭监控" /><span className={cn("text-xs font-medium", group.is_exclusive ? "text-warning" : "text-brand")}>{group.is_exclusive ? "专属" : "公开"}</span></div></div>
      <div className="flex min-h-11 items-center justify-between gap-3 lg:justify-start"><span className="text-xs text-muted-foreground lg:hidden">监控</span><div className="flex items-center gap-2"><Switch checked={group.monitor_enabled} onCheckedChange={(checked) => void update({ monitor_enabled: checked })} disabled={busy} aria-label={`${group.name}${group.monitor_enabled ? "关闭" : "开启"}公开监控`} /><span className={cn("text-xs font-medium", group.monitor_enabled ? "text-brand" : "text-muted-foreground")}>{group.monitor_enabled ? "已监控" : "未监控"}</span></div></div>
      <div className="flex min-h-11 items-center justify-between gap-3 lg:justify-start"><span className="text-xs text-muted-foreground lg:hidden">状态</span><div className="flex items-center gap-2"><Switch checked={active} onCheckedChange={(checked) => void update({ status: checked ? "active" : "inactive" })} disabled={busy} aria-label={`${group.name}${active ? "停用" : "启用"}`} /><span className={cn("text-xs font-medium", active ? "text-success" : "text-muted-foreground")}>{active ? "已启用" : "已停用"}</span></div></div>
      <div className="flex items-center justify-between gap-2 text-xs lg:block"><span className="text-muted-foreground lg:hidden">账号</span><p className="font-mono text-sm font-semibold tabular-nums text-foreground">{accounts.length}</p></div>
      <div className="fixed-column-shadow-desktop fixed-column-shadow-right flex flex-nowrap justify-start gap-1.5 lg:sticky lg:right-0 lg:z-10 lg:self-stretch lg:items-center lg:justify-end lg:bg-card"><Tooltip delayDuration={250}><TooltipTrigger asChild><Button type="button" variant="outline" size="icon" className="size-8 shrink-0 text-brand" aria-label={`快速测试${group.name}`} disabled={busy || testing || deleting} onClick={() => onTest(group)}><Play className={cn("size-3.5", testing && "animate-pulse")} /></Button></TooltipTrigger><TooltipContent side="top">快速测试</TooltipContent></Tooltip><Tooltip delayDuration={250}><TooltipTrigger asChild><Button type="button" variant="outline" size="icon" className="size-8 shrink-0" aria-label={`保存${group.name}`} disabled={busy || testing || deleting || !changed} onClick={() => void save()}><Save className={cn("size-3.5", busy && "animate-pulse")} /></Button></TooltipTrigger><TooltipContent side="top">保存修改</TooltipContent></Tooltip><Tooltip delayDuration={250}><TooltipTrigger asChild><Button type="button" variant="outline" size="icon" className="size-8 shrink-0 border-danger/30 text-danger hover:bg-danger/10 hover:text-danger" aria-label={`删除分组${group.name}`} disabled={busy || testing || deleting} onClick={() => onDelete(group)}><Trash2 className={cn("size-3.5", deleting && "animate-pulse")} /></Button></TooltipTrigger><TooltipContent side="top">删除分组</TooltipContent></Tooltip></div>
    </div>
    {expanded ? <div className="border-t border-border bg-muted/20 px-4 py-3"><div className="mb-2 flex items-center justify-between gap-3"><p className="text-xs font-medium text-foreground">分组账号</p><span className="text-[11px] text-muted-foreground">{accounts.length} 个</span></div>{accounts.length === 0 ? <p className="rounded-md border border-dashed border-border px-4 py-5 text-center text-xs text-muted-foreground">该分组暂未关联账号</p> : <div className="grid grid-cols-1 gap-3 sm:grid-cols-[repeat(auto-fill,minmax(260px,320px))] sm:justify-start">{accounts.map((account) => { const state = stateMeta[account.risk_state]; return <article key={account.external_id} className="rounded-md border border-border bg-card px-3 py-2.5 shadow-xs"><div className="flex items-start justify-between gap-2"><div className="min-w-0"><AccountName account={account} /><div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground"><span>{account.platform || "-"} · {account.type || "-"}</span><span>#{account.external_id}</span>{account.model_type ? <span className="rounded bg-brand/10 px-1.5 py-0.5 font-medium text-brand">{account.model_type}</span> : null}</div></div><span className={cn("shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium", state.tone)}>{state.label}</span></div><div className="mt-2 flex items-center justify-between gap-3 border-t border-border pt-2 text-[11px]"><span className="text-muted-foreground">成本倍率 <strong className="ml-1 font-mono text-sm font-semibold tabular-nums text-foreground">{multiplier(account.cost_multiplier)}</strong></span><span className={cn("shrink-0 font-medium", account.schedulable ? "text-success" : "text-muted-foreground")}>{account.schedulable ? "调度已开启" : "调度已关闭"}</span></div></article> })}</div>}</div> : null}
  </div>
}

function GroupManagement({ overview, busy, refreshing, testingGroupID, deletingGroupID, onUpdate, onOrderChange, onTest, onDelete, onRefresh }: { overview: RelayOverview; busy: boolean; refreshing: boolean; testingGroupID: number | null; deletingGroupID: number | null; onUpdate: (groupID: number, patch: RelayGroupPatch) => Promise<void>; onOrderChange: (ids: number[]) => Promise<void>; onTest: (group: RelayGroupView) => void; onDelete: (group: RelayGroupView) => void; onRefresh: () => void }) {
  const [open, setOpen] = usePersistedOpen("uh_relay_group_management_open")
  const [typeFilter, setTypeFilter] = useState<"public" | "exclusive" | "all">("all")
  const [statusFilter, setStatusFilter] = useState<"active" | "inactive" | "all">("all")
  const [sort, setSort] = useState<SortState<GroupSortKey>>(null)
  const [localOrder, setLocalOrder] = useState<number[]>([])
  const [draggingID, setDraggingID] = useState<number | null>(null)
  const [savingOrder, setSavingOrder] = useState(false)
  const [inputFocused, setInputFocused] = useState(false)
  const [tableOverview, setTableOverview] = useState(overview)
  useEffect(() => { if (!inputFocused) setTableOverview(overview) }, [inputFocused, overview])
  useEffect(() => { setLocalOrder(tableOverview.groups.map((group) => group.external_id)) }, [tableOverview.groups])
  const accountsByGroup = useMemo(() => {
    const result = new Map<number, RelayAccountView[]>()
    for (const account of tableOverview.accounts) {
      for (const group of account.current_groups) {
        const items = result.get(group.external_id) ?? []
        items.push(account)
        result.set(group.external_id, items)
      }
    }
    return result
  }, [tableOverview.accounts])
  const orderedGroups = useMemo(() => {
    const byID = new Map(tableOverview.groups.map((group) => [group.external_id, group]))
    const ids = localOrder.length ? localOrder : tableOverview.groups.map((group) => group.external_id)
    return ids.map((id) => byID.get(id)).filter((group): group is RelayGroupView => Boolean(group))
  }, [localOrder, tableOverview.groups])
  const groups = useMemo(() => {
    const filtered = orderedGroups.filter((group) => {
      if (typeFilter === "public" && group.is_exclusive) return false
      if (typeFilter === "exclusive" && !group.is_exclusive) return false
      const active = group.status?.toLowerCase() === "active"
      if (statusFilter === "active" && !active) return false
      if (statusFilter === "inactive" && active) return false
      return true
    })
    if (!sort) return filtered
    const direction = sort.direction === "asc" ? 1 : -1
    return [...filtered].sort((a, b) => {
      const compared = sort.key === "name" ? a.name.localeCompare(b.name, "zh-CN") : a.rate_multiplier - b.rate_multiplier
      return compared * direction || a.name.localeCompare(b.name, "zh-CN")
    })
  }, [orderedGroups, sort, statusFilter, typeFilter])
  function moveGroup(targetID: number) {
    if (draggingID == null || draggingID === targetID || sort) return
    const visibleIDs = groups.map((group) => group.external_id)
    const from = visibleIDs.indexOf(draggingID)
    const to = visibleIDs.indexOf(targetID)
    if (from < 0 || to < 0) return
    const reorderedVisible = [...visibleIDs]
    reorderedVisible.splice(from, 1)
    reorderedVisible.splice(to, 0, draggingID)
    let cursor = 0
    const next = (localOrder.length ? localOrder : tableOverview.groups.map((group) => group.external_id)).map((id) => visibleIDs.includes(id) ? reorderedVisible[cursor++] : id)
    setLocalOrder(next)
    setDraggingID(null)
    setSavingOrder(true)
    void onOrderChange(next).catch((error) => toast.error(error instanceof Error ? error.message : "保存分组排序失败")).finally(() => setSavingOrder(false))
  }
  function toggleSort(key: GroupSortKey, initialDirection: SortDirection) {
    setSort((current) => current?.key === key ? { key, direction: current.direction === "asc" ? "desc" : "asc" } : { key, direction: initialDirection })
  }
  function resetFilters() { setTypeFilter("all"); setStatusFilter("all"); setSort(null) }
  return <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
    <CardHeader className="gap-3 px-4 py-3">
      <div className="flex items-center justify-between gap-3"><div className="min-w-0"><CardTitle className="flex items-center gap-2 text-sm font-semibold"><Layers3 className="size-4 text-brand" />分组管理</CardTitle>{open ? <p className="mt-1 text-xs text-muted-foreground">编辑名称、描述、倍率和模型类型；分组类型、监控与状态可直接切换。</p> : null}</div><div className="flex shrink-0 items-center gap-2">{open ? <span className="text-xs text-muted-foreground">{groups.length} / {tableOverview.groups.length} 个</span> : null}<Button type="button" variant="ghost" size="icon" className="size-9" aria-label={open ? "收起分组管理" : "展开分组管理"} aria-expanded={open} onClick={() => setOpen((value) => !value)}><ChevronDown className={cn("size-4 transition-transform duration-200", open && "rotate-180")} /></Button></div></div>
      {open ? <div className="flex flex-wrap justify-end gap-2"><Select value={typeFilter} onValueChange={(value) => setTypeFilter(value as typeof typeFilter)}><SelectTrigger className="h-9 w-[calc(var(--spacing)*26)] text-xs" aria-label="筛选分组类型"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="public">公开</SelectItem><SelectItem value="exclusive">专属</SelectItem><SelectItem value="all">全部类型</SelectItem></SelectContent></Select><Select value={statusFilter} onValueChange={(value) => setStatusFilter(value as typeof statusFilter)}><SelectTrigger className="h-9 w-[calc(var(--spacing)*26)] text-xs" aria-label="筛选分组状态"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="active">已启用</SelectItem><SelectItem value="inactive">已停用</SelectItem><SelectItem value="all">全部状态</SelectItem></SelectContent></Select><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={resetFilters}><RotateCcw className="size-3.5" />重置</Button><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" disabled={busy || refreshing || inputFocused} title={inputFocused ? "编辑中，刷新已暂停" : undefined} onClick={onRefresh}><RefreshCw className={cn("size-3.5", refreshing && !inputFocused && "animate-spin")} />刷新</Button>{inputFocused ? <span className="self-center text-[11px] font-medium text-warning" aria-live="polite">编辑中，刷新已暂停</span> : null}{sort ? <span className="self-center text-[11px] text-muted-foreground">清除名称/倍率排序后可拖拽</span> : null}{savingOrder ? <span className="self-center text-[11px] text-muted-foreground">正在保存排序…</span> : null}</div> : null}
    </CardHeader>
    {open ? <CardContent className="border-t border-border p-0"><div className="relative h-[min(56vh,560px)]"><div className="h-full overflow-auto" onFocusCapture={(event) => { if (event.target instanceof HTMLInputElement) setInputFocused(true) }} onBlurCapture={(event) => { if (!(event.target instanceof HTMLInputElement)) return; if (event.relatedTarget instanceof HTMLInputElement && event.currentTarget.contains(event.relatedTarget)) return; setInputFocused(false) }}><div className="isolate"><div className="sticky top-0 z-30 hidden min-w-[1422px] grid-cols-[88px_minmax(152px,.8fr)_132px_minmax(172px,1fr)_114px_minmax(152px,.9fr)_128px_120px_144px_96px_144px] border-b border-border bg-muted text-[11px] text-foreground/90 font-medium lg:grid lg:[&>*]:px-4 lg:[&>*]:py-2"><span /><SortButton label="名称" active={sort?.key === "name"} direction={sort?.key === "name" ? sort.direction : undefined} onClick={() => toggleSort("name", "asc")} /><span>平台</span><span>描述</span><SortButton label="倍率" active={sort?.key === "rate"} direction={sort?.key === "rate" ? sort.direction : undefined} onClick={() => toggleSort("rate", "desc")} /><span className="inline-flex items-center gap-1">模型类型<ModelTypeHelp /></span><span>类型</span><span>监控</span><span>状态</span><span>账号</span><span className="fixed-column-shadow-desktop fixed-column-shadow-right sticky right-0 z-40 flex self-stretch items-center justify-center bg-muted">操作</span></div>{groups.length === 0 ? <p className="px-4 py-10 text-center text-sm text-muted-foreground">当前筛选没有分组</p> : groups.map((group) => <GroupManagementRow key={group.external_id} group={group} accounts={accountsByGroup.get(group.external_id) ?? []} onUpdate={onUpdate} onTest={onTest} onDelete={onDelete} testing={testingGroupID === group.external_id} deleting={deletingGroupID === group.external_id} dragDisabled={Boolean(sort) || savingOrder} dragging={draggingID === group.external_id} onDragStart={setDraggingID} onDragOver={() => undefined} onDrop={moveGroup} />)}</div></div>{busy || (refreshing && !inputFocused) ? <div className="pointer-events-none absolute inset-x-0 top-10 z-40 flex justify-center"><span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><RefreshCw className="size-3.5 animate-spin text-brand" />正在刷新分组列表</span></div> : null}</div></CardContent> : null}
  </Card>
}

function isProfitableGroupCandidate(account: RelayAccountView, group: RelayGroupOption) {
  if (account.cost_multiplier == null || group.status?.toLowerCase() !== "active") return false
  if ((account.platform || "").trim().toLowerCase() !== (group.platform || "").trim().toLowerCase()) return false
  const modelType = (account.model_type || "").trim().toLowerCase()
  if (!modelType || !group.model_types?.map((value) => value.trim().toLowerCase()).includes(modelType)) return false
  const accountType = (account.type || "unknown").trim().toLowerCase()
  if (group.require_oauth_only && accountType === "apikey") return false
  if (group.account_types?.length && !group.account_types.map((value) => value.trim().toLowerCase()).includes(accountType)) return false
  return group.rate_multiplier > account.cost_multiplier
}

function GroupEditor({ account, groups, open, busy, onOpenChange, onSave }: { account: RelayAccountView | null; groups: RelayGroupOption[]; open: boolean; busy: boolean; onOpenChange: (open: boolean) => void; onSave: (ids: number[]) => void }) {
  const [ids, setIDs] = useState<number[]>([])
  useEffect(() => setIDs(account?.current_groups.map((group) => group.external_id) ?? []), [account])
  const orderedGroups = useMemo(() => publicFirst(groups), [groups])
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="max-w-xl"><DialogHeader><DialogTitle>手动调整账号分组</DialogTitle><DialogDescription>{account ? <>{account.name} · {account.platform || "-"} · {account.type || "-"}{account.cost_multiplier == null ? " · 当前成本未知" : <> · 当前成本 <strong className="font-mono font-semibold text-foreground">{multiplier(account.cost_multiplier)}</strong></>}</> : "选择销售分组"}</DialogDescription></DialogHeader><div className="flex flex-wrap gap-2 text-[11px] text-muted-foreground"><span className="rounded bg-success/10 px-2 py-1 font-medium text-success">有利润候选</span><span>启用、同平台、同账号类型、同模型类型且倍率高于当前账号成本</span></div><div className="max-h-[min(52vh,460px)] space-y-2 overflow-y-auto pr-1">{orderedGroups.map((group) => { const checked = ids.includes(group.external_id); const profitable = account ? isProfitableGroupCandidate(account, group) : false; return <label key={group.external_id} className={cn("flex cursor-pointer items-center justify-between gap-3 border px-3 py-3 transition-colors", checked ? "border-brand/50 bg-brand/5" : profitable ? "border-success/30 bg-success/5 hover:bg-success/10" : "border-border hover:bg-muted/40")}><span className="flex min-w-0 items-center gap-3"><Checkbox checked={checked} onCheckedChange={(value) => setIDs((current) => value === true ? [...new Set([...current, group.external_id])] : current.filter((id) => id !== group.external_id))} /><span className="min-w-0"><span className="flex flex-wrap items-center gap-1.5"><span className="truncate text-sm font-medium">{group.name}</span>{profitable ? <span className="rounded bg-success/10 px-1.5 py-0.5 text-[10px] font-medium text-success">有利润候选</span> : null}<span className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium", group.is_exclusive ? "bg-warning/10 text-warning" : "bg-blue-500/10 text-blue-700 dark:text-blue-400")}>{group.is_exclusive ? "专属" : "公开"}</span></span><span className="mt-1 block text-[11px] text-muted-foreground">{group.platform || "-"} · {group.model_types?.length ? group.model_types.join("/") : "未配置模型类型"} · {group.require_oauth_only ? "OAuth 类型" : group.account_types?.length ? group.account_types.join("/") : "通用类型"} · 倍率 <strong className="font-mono text-sm font-semibold tabular-nums text-foreground">{multiplier(group.rate_multiplier)}</strong></span></span></span><span className={cn("size-2 rounded-full", group.status === "active" ? "bg-success" : "bg-muted-foreground")} /></label> })}</div><DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button><Button type="button" disabled={busy} onClick={() => onSave(ids)}>保存分组</Button></DialogFooter></DialogContent></Dialog>
}

function BatchGroupEditor({ groups, count, open, busy, onOpenChange, onSave }: { groups: RelayGroupOption[]; count: number; open: boolean; busy: boolean; onOpenChange: (open: boolean) => void; onSave: (ids: number[]) => void }) {
  const [ids, setIDs] = useState<number[]>([])
  useEffect(() => { if (open) setIDs([]) }, [open])
  const orderedGroups = useMemo(() => publicFirst(groups), [groups])
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="max-w-xl"><DialogHeader><DialogTitle>批量调整销售分组</DialogTitle><DialogDescription>将为已选的 {count} 个账号设置相同的销售分组，提交后逐个同步到 Sub2API。</DialogDescription></DialogHeader><div className="max-h-[min(52vh,460px)] space-y-2 overflow-y-auto pr-1">{orderedGroups.map((group) => { const checked = ids.includes(group.external_id); return <label key={group.external_id} className={cn("flex cursor-pointer items-center justify-between gap-3 border px-3 py-2.5 transition-colors", checked ? "border-brand/50 bg-brand/5" : "border-border hover:bg-muted/40")}><span className="flex min-w-0 items-center gap-3"><Checkbox checked={checked} onCheckedChange={(value) => setIDs((current) => value === true ? [...new Set([...current, group.external_id])] : current.filter((id) => id !== group.external_id))} /><span className="min-w-0"><span className="flex flex-wrap items-center gap-1.5"><span className="block truncate text-sm font-medium">{group.name}</span><span className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium", group.is_exclusive ? "bg-warning/10 text-warning" : "bg-blue-500/10 text-blue-700 dark:text-blue-400")}>{group.is_exclusive ? "专属" : "公开"}</span></span><span className="mt-1 block text-[11px] text-muted-foreground">{group.platform || "-"} · {group.require_oauth_only ? "OAuth 类型" : "通用类型"} · 倍率 {multiplier(group.rate_multiplier)}</span></span></span><span className={cn("size-2 rounded-full", group.status === "active" ? "bg-success" : "bg-muted-foreground")} /></label> })}</div><DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button><Button type="button" disabled={busy} onClick={() => onSave(ids)}>保存分组</Button></DialogFooter></DialogContent></Dialog>
}

export default function RelayStationsPage() {
  const stations = useRelayStations()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [selectedID, setSelectedID] = useState<number | null>(null)
  const [detailModule, setDetailModule] = usePersistedDetailModule("uh_relay_detail_module")
  const [usageRange, setUsageRange] = useState<RelayUsageRange>("today")
  const overview = useRelayOverview(selectedID)
  const usage = useRelayUsage(detailModule === "accounts" ? selectedID : null, usageRange)
  const recentUsage = useRelayRecentUsage(detailModule === "usage" ? selectedID : null)
  const [historyUser, setHistoryUser] = useState<{ id: number; email: string; name: string } | null>(null)
  const userHistory = useRelayUserBalanceHistory(selectedID, historyUser?.id ?? null, 1)
  const syncSettings = useSyncSettings()
  const [form, setForm] = useState<StationForm>(emptyForm)
  const [editingID, setEditingID] = useState<number | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [autoRateSyncEnabled, setAutoRateSyncEnabled] = useState(false)
  const [autoRateSyncInterval, setAutoRateSyncInterval] = useState(60)
  const [autoSnapshotSyncEnabled, setAutoSnapshotSyncEnabled] = useState(false)
  const [autoSnapshotSyncInterval, setAutoSnapshotSyncInterval] = useState(3600)
  const [autoAdjustEnabled, setAutoAdjustEnabled] = useState(false)
  const [autoAdjustNoProfitEnabled, setAutoAdjustNoProfitEnabled] = useState(false)
  const [autoPriorityEnabled, setAutoPriorityEnabled] = useState(false)
  const [autoPriorityRecallEnabled, setAutoPriorityRecallEnabled] = useState(false)
  const [autoPriorityRecallMinutes, setAutoPriorityRecallMinutes] = useState(180)
  const [accountNameFilter, setAccountNameFilter] = useState("")
  const [riskFilter, setRiskFilter] = useState<RiskFilter>("all")
  const [modelTypeFilter, setModelTypeFilter] = useState("all")
  const [schedulableFilter, setSchedulableFilter] = useState<"enabled" | "disabled" | "all">("all")
  const [groupFilter, setGroupFilter] = useState("all")
  const [selected, setSelected] = useState<number[]>([])
  const [batchMode, setBatchMode] = useState<BatchMode>("channel_group")
  const [batchChannelID, setBatchChannelID] = useState("")
  const [batchGroup, setBatchGroup] = useState("")
  const [batchMultiplier, setBatchMultiplier] = useState("")
  const [batchRuntimeValue, setBatchRuntimeValue] = useState("")
  const [batchModelType, setBatchModelType] = useState("")
  const [batchGroupDialogOpen, setBatchGroupDialogOpen] = useState(false)
  const [groupEditor, setGroupEditor] = useState<RelayAccountView | null>(null)
  const [schedulingAccountID, setSchedulingAccountID] = useState<number | null>(null)
  const [probingAccountID, setProbingAccountID] = useState<number | null>(null)
  const [testingAccountID, setTestingAccountID] = useState<number | null>(null)
  const [deletingAccountID, setDeletingAccountID] = useState<number | null>(null)
  const [testAccount, setTestAccount] = useState<RelayAccountView | null>(null)
  const [testModels, setTestModels] = useState<string[]>([])
  const [testModel, setTestModel] = useState("")
  const [testMode, setTestMode] = useState("regular")
  const [testModelsLoading, setTestModelsLoading] = useState(false)
  const [testResult, setTestResult] = useState<RelayAccountTestResult | null>(null)
  const [testError, setTestError] = useState("")
  const testOutput = useMemo(() => parseAccountTestOutput(testResult?.output ?? ""), [testResult?.output])
  const [testingGroupID, setTestingGroupID] = useState<number | null>(null)
  const [deletingGroupID, setDeletingGroupID] = useState<number | null>(null)
  const [testGroup, setTestGroup] = useState<RelayGroupView | null>(null)
  const [groupTestModels, setGroupTestModels] = useState<string[]>([])
  const [groupTestModel, setGroupTestModel] = useState("")
  const [groupTestCount, setGroupTestCount] = useState("1")
  const [groupTestModelsLoading, setGroupTestModelsLoading] = useState(false)
  const [groupTestResult, setGroupTestResult] = useState<RelayGroupTestResult | null>(null)
  const [groupTestError, setGroupTestError] = useState("")
  const [groupTestNeedsKey, setGroupTestNeedsKey] = useState(false)
  const [accountSort, setAccountSort] = useState<SortState<AccountSortKey>>(null)
  const [accountListOpen, setAccountListOpen] = usePersistedOpen("uh_relay_account_risk_open")
  const lastOverviewByStation = useRef(new Map<number, RelayOverview>())

  useEffect(() => { if (selectedID == null && stations.data?.[0]) setSelectedID(stations.data[0].id); if (selectedID != null && stations.data && !stations.data.some((station) => station.id === selectedID)) setSelectedID(stations.data[0]?.id ?? null) }, [stations.data, selectedID])
  useEffect(() => { if (!syncSettings.data) return; setAutoRateSyncEnabled(syncSettings.data.relay_rate_enabled); setAutoRateSyncInterval(syncSettings.data.relay_rate_interval_minutes || 60); setAutoSnapshotSyncEnabled(syncSettings.data.relay_snapshot_enabled); setAutoSnapshotSyncInterval(syncSettings.data.relay_snapshot_interval_seconds || (syncSettings.data.relay_snapshot_interval_minutes || 60) * 60) }, [syncSettings.data])
  useEffect(() => { if (!overview.data) return; setAutoAdjustEnabled(overview.data.station.auto_adjust_enabled); setAutoAdjustNoProfitEnabled(overview.data.station.auto_adjust_no_profit_enabled); setAutoPriorityEnabled(overview.data.station.auto_priority_enabled); setAutoPriorityRecallEnabled(overview.data.station.auto_priority_recall_enabled); setAutoPriorityRecallMinutes(overview.data.station.auto_priority_recall_minutes || 180) }, [overview.data])
  useEffect(() => { setSelected([]) }, [selectedID])
  useEffect(() => { if (selectedID != null && overview.data) lastOverviewByStation.current.set(selectedID, overview.data) }, [overview.data, selectedID])

  const baseOverview = overview.data ?? (selectedID == null ? null : lastOverviewByStation.current.get(selectedID) ?? null)
  const currentOverview = useMemo(() => {
    if (!baseOverview) return null
    const usageByAccount = new Map((usage.data?.accounts ?? []).map((account) => [account.external_id, account]))
    return {
      ...baseOverview,
      accounts: baseOverview.accounts.map((account) => {
        const stats = usageByAccount.get(account.external_id)
        return { ...account, usage_total_tokens: stats?.usage_total_tokens ?? null, user_charge_amount: stats?.user_charge_amount ?? null }
      }),
      range: usage.data?.range ?? usageRange,
      usage_complete: usage.data?.complete ?? false,
      usage_failed_accounts: usage.data?.failed_accounts ?? 0,
    }
  }, [baseOverview, usage.data, usageRange])
  const managedAccounts = useMemo(() => (currentOverview?.accounts ?? []).filter((account) => account.type?.trim().toLowerCase() !== "oauth"), [currentOverview])
  const managedAccountIDs = useMemo(() => new Set(managedAccounts.map((account) => account.external_id)), [managedAccounts])
  const usageTotals = useMemo(() => (usage.data?.accounts ?? []).filter((account) => managedAccountIDs.has(account.external_id)).reduce((totals, account) => ({
    tokens: totals.tokens + account.usage_total_tokens,
    charge: totals.charge + account.user_charge_amount,
  }), { tokens: 0, charge: 0 }), [managedAccountIDs, usage.data?.accounts])
  const selectedStation = useMemo(() => stations.data?.find((station) => station.id === selectedID) ?? null, [stations.data, selectedID])
  const groupAPIKeysURL = useMemo(() => {
    const baseURL = selectedStation?.base_url?.trim()
    if (!baseURL) return null
    try {
      return new URL("/keys", baseURL).toString()
    } catch {
      return null
    }
  }, [selectedStation?.base_url])
  const accountModelTypeOptions = useMemo(() => Array.from(new Set(managedAccounts.map((account) => account.model_type?.trim()).filter(Boolean) as string[])).sort((a, b) => a.localeCompare(b, "zh-CN")), [managedAccounts])
  const filteredAccounts = useMemo(() => {
    const accounts = managedAccounts.filter((account) => {
    if (accountNameFilter.trim() && !account.name.toLowerCase().includes(accountNameFilter.trim().toLowerCase())) return false
    if (modelTypeFilter === "__unassigned" && account.model_type?.trim()) return false
    if (modelTypeFilter !== "all" && modelTypeFilter !== "__unassigned" && account.model_type?.trim() !== modelTypeFilter) return false
    if (schedulableFilter === "enabled" && !account.schedulable) return false
    if (schedulableFilter === "disabled" && account.schedulable) return false
    if (riskFilter === "adjustable" && !account.can_apply) return false
    if (riskFilter === "downgradable" && accountDowngradeGroups(account).length === 0) return false
    if (riskFilter !== "all" && riskFilter !== "adjustable" && riskFilter !== "downgradable" && account.risk_state !== riskFilter) return false
    if (groupFilter !== "all" && !account.current_groups.some((group) => String(group.external_id) === groupFilter)) return false
    return true
    })
    if (!accountSort) return accounts
    const direction = accountSort.direction === "asc" ? 1 : -1
    return [...accounts].sort((a, b) => {
      let first: number | null
      let second: number | null
      if (accountSort.key === "latency") {
        first = latencySmoothnessScore(a)
        second = latencySmoothnessScore(b)
      } else if (accountSort.key === "priority") {
        first = a.priority
        second = b.priority
      } else if (accountSort.key === "cost") {
        first = a.cost_multiplier ?? null
        second = b.cost_multiplier ?? null
      } else {
        first = a.user_charge_amount ?? null
        second = b.user_charge_amount ?? null
      }
      if (first == null && second == null) return a.name.localeCompare(b.name, "zh-CN")
      if (first == null) return 1
      if (second == null) return -1
      const compared = (first - second) * direction
      if (compared !== 0) return compared
      if (accountSort.key === "usage") {
        const tokenCompared = ((a.usage_total_tokens ?? 0) - (b.usage_total_tokens ?? 0)) * direction
        if (tokenCompared !== 0) return tokenCompared
      }
      return a.name.localeCompare(b.name, "zh-CN")
    })
  }, [managedAccounts, accountNameFilter, modelTypeFilter, schedulableFilter, riskFilter, groupFilter, accountSort])
  const allFilteredSelected = filteredAccounts.length > 0 && filteredAccounts.every((account) => selected.includes(account.external_id))
  const filteredEnableCount = filteredAccounts.filter((account) => !account.schedulable).length
  const filteredDisableCount = filteredAccounts.filter((account) => account.schedulable).length
  const filteredSuggestionCount = filteredAccounts.filter((account) => account.can_apply).length
  const filteredDowngradeCount = filteredAccounts.filter((account) => accountDowngradeGroups(account).length > 0).length
  const batchGroups = currentOverview?.monitor_channels.find((channel) => String(channel.id) === batchChannelID)?.groups ?? []
  const modelTypeOptions = useMemo(() => Array.from(new Set((currentOverview?.groups ?? []).flatMap((group) => group.model_types ?? []))).sort(), [currentOverview?.groups])

  async function reload() { await Promise.all([stations.refetch(), overview.refetch(), usage.refetch(), recentUsage.refetch()]) }
  function openCreate() { setEditingID(null); setForm(emptyForm); setShowForm(true) }
  function openEdit() { if (selectedStation) { setEditingID(selectedStation.id); setForm({ name: selectedStation.name, base_url: selectedStation.base_url, api_key: "" }); setShowForm(true) } }
  async function saveStation(event: React.FormEvent) { event.preventDefault(); setBusy(true); try { const payload = editingID == null ? form : { name: form.name, base_url: form.base_url, ...(form.api_key ? { api_key: form.api_key } : {}) }; const station = await apiFetch<RelayStation>(editingID == null ? "/relay-stations" : `/relay-stations/${editingID}`, { method: editingID == null ? "POST" : "PUT", body: JSON.stringify(payload) }); setShowForm(false); setSelectedID(station.id); await reload(); toast.success(editingID == null ? "中转站已添加" : "中转站配置已更新") } catch (error) { toast.error(error instanceof Error ? error.message : "保存失败") } finally { setBusy(false) } }
  async function sync() { if (!selectedID) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}/sync`, { method: "POST" }); await reload(); toast.success("已实时探测 API Key 成本并刷新账号快照") } catch (error) { toast.error(error instanceof Error ? error.message : "同步失败"); await reload() } finally { setBusy(false) } }
  async function syncAll() { setBusy(true); try { const result = await apiFetch<{ synced: number; failed: number }>("/relay-stations/sync-all", { method: "POST" }); await reload(); toast.success(`已同步 ${result.synced} 个中转站${result.failed ? `，${result.failed} 个失败` : ""}`) } catch (error) { toast.error(error instanceof Error ? error.message : "同步失败") } finally { setBusy(false) } }
  async function saveAutoSync() { if (!syncSettings.data) return; setBusy(true); try { await apiFetch("/sync-settings", { method: "PUT", body: JSON.stringify({ channel_enabled: syncSettings.data.channel_enabled, channel_interval_minutes: syncSettings.data.channel_interval_minutes, relay_rate_enabled: autoRateSyncEnabled, relay_rate_interval_minutes: autoRateSyncInterval, relay_snapshot_enabled: autoSnapshotSyncEnabled, relay_snapshot_interval_minutes: Math.ceil(autoSnapshotSyncInterval / 60), relay_snapshot_interval_seconds: autoSnapshotSyncInterval }) }); await syncSettings.refetch(); toast.success("中转站同步计划已保存") } catch (error) { toast.error(error instanceof Error ? error.message : "保存失败") } finally { setBusy(false) } }
  function resetFilters() { setAccountNameFilter(""); setModelTypeFilter("all"); setSchedulableFilter("all"); setRiskFilter("all"); setGroupFilter("all"); setAccountSort(null) }
  function toggleAccountSort(key: AccountSortKey, initialDirection: SortDirection) { setAccountSort((current) => current?.key === key ? { key, direction: current.direction === "asc" ? "desc" : "asc" } : { key, direction: initialDirection }) }
  async function savePolicy() { if (!selectedID) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}`, { method: "PUT", body: JSON.stringify({ auto_adjust_enabled: autoAdjustEnabled, auto_adjust_no_profit_enabled: autoAdjustNoProfitEnabled }) }); await reload(); toast.success("自动调组策略已保存") } catch (error) { toast.error(error instanceof Error ? error.message : "保存失败") } finally { setBusy(false) } }
  async function savePriorityPolicy() { if (!selectedID) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}`, { method: "PUT", body: JSON.stringify({ auto_priority_enabled: autoPriorityEnabled, auto_priority_recall_enabled: autoPriorityRecallEnabled, auto_priority_recall_minutes: autoPriorityRecallMinutes }) }); await reload(); toast.success("自动优先级策略已保存") } catch (error) { toast.error(error instanceof Error ? error.message : "保存失败") } finally { setBusy(false) } }
  async function updateGroup(groupID: number, patch: RelayGroupPatch) { if (!selectedID) return; await apiFetch(`/relay-stations/${selectedID}/groups/${groupID}`, { method: "PUT", body: JSON.stringify(patch) }); await reload(); toast.success("分组已更新") }
  async function updateGroupOrder(ids: number[]) { if (!selectedID) return; await apiFetch(`/relay-stations/${selectedID}/groups/sort-order`, { method: "PUT", body: JSON.stringify({ updates: ids.map((id, index) => ({ id, sort_order: index * 10 })) }) }); await reload(); toast.success("分组排序已保存") }
  async function applySuggestion(account: RelayAccountView) { if (!selectedID || !account.recommended_group) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}/accounts/${account.external_id}/apply-suggestion`, { method: "POST" }); await reload(); toast.success("账号分组已更新并写入审计记录") } catch (error) { toast.error(error instanceof Error ? error.message : "应用建议失败") } finally { setBusy(false) } }
  async function addDowngradeGroups(account: RelayAccountView) { const groups = accountDowngradeGroups(account); if (!selectedID || groups.length === 0) return; setBusy(true); try { const result = await apiFetch<RelayAccountBatchActionResult>(`/relay-stations/${selectedID}/accounts/add-downgrades`, { method: "POST", body: JSON.stringify({ account_external_ids: [account.external_id] }) }); if (result.failed) throw new Error(result.errors?.[0] || "加入可降级分组失败"); await reload(); toast.success(`已加入 ${groups.length} 个可降级分组：${groups.map((group) => group.name).join("、")}；原分组保持不变`) } catch (error) { toast.error(error instanceof Error ? error.message : "加入可降级分组失败") } finally { setBusy(false) } }
  async function saveGroups(ids: number[]) { if (!selectedID || !groupEditor) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}/accounts/${groupEditor.external_id}/groups`, { method: "PUT", body: JSON.stringify({ group_external_ids: ids }) }); setGroupEditor(null); await reload(); toast.success("账号分组已手动更新") } catch (error) { toast.error(error instanceof Error ? error.message : "更新分组失败") } finally { setBusy(false) } }
  async function setSchedulable(account: RelayAccountView, schedulable: boolean) { if (!selectedID) return; setSchedulingAccountID(account.external_id); try { await apiFetch(`/relay-stations/${selectedID}/accounts/${account.external_id}/schedulable`, { method: "PUT", body: JSON.stringify({ schedulable }) }); await reload(); toast.success(schedulable ? "账号调度已开启" : "账号调度已关闭") } catch (error) { toast.error(error instanceof Error ? error.message : "更新调度状态失败") } finally { setSchedulingAccountID(null) } }
  async function probeAccount(account: RelayAccountView) { if (!selectedID) return; setProbingAccountID(account.external_id); try { await apiFetch(`/relay-stations/${selectedID}/accounts/${account.external_id}/probe`, { method: "POST" }); await reload(); toast.success(account.cost_override_mode ? "上游倍率已探测，当前手工成本覆盖继续优先" : "上游倍率已立即探测并刷新") } catch (error) { toast.error(error instanceof Error ? error.message : "上游倍率探测失败") } finally { setProbingAccountID(null) } }
  async function openAccountTest(account: RelayAccountView) { if (!selectedID) return; setTestAccount(account); setTestModels([]); setTestModel(""); setTestMode("regular"); setTestResult(null); setTestError(""); setTestModelsLoading(true); try { const models = await apiFetch<string[]>(`/relay-stations/${selectedID}/accounts/${account.external_id}/models`); setTestModels(models); setTestModel(models[0] ?? "") } catch (error) { setTestError(error instanceof Error ? error.message : "读取测试模型失败") } finally { setTestModelsLoading(false) } }
  async function runAccountTest() { if (!selectedID || !testAccount || !testModel) return; setTestResult(null); setTestError(""); setTestingAccountID(testAccount.external_id); try { const result = await apiFetch<RelayAccountTestResult>(`/relay-stations/${selectedID}/accounts/${testAccount.external_id}/test`, { method: "POST", body: JSON.stringify({ model_id: testModel, mode: testMode }) }); setTestResult(result); toast.success("账号连接测试成功") } catch (error) { setTestError(error instanceof Error ? error.message : "账号连接测试失败") } finally { setTestingAccountID(null) } }
  async function openGroupTest(group: RelayGroupView) { if (!selectedID) return; setTestGroup(group); setGroupTestModels([]); setGroupTestModel(""); setGroupTestCount("1"); setGroupTestResult(null); setGroupTestError(""); setGroupTestNeedsKey(false); setGroupTestModelsLoading(true); try { const models = await apiFetch<string[]>(`/relay-stations/${selectedID}/groups/${group.external_id}/models`); setGroupTestModels(models); setGroupTestModel(models[0] ?? "") } catch (error) { setGroupTestNeedsKey(isMissingGroupAPIKeyError(error)); setGroupTestError(error instanceof Error ? error.message : "读取分组模型失败") } finally { setGroupTestModelsLoading(false) } }
  async function runGroupTest() { if (!selectedID || !testGroup || !groupTestModel) return; const count = Number(groupTestCount); if (!Number.isInteger(count) || count < 1 || count > 10) { setGroupTestError("调用次数必须是 1 到 10 次"); return } setGroupTestResult(null); setGroupTestError(""); setGroupTestNeedsKey(false); setTestingGroupID(testGroup.external_id); try { const result = await apiFetch<RelayGroupTestResult>(`/relay-stations/${selectedID}/groups/${testGroup.external_id}/test`, { method: "POST", body: JSON.stringify({ model: groupTestModel, count }) }); setGroupTestResult(result); await recentUsage.refetch(); if (result.failed === 0) toast.success(`${result.succeeded} 次真实调用均成功`); else toast.error(`测试完成：成功 ${result.succeeded} 次，失败 ${result.failed} 次`) } catch (error) { setGroupTestNeedsKey(isMissingGroupAPIKeyError(error)); setGroupTestError(error instanceof Error ? error.message : "分组快速测试失败") } finally { setTestingGroupID(null) } }
  async function deleteGroup(group: RelayGroupView) { if (!selectedID) return; const accepted = await confirm({ title: `删除分组“${group.name}”？`, description: `将从当前中转站真实删除该分组，操作不可恢复。${group.account_count > 0 ? `当前关联 ${group.account_count} 个账号，删除后这些账号的分组配置可能受到影响。` : ""}`, confirmLabel: "确认删除", destructive: true }); if (!accepted) return; setDeletingGroupID(group.external_id); try { await apiFetch(`/relay-stations/${selectedID}/groups/${group.external_id}`, { method: "DELETE" }); if (testGroup?.external_id === group.external_id) setTestGroup(null); await reload(); toast.success(`分组“${group.name}”已删除`) } catch (error) { toast.error(error instanceof Error ? error.message : "删除分组失败") } finally { setDeletingGroupID(null) } }
  async function deleteAccount(account: RelayAccountView) { if (!selectedID) return; const accepted = await confirm({ title: `删除账号“${account.name}”？`, description: "将从当前中转站真实删除该账号及其调度配置，操作不可恢复；历史使用记录不会被删除。", confirmLabel: "确认删除", destructive: true }); if (!accepted) return; setDeletingAccountID(account.external_id); try { await apiFetch(`/relay-stations/${selectedID}/accounts/${account.external_id}`, { method: "DELETE" }); setSelected((current) => current.filter((id) => id !== account.external_id)); if (groupEditor?.external_id === account.external_id) setGroupEditor(null); if (testAccount?.external_id === account.external_id) setTestAccount(null); await reload(); toast.success(`账号“${account.name}”已删除`) } catch (error) { toast.error(error instanceof Error ? error.message : "删除账号失败") } finally { setDeletingAccountID(null) } }
  async function refreshAccounts() { await Promise.all([overview.refetch(), usage.refetch()]); toast.success("账号列表已刷新") }
  async function refreshGroups() { await overview.refetch(); toast.success("分组列表已刷新") }
  async function saveBatchOverride(clear = false) { if (!selectedID || selected.length === 0) return; if (batchMode === "groups") { setBatchGroupDialogOpen(true); return } if (!clear && batchMode === "channel_group" && (!batchChannelID || !batchGroup)) { toast.error("请选择渠道和渠道分组"); return } if (!clear && batchMode === "manual" && (!batchMultiplier || !Number.isFinite(Number(batchMultiplier)) || Number(batchMultiplier) < 0)) { toast.error("手工倍率必须是非负数"); return } setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}/accounts/cost-overrides`, { method: "PUT", body: JSON.stringify({ account_external_ids: selected, clear, mode: batchMode, monitor_channel_id: batchChannelID ? Number(batchChannelID) : undefined, upstream_group: batchGroup, manual_multiplier: batchMode === "manual" ? Number(batchMultiplier) : undefined }) }); setSelected([]); await reload(); toast.success(clear ? "已清除账号成本覆盖" : "账号成本覆盖已保存") } catch (error) { toast.error(error instanceof Error ? error.message : "保存成本覆盖失败") } finally { setBusy(false) } }
  async function saveBatchRuntimeSettings() { if (!selectedID || selected.length === 0 || (batchMode !== "concurrency" && batchMode !== "priority" && batchMode !== "retry_count")) return; const value = Number(batchRuntimeValue); if (batchMode === "retry_count") { if (!Number.isInteger(value) || value < 0 || value > 10) { toast.error("同账号重试次数必须是 0 到 10 的整数"); return } const selectedAccounts = currentOverview?.accounts.filter((account) => selected.includes(account.external_id)) ?? []; const unsupported = selectedAccounts.find((account) => !["apikey", "bedrock"].includes((account.type || "").toLowerCase())); if (unsupported) { toast.error(`账号“${unsupported.name}”不支持同账号重试次数，仅支持 API Key 或 Bedrock 账号`); return } const poolModeOff = selectedAccounts.find((account) => account.pool_mode !== true); if (poolModeOff) { toast.error(`账号“${poolModeOff.name}”未开启池模式，请先在 Sub2API 账号编辑中开启池模式`); return } } else { if (!Number.isInteger(value) || value < 1 || value > 1000) { toast.error(`${batchMode === "concurrency" ? "并发数" : "优先级"}必须是 1 到 1000 的整数`); return } if (batchMode === "priority" && value === 1 && currentOverview?.accounts.some((account) => selected.includes(account.external_id) && account.type?.toLowerCase() !== "oauth")) { toast.error("优先级 1 仅保留给 OAuth 账号"); return } } setBusy(true); try { const field = batchMode === "retry_count" ? "pool_mode_retry_count" : batchMode; await apiFetch(`/relay-stations/${selectedID}/accounts/runtime-settings`, { method: "PUT", body: JSON.stringify({ account_external_ids: selected, [field]: value }) }); setSelected([]); setBatchRuntimeValue(""); await reload(); toast.success(`已批量设置账号${batchMode === "concurrency" ? "并发数" : batchMode === "priority" ? "优先级" : "同账号重试次数"}`) } catch (error) { toast.error(error instanceof Error ? error.message : "批量设置失败") } finally { setBusy(false) } }
  async function saveBatchModelType() { if (!selectedID || selected.length === 0) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}/accounts/model-types`, { method: "PUT", body: JSON.stringify({ account_external_ids: selected, model_type: batchModelType }) }); setSelected([]); setBatchModelType(""); await reload(); toast.success("已批量设置账号模型类型") } catch (error) { toast.error(error instanceof Error ? error.message : "批量设置模型类型失败") } finally { setBusy(false) } }
  async function saveBatchGroups(ids: number[]) { if (!selectedID || selected.length === 0) return; if (ids.length === 0) { toast.error("至少选择一个销售分组"); return } setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}/accounts/groups`, { method: "PUT", body: JSON.stringify({ account_external_ids: selected, group_external_ids: ids }) }); setBatchGroupDialogOpen(false); setSelected([]); await reload(); toast.success("已批量调整账号销售分组") } catch (error) { toast.error(error instanceof Error ? error.message : "批量调整分组失败") } finally { setBusy(false) } }
  async function runFilteredAccountAction(path: string, schedulable: boolean | undefined, successLabel: string) { if (!selectedID || filteredAccounts.length === 0) { toast.info("当前筛选没有账号"); return } setBusy(true); try { const result = await apiFetch<RelayAccountBatchActionResult>(`/relay-stations/${selectedID}/accounts/${path}`, { method: path === "schedulable" ? "PUT" : "POST", body: JSON.stringify({ account_external_ids: filteredAccounts.map((account) => account.external_id), ...(path === "schedulable" ? { schedulable } : {}) }) }); setSelected([]); await reload(); const detail = result.failed ? `，失败 ${result.failed} 个${result.errors?.[0] ? `：${result.errors[0]}` : ""}` : ""; const message = `${successLabel}：已处理 ${result.applied} 个，跳过 ${result.skipped} 个${detail}`; if (result.failed) toast.warning(message); else toast.success(message) } catch (error) { toast.error(error instanceof Error ? error.message : `${successLabel}失败`) } finally { setBusy(false) } }
  async function remove() { if (!selectedID || !selectedStation) return; const accepted = await confirm({ title: `删除中转站“${selectedStation.name}”？`, description: "将从系统真实删除该中转站及其账号快照、调整记录，操作不可恢复；历史使用记录不会被删除。", confirmLabel: "确认删除", destructive: true }); if (!accepted) return; setBusy(true); try { await apiFetch(`/relay-stations/${selectedID}`, { method: "DELETE" }); setSelectedID(null); await reload(); toast.success("中转站已删除") } catch (error) { toast.error(error instanceof Error ? error.message : "删除失败") } finally { setBusy(false) } }

  return <section className="space-y-4"><header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3"><div><h1 className="flex items-center gap-2 text-xl font-bold text-foreground"><Server className="size-5 text-brand" />中转站管理</h1><p className="mt-1 text-xs text-muted-foreground">同步时实时探测 API Key 上游倍率，并按平台、账号类型和模型类型评估销售分组与降级候选。</p></div><div className="flex flex-wrap items-center gap-2"><Button variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9" onClick={openCreate}><Plus className="size-3.5" />添加中转站</Button>{stations.data?.length ? <Button variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9" disabled={busy} onClick={() => void syncAll()}><RefreshCw className={cn("size-3.5", busy && "animate-spin")} />同步全部</Button> : null}{selectedStation ? <><Button asChild variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9"><a href={`/public/relay-monitor/${selectedStation.id}`} target="_blank" rel="noopener noreferrer"><Activity className="size-3.5" />分组监控</a></Button><Button variant="outline" size="sm" className="h-11 sm:h-9" onClick={openEdit}>编辑</Button><Button size="sm" className="h-11 gap-1.5 sm:h-9" disabled={busy || !selectedStation.api_key_configured} onClick={() => void sync()}><RefreshCw className={cn("size-3.5", busy && "animate-spin")} />实时同步</Button><Button variant="ghost" size="icon" className="size-11 sm:size-9" aria-label="删除中转站" disabled={busy} onClick={() => void remove()}><Trash2 className="size-4 text-danger" /></Button></> : null}</div></header>

    {showForm ? <form onSubmit={saveStation} autoComplete="off" className="grid gap-3 border border-border bg-card p-4 md:grid-cols-[1fr_1.3fr_1.5fr_auto] md:items-end"><div className="space-y-1.5"><Label htmlFor="relay-name">名称</Label><Input id="relay-name" name="gatewayops-relay-name" autoComplete="off" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required /></div><div className="space-y-1.5"><Label htmlFor="relay-url">中转站地址</Label><Input id="relay-url" name="gatewayops-relay-base-url" autoComplete="off" type="url" value={form.base_url} onChange={(event) => setForm({ ...form, base_url: event.target.value })} required /></div><div className="space-y-1.5"><Label htmlFor="relay-key">管理员 API Key</Label><Input id="relay-key" name="gatewayops-relay-admin-key" type="password" autoComplete="new-password" data-lpignore="true" value={form.api_key} onChange={(event) => setForm({ ...form, api_key: event.target.value })} placeholder={editingID == null ? "粘贴 x-api-key" : "留空表示不修改"} required={editingID == null} /></div><div className="flex gap-2"><Button type="submit" disabled={busy}>保存</Button><Button type="button" variant="outline" onClick={() => setShowForm(false)}>取消</Button></div></form> : null}

    {stations.loading && !stations.data ? <div className="py-10 text-center text-sm text-muted-foreground">正在读取中转站配置...</div> : stations.data?.length ? <div><div className="mb-3 flex max-w-full items-center gap-2 overflow-x-auto border-b border-border pb-1 [scrollbar-width:thin]">{stations.data.map((station) => <button key={station.id} type="button" onClick={() => setSelectedID(station.id)} title={`${station.name} · ${station.base_url}`} className={cn("inline-flex shrink-0 cursor-pointer items-center gap-2 border-b-2 px-3 py-2 text-left text-sm transition-colors duration-200", selectedID === station.id ? "border-foreground font-medium text-foreground" : "border-transparent text-muted-foreground hover:border-border hover:text-foreground")}><span className="max-w-48 truncate">{station.name}</span><span className={cn("size-1.5 rounded-full", station.last_error ? "bg-danger" : station.api_key_configured ? "bg-success" : "bg-warning")} /></button>)}</div><div className="min-w-0">{overview.loading && !currentOverview ? <div className="py-10 text-center text-sm text-muted-foreground">正在读取账号快照...</div> : currentOverview ? <div className="space-y-3">{overview.error ? <Alert variant="destructive"><CircleAlert /><AlertTitle>刷新失败，正在显示上次数据</AlertTitle><AlertDescription>{overview.error}</AlertDescription></Alert> : null}{currentOverview.station.last_error ? <Alert variant="destructive"><CircleAlert /><AlertTitle>最近同步失败</AlertTitle><AlertDescription>{currentOverview.station.last_error}</AlertDescription></Alert> : null}{!currentOverview.station.last_synced_at ? <Alert><CircleAlert /><AlertTitle>还没有账号快照</AlertTitle><AlertDescription>实时同步后将读取远端账号、成本倍率与分组关联。</AlertDescription></Alert> : null}<RiskSummary overview={currentOverview} />

      <div className="grid gap-3 xl:grid-cols-3">
        <Card className="gap-0 border border-border py-0 shadow-none">
          <CardHeader className="flex flex-row items-center justify-between gap-3 px-4 py-3">
            <div className="min-w-0"><CardTitle className="flex items-center gap-2 text-sm font-semibold"><Clock3 className="size-4 text-brand" />同步计划</CardTitle><p className="mt-1 truncate text-xs text-muted-foreground">应用于全部中转站，倍率探测与快照刷新独立执行。</p></div>
            <Button size="sm" className="h-9 shrink-0 gap-1.5" disabled={busy || !syncSettings.data} onClick={() => void saveAutoSync()}><Save className="size-3.5" />保存</Button>
          </CardHeader>
          <CardContent className="grid divide-y divide-border border-t border-border px-4 md:grid-cols-2 md:divide-x md:divide-y-0">
            <div className="grid min-h-11 grid-cols-[auto_minmax(0,1fr)_6rem] items-center gap-3 py-4 md:pr-6"><Switch checked={autoRateSyncEnabled} onCheckedChange={setAutoRateSyncEnabled} disabled={!syncSettings.data} aria-label="启用倍率探测" /><div className="min-w-0"><p className="text-xs font-medium">倍率探测</p><p className="mt-1 flex items-center gap-1 text-[11px] leading-4 text-muted-foreground"><span className="truncate">读取上游成本倍率</span><SyncPlanHelp label="倍率探测" help={rateSyncHelp} /></p></div><Select value={String(autoRateSyncInterval)} onValueChange={(value) => setAutoRateSyncInterval(Number(value))} disabled={!autoRateSyncEnabled}><SelectTrigger className="h-9 w-24 text-xs"><SelectValue /></SelectTrigger><SelectContent>{rateIntervals.map((value) => <SelectItem key={value} value={String(value)}>{intervalLabel(value)}</SelectItem>)}</SelectContent></Select></div>
            <div className="grid min-h-11 grid-cols-[auto_minmax(0,1fr)_6rem] items-center gap-3 py-4 md:pl-6"><Switch checked={autoSnapshotSyncEnabled} onCheckedChange={setAutoSnapshotSyncEnabled} disabled={!syncSettings.data} aria-label="启用快照同步" /><div className="min-w-0"><p className="text-xs font-medium">快照同步</p><p className="mt-1 flex items-center gap-1 text-[11px] leading-4 text-muted-foreground"><span className="truncate">刷新账号和分组快照</span><SyncPlanHelp label="快照同步" help={snapshotSyncHelp} /></p></div><Select value={String(autoSnapshotSyncInterval)} onValueChange={(value) => setAutoSnapshotSyncInterval(Number(value))} disabled={!autoSnapshotSyncEnabled}><SelectTrigger className="h-9 w-24 text-xs"><SelectValue /></SelectTrigger><SelectContent>{snapshotIntervals.map((value) => <SelectItem key={value} value={String(value)}>{snapshotIntervalLabel(value)}</SelectItem>)}</SelectContent></Select></div>
          </CardContent>
        </Card>
        <Card className="gap-0 border border-border py-0 shadow-none">
          <CardHeader className="flex flex-row items-center justify-between gap-3 px-4 py-3">
            <div className="min-w-0"><CardTitle className="flex items-center gap-2 text-sm font-semibold"><Cog className="size-4 text-brand" />自动调组策略</CardTitle><p className="mt-1 truncate text-xs text-muted-foreground">分别控制亏损风险和无利润账号的自动处理。</p></div>
            <Button size="sm" className="h-9 shrink-0 gap-1.5" disabled={busy} onClick={() => void savePolicy()}><Save className="size-3.5" />保存</Button>
          </CardHeader>
          <CardContent className="grid divide-y divide-border border-t border-border px-4 md:grid-cols-2 md:divide-x md:divide-y-0">
            <div className="flex min-h-11 items-center justify-between gap-6 py-4 md:pr-6"><div className="min-w-0"><p className="truncate text-xs font-medium">亏损风险</p><p className="mt-1 truncate text-[11px] text-muted-foreground">成本高于销售倍率时调整</p></div><Switch className="relative shrink-0 cursor-pointer before:absolute before:-inset-3 before:content-['']" checked={autoAdjustEnabled} onCheckedChange={setAutoAdjustEnabled} aria-label="自动处理亏损风险" /></div>
            <div className="flex min-h-11 items-center justify-between gap-6 py-4 md:pl-6"><div className="min-w-0"><p className="truncate text-xs font-medium">无利润分组</p><p className="mt-1 truncate text-[11px] text-muted-foreground">销售倍率与成本完全相同时调整</p></div><Switch className="relative shrink-0 cursor-pointer before:absolute before:-inset-3 before:content-['']" checked={autoAdjustNoProfitEnabled} onCheckedChange={setAutoAdjustNoProfitEnabled} aria-label="自动处理无利润分组" /></div>
          </CardContent>
        </Card>
        <Card className="gap-0 border border-border py-0 shadow-none">
          <CardHeader className="flex flex-row items-center justify-between gap-3 px-4 py-3">
            <div className="min-w-0"><CardTitle className="flex items-center gap-2 text-sm font-semibold"><SlidersHorizontal className="size-4 text-brand" />自动优先级</CardTitle><p className="mt-1 truncate text-xs text-muted-foreground">按最近调用流畅度自动分配调度优先级。</p></div>
            <Button size="sm" className="h-9 shrink-0 gap-1.5" disabled={busy} onClick={() => void savePriorityPolicy()}><Save className="size-3.5" />保存</Button>
          </CardHeader>
          <CardContent className="grid divide-y divide-border border-t border-border px-4 md:grid-cols-2 md:divide-x md:divide-y-0">
            <div className="flex min-h-11 items-center justify-between gap-4 py-4 md:pr-6"><div className="min-w-0"><p className="whitespace-nowrap text-xs font-medium">流畅度排序</p><p className="mt-1 flex items-center gap-1 truncate text-[11px] text-muted-foreground">按近期响应排序<Tooltip delayDuration={200}><TooltipTrigger asChild><button type="button" className="shrink-0 rounded text-muted-foreground hover:text-foreground" aria-label="查看流畅度排序说明"><CircleHelp className="size-3.5" /></button></TooltipTrigger><TooltipContent side="top" className="max-w-72 text-xs">OAuth 账号固定为优先级 1；未调用及响应最流畅的 API Key 从优先级 2 开始，按最近调用的首字与总耗时综合排序，最慢逐级排到 10。</TooltipContent></Tooltip></p></div><Switch className="relative shrink-0 cursor-pointer before:absolute before:-inset-3 before:content-['']" checked={autoPriorityEnabled} onCheckedChange={setAutoPriorityEnabled} aria-label="按最近调用流畅度自动调整优先级" /></div>
            <div className="grid min-h-11 grid-cols-[auto_minmax(0,1fr)_5.5rem] items-center gap-2 py-4 md:pl-6"><Switch className="shrink-0" checked={autoPriorityRecallEnabled} onCheckedChange={setAutoPriorityRecallEnabled} disabled={!autoPriorityEnabled} aria-label="启用自动回调优先级" /><div className="min-w-0"><p className="whitespace-nowrap text-xs font-medium">自动回调优先级</p><p className="mt-1 flex items-center gap-1 truncate text-[11px] text-muted-foreground">超时逐步提升<Tooltip delayDuration={200}><TooltipTrigger asChild><button type="button" className="shrink-0 rounded text-muted-foreground hover:text-foreground" aria-label="查看自动回调优先级说明"><CircleHelp className="size-3.5" /></button></TooltipTrigger><TooltipContent side="top" className="max-w-72 text-xs">已启用调度的 API Key 账号若在设定时间内未被调用，每次快照同步会将当前优先级提高一级，最低提升到按流畅度计算出的基础优先级 2；账号重新产生调用后继续按近期表现排序。</TooltipContent></Tooltip></p></div><Select value={String(autoPriorityRecallMinutes)} onValueChange={(value) => setAutoPriorityRecallMinutes(Number(value))} disabled={!autoPriorityEnabled || !autoPriorityRecallEnabled}><SelectTrigger className="h-9 w-[88px] px-2 text-xs"><SelectValue /></SelectTrigger><SelectContent>{[[30,"30 分"],[60,"1 小时"],[180,"3 小时"],[360,"6 小时"],[720,"12 小时"],[1440,"1 天"]].map(([value,label]) => <SelectItem key={value} value={String(value)}>{label}</SelectItem>)}</SelectContent></Select></div>
          </CardContent>
        </Card>
      </div>

      <div className="flex max-w-full items-center gap-1 overflow-x-auto border-b border-border pb-1" role="tablist" aria-label="中转站详情模块">
        {([
          ["accounts", ShieldAlert, "账号列表"],
          ["usage", History, "最近使用记录"],
          ["users", Users, "用户管理"],
          ["groups", Layers3, "分组管理"],
        ] as const).map(([value, Icon, label]) => (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={detailModule === value}
            onClick={() => setDetailModule(value)}
            className={cn(
              "inline-flex h-10 shrink-0 items-center gap-1.5 border-b-2 px-3 text-xs transition-colors sm:h-9",
              detailModule === value ? "border-foreground font-medium text-foreground" : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
            )}
          >
            <Icon className="size-3.5" />
            {label}
          </button>
        ))}
      </div>

      {detailModule === "usage" ? <RecentUsageTable rows={recentUsage.data ?? []} loading={recentUsage.loading} refreshing={recentUsage.refreshing} error={recentUsage.error} onRefresh={recentUsage.refetch} groupNames={new Map(currentOverview.groups.map((group) => [group.external_id, group.name]))} accountNames={new Map(currentOverview.accounts.map((account) => [account.external_id, account.name]))} accountURLs={new Map(currentOverview.accounts.filter((account) => account.base_url).map((account) => [account.external_id, account.base_url!] as const))} systemCapacity={{ current: currentOverview.accounts.reduce((sum, account) => sum + account.current_concurrency, 0), total: currentOverview.accounts.reduce((sum, account) => sum + account.concurrency, 0) }} onUserClick={(userID, userEmail, userName) => setHistoryUser({ id: userID, email: userEmail, name: userName })} /> : null}
      <UserBalanceHistoryDialog open={historyUser != null} userEmail={historyUser?.email ?? ""} userName={historyUser?.name ?? ""} data={userHistory.data} loading={userHistory.loading} error={userHistory.error} onClose={() => setHistoryUser(null)} />

      {detailModule === "users" ? <UserManagement stationID={selectedID!} /> : null}

      {detailModule === "groups" ? <GroupManagement overview={currentOverview} busy={busy} refreshing={overview.refreshing} testingGroupID={testingGroupID} deletingGroupID={deletingGroupID} onUpdate={updateGroup} onOrderChange={updateGroupOrder} onTest={(group) => void openGroupTest(group)} onDelete={(group) => void deleteGroup(group)} onRefresh={() => void refreshGroups()} /> : null}

      {detailModule === "accounts" ? <Card className="gap-0 border border-border py-0 shadow-none">
        <CardHeader className="gap-3 px-4 py-3">
          <div className="flex items-center justify-between gap-3"><div className="min-w-0"><CardTitle className="flex items-center gap-2 text-sm font-semibold"><ShieldAlert className="size-4 text-brand" />账号列表</CardTitle>{accountListOpen ? <p className="mt-1 text-xs text-muted-foreground">展示非 OAuth 账号的调度、成本、销售分组与风险；成本覆盖优先于实时探测。</p> : null}</div><div className="flex min-w-0 flex-1 flex-wrap items-center justify-end gap-2">{accountListOpen ? <>{usage.loading ? <span className="flex items-center gap-1 text-xs text-muted-foreground" aria-live="polite"><RefreshCw className="size-3 animate-spin" />消费统计读取中</span> : usage.error ? <span className="max-w-[min(34rem,50vw)] text-right text-xs leading-5 text-danger" title={usage.error}>消费统计读取失败</span> : usage.data && !usage.data.complete ? <span className="max-w-[min(38rem,50vw)] text-right text-xs leading-5 text-warning">有 {usage.data.failed_accounts} 个账号的区间消费读取失败，账号卡片中的消费数据可能不完整。</span> : null}<span className="shrink-0 text-xs text-muted-foreground">{filteredAccounts.length} / {managedAccounts.length} 个</span></> : null}<Button type="button" variant="ghost" size="icon" className="size-9 shrink-0" aria-label={accountListOpen ? "收起账号列表" : "展开账号列表"} aria-expanded={accountListOpen} onClick={() => setAccountListOpen((value) => !value)}><ChevronDown className={cn("size-4 transition-transform duration-200", accountListOpen && "rotate-180")} /></Button></div></div>
          {accountListOpen ? <div className="flex flex-wrap items-center justify-end gap-2"><div className="mr-auto flex flex-wrap items-center gap-4" aria-live="polite"><div className="flex min-w-32 items-center gap-2"><span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-amber-400/15 text-amber-600 dark:text-amber-400"><Box className="size-4" /></span><div><p className="text-[10px] text-muted-foreground">总 Token</p><p className="font-mono text-sm font-semibold tabular-nums text-foreground">{usage.loading && !usage.data ? "读取中…" : compactNumber.format(usageTotals.tokens)}</p></div></div><div className="flex min-w-32 items-center gap-2"><span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-success/10 text-success"><CircleDollarSign className="size-4" /></span><div><p className="text-[10px] text-muted-foreground">总消费</p><p className="font-mono text-sm font-semibold tabular-nums text-success">{usage.loading && !usage.data ? "读取中…" : chargeAmount(usageTotals.charge)}</p></div></div></div><div className="relative"><Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={accountNameFilter} onChange={(event) => setAccountNameFilter(event.target.value)} placeholder="输入账号名称" aria-label="按账号名称筛选" className="h-11 w-44 pl-8 text-xs sm:h-9 sm:w-40" /></div><Select value={usageRange} onValueChange={(value) => setUsageRange(value as RelayUsageRange)}><SelectTrigger className="h-11 w-24 text-xs sm:h-9" aria-label="账号消费统计时间范围"><SelectValue /></SelectTrigger><SelectContent>{usageRanges.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select><Select value={modelTypeFilter} onValueChange={setModelTypeFilter}><SelectTrigger className="h-11 w-32 text-xs sm:h-9" aria-label="按模型类型筛选"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部模型类型</SelectItem><SelectItem value="__unassigned">未绑定模型类型</SelectItem>{accountModelTypeOptions.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select><Select value={schedulableFilter} onValueChange={(value) => setSchedulableFilter(value as "enabled" | "disabled" | "all")}><SelectTrigger className="h-11 w-28 text-xs sm:h-9"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="enabled">调度开启</SelectItem><SelectItem value="disabled">调度关闭</SelectItem><SelectItem value="all">全部调度</SelectItem></SelectContent></Select><Select value={riskFilter} onValueChange={(value) => setRiskFilter(value as RiskFilter)}><SelectTrigger className="h-11 w-28 text-xs sm:h-9" aria-label="按账号状态筛选"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部账号</SelectItem><SelectItem value="adjustable">可调组</SelectItem><SelectItem value="downgradable">可降级</SelectItem>{Object.entries(stateMeta).map(([value, meta]) => <SelectItem key={value} value={value}>{meta.label}</SelectItem>)}</SelectContent></Select><Select value={groupFilter} onValueChange={setGroupFilter}><SelectTrigger className="h-11 w-36 text-xs sm:h-9"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部销售分组</SelectItem>{publicFirst(currentOverview.groups).map((group) => <SelectItem key={group.external_id} value={String(group.external_id)}><span className="flex w-full min-w-0 items-center justify-between gap-3"><span className="truncate">{group.name} · {group.platform || "-"}</span><span className={cn("shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium", group.is_exclusive ? "bg-warning/10 text-warning" : "bg-blue-500/10 text-blue-700 dark:text-blue-400")}>{group.is_exclusive ? "专属" : "公开"}</span></span></SelectItem>)}</SelectContent></Select><Button type="button" variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9" onClick={resetFilters}><RotateCcw className="size-3.5" />重置</Button><Button type="button" variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9" disabled={busy || overview.refreshing} onClick={() => void refreshAccounts()}><RefreshCw className={cn("size-3.5", overview.refreshing && "animate-spin")} />刷新</Button></div> : null}
          {accountListOpen ? <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border pt-2"><span className="ml-auto whitespace-nowrap text-xs text-muted-foreground">作用于当前筛选的 <strong className="font-semibold text-foreground">{filteredAccounts.length}</strong> 个账号</span><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" title={`启用当前筛选结果中 ${filteredEnableCount} 个未开启调度的账号`} disabled={busy || filteredEnableCount === 0} onClick={() => void runFilteredAccountAction("schedulable", true, "一键启用调度")}><Power className="size-3.5" />全部启用</Button><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" title={`禁用当前筛选结果中 ${filteredDisableCount} 个已开启调度的账号`} disabled={busy || filteredDisableCount === 0} onClick={() => void runFilteredAccountAction("schedulable", false, "一键禁用调度")}><PowerOff className="size-3.5" />全部禁用</Button><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" title={`接受当前筛选结果中 ${filteredSuggestionCount} 个账号的推荐调组`} disabled={busy || filteredSuggestionCount === 0} onClick={() => void runFilteredAccountAction("apply-suggestions", undefined, "接受推荐调组")}><ListChecks className="size-3.5" />接受推荐调组 <span className="text-[10px] text-muted-foreground">{filteredSuggestionCount}</span></Button><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" title={`接受当前筛选结果中 ${filteredDowngradeCount} 个账号的推荐降级`} disabled={busy || filteredDowngradeCount === 0} onClick={() => void runFilteredAccountAction("add-downgrades", undefined, "接受推荐降级")}><TrendingDown className="size-3.5" />接受推荐降级 <span className="text-[10px] text-muted-foreground">{filteredDowngradeCount}</span></Button></div> : null}
        </CardHeader>
        {accountListOpen ? <CardContent className="border-t border-border p-0">
          {selected.length ? <div className="border-b border-brand/20 bg-brand/5 px-4 py-2.5"><div className="flex flex-wrap items-center gap-2"><div className="mr-2 flex items-center gap-2 text-xs font-medium text-brand"><Users className="size-4" />已选 {selected.length} 个账号</div><Select value={batchMode} onValueChange={(value) => { setBatchMode(value as BatchMode); setBatchRuntimeValue("") }}><SelectTrigger className="h-9 w-40 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="groups">批量调整分组</SelectItem><SelectItem value="model_type">设置模型类型</SelectItem><SelectItem value="concurrency">设置并发数</SelectItem><SelectItem value="priority">设置优先级</SelectItem><SelectItem value="retry_count">设置同账号重试次数</SelectItem><SelectItem value="channel_group">渠道分组绑定</SelectItem><SelectItem value="manual">手工设置倍率</SelectItem></SelectContent></Select>{batchMode === "model_type" ? <><Select value={batchModelType || "__empty"} onValueChange={(value) => setBatchModelType(value === "__empty" ? "" : value)}><SelectTrigger className="h-9 w-44 text-xs"><SelectValue placeholder="选择模型类型" /></SelectTrigger><SelectContent><SelectItem value="__empty">清除类型</SelectItem>{modelTypeOptions.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select><Button size="sm" className="h-9 gap-1.5" disabled={busy} onClick={() => void saveBatchModelType()}><Save className="size-3.5" />保存</Button></> : batchMode === "channel_group" ? <><Select value={batchChannelID} onValueChange={(value) => { setBatchChannelID(value); setBatchGroup("") }}><SelectTrigger className="h-9 w-48 text-xs"><SelectValue placeholder="选择当前渠道" /></SelectTrigger><SelectContent>{currentOverview.monitor_channels.map((channel) => <SelectItem key={channel.id} value={String(channel.id)}>{channel.name}</SelectItem>)}</SelectContent></Select><Select value={batchGroup} onValueChange={(value) => setBatchGroup(value)} disabled={!batchChannelID}><SelectTrigger className="h-9 w-52 text-xs"><SelectValue placeholder="选择渠道分组" /></SelectTrigger><SelectContent>{batchGroups.map((group) => <SelectItem key={group} value={group}>{group}</SelectItem>)}</SelectContent></Select></> : batchMode === "manual" ? <Input className="h-9 w-44 font-mono text-xs" inputMode="decimal" value={batchMultiplier} onChange={(event) => setBatchMultiplier(event.target.value)} placeholder="成本倍率，如 0.8" /> : batchMode === "concurrency" || batchMode === "priority" || batchMode === "retry_count" ? <Input className="h-9 w-36 font-mono text-xs" inputMode="numeric" value={batchRuntimeValue} onChange={(event) => setBatchRuntimeValue(event.target.value)} placeholder={batchMode === "retry_count" ? "同账号重试次数 0-10" : batchMode === "concurrency" ? "并发数 1-1000" : "优先级 1-1000"} aria-label={batchMode === "retry_count" ? "批量设置同账号重试次数" : batchMode === "concurrency" ? "批量设置并发数" : "批量设置优先级"} /> : <Button size="sm" variant="outline" className="h-9 gap-1.5" disabled={busy} onClick={() => setBatchGroupDialogOpen(true)}><SlidersHorizontal className="size-3.5" />选择销售分组</Button>}{batchMode === "groups" || batchMode === "model_type" ? null : batchMode === "concurrency" || batchMode === "priority" || batchMode === "retry_count" ? <Button size="sm" className="h-9 gap-1.5" disabled={busy} onClick={() => void saveBatchRuntimeSettings()}><Save className="size-3.5" />保存</Button> : <><Button size="sm" className="h-9 gap-1.5" disabled={busy} onClick={() => void saveBatchOverride()}><Save className="size-3.5" />保存覆盖</Button><Button size="sm" variant="outline" className="h-9 gap-1.5" disabled={busy} onClick={() => void saveBatchOverride(true)}><X className="size-3.5" />清除覆盖</Button></>}</div></div> : null}
          <div className="relative isolate h-[min(72vh,760px)]">{overview.refreshing || usage.refreshing ? <div className="pointer-events-none absolute inset-x-0 top-10 z-50 flex justify-center"><span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><RefreshCw className="size-3.5 animate-spin text-brand" />正在刷新账号列表</span></div> : null}<div className="h-full overflow-auto"><div className="relay-account-grid sticky top-0 z-40 flex items-center gap-3 border-b border-border bg-muted px-4 py-2 text-[11px] text-foreground/90 font-medium lg:grid lg:gap-0 lg:p-0 lg:[&>*]:px-4 lg:[&>*]:py-2"><span className="fixed-column-shadow-desktop fixed-column-shadow-left lg:sticky lg:left-0 lg:z-40 lg:flex lg:self-stretch lg:items-center lg:bg-muted"><Checkbox checked={allFilteredSelected} onCheckedChange={(value) => { const ids = filteredAccounts.map((account) => account.external_id); setSelected((current) => value === true ? Array.from(new Set([...current, ...ids])) : current.filter((id) => !ids.includes(id))) }} aria-label="选择筛选结果" /></span><span className="fixed-column-shadow-desktop fixed-column-shadow-left hidden lg:sticky lg:left-[64px] lg:z-40 lg:flex lg:self-stretch lg:items-center lg:bg-muted">账号</span><span className="hidden lg:inline">平台</span><span className="hidden lg:inline">并发数</span><span className="hidden lg:inline" title="Sub2API 实时并发占用 / 并发上限">容量</span><SortButton label="优先级" title="数值越小调度优先级越高" active={accountSort?.key === "priority"} direction={accountSort?.key === "priority" ? accountSort.direction : undefined} onClick={() => toggleAccountSort("priority", "asc")} className="hidden lg:inline-flex" /><span className="hidden lg:inline" title="池模式下同一账号的失败重试次数">重试次数</span><span className="hidden lg:inline">调度</span><span className="hidden lg:inline-flex items-center gap-1">模型类型<ModelTypeHelp /></span><SortButton label="最近调用" title="综合最近调用的首字时间和总耗时排序，升序更流畅" active={accountSort?.key === "latency"} direction={accountSort?.key === "latency" ? accountSort.direction : undefined} onClick={() => toggleAccountSort("latency", "asc")} className="hidden lg:inline-flex" /><SortButton label="区间消费" active={accountSort?.key === "usage"} direction={accountSort?.key === "usage" ? accountSort.direction : undefined} onClick={() => toggleAccountSort("usage", "desc")} className="hidden lg:inline-flex" /><SortButton label="成本" active={accountSort?.key === "cost"} direction={accountSort?.key === "cost" ? accountSort.direction : undefined} onClick={() => toggleAccountSort("cost", "desc")} className="hidden lg:inline-flex" /><span className="hidden lg:inline">当前销售分组</span><span className="hidden lg:inline">风险</span><span className="hidden lg:inline" title="利润率 = (销售倍率 - 成本倍率) / 成本倍率">利润</span><span className="hidden lg:inline" title="存在倍率低于当前销售分组且仍高于账号成本的可用分组">可降级</span><span className="fixed-column-shadow-desktop fixed-column-shadow-right hidden lg:sticky lg:right-0 lg:z-50 lg:flex lg:self-stretch lg:items-center lg:justify-end lg:bg-muted">操作</span><span className="ml-auto lg:hidden">当前筛选 {filteredAccounts.length} 个</span></div>
          {filteredAccounts.length === 0 ? <div className="px-4 py-10 text-center text-sm text-muted-foreground">当前筛选没有账号</div> : filteredAccounts.map((account) => <RiskRow key={account.external_id} account={account} overview={currentOverview} selected={selected.includes(account.external_id)} busy={busy} scheduling={schedulingAccountID === account.external_id} probing={probingAccountID === account.external_id} testing={testingAccountID === account.external_id} deleting={deletingAccountID === account.external_id} onToggle={(checked) => setSelected((current) => checked ? [...new Set([...current, account.external_id])] : current.filter((id) => id !== account.external_id))} onApply={() => void applySuggestion(account)} onAddDowngrade={() => void addDowngradeGroups(account)} onEditGroups={() => setGroupEditor(account)} onSchedulableChange={(checked) => void setSchedulable(account, checked)} onProbe={() => void probeAccount(account)} onTest={() => void openAccountTest(account)} onDelete={() => void deleteAccount(account)} />)}</div></div>
        </CardContent> : null}
      </Card> : null}

      <RelayAdjustmentLog rows={currentOverview.adjustments} refreshing={overview.refreshing} />
    </div> : overview.error ? <Alert variant="destructive"><CircleAlert /><AlertTitle>读取失败</AlertTitle><AlertDescription>{overview.error}</AlertDescription></Alert> : null}</div></div> : <div className="border border-dashed border-border px-4 py-12 text-center"><Server className="mx-auto size-8 text-muted-foreground" /><p className="mt-3 text-sm font-medium">还没有配置中转站</p><Button className="mt-4 gap-1.5" size="sm" onClick={openCreate}><Plus className="size-3.5" />添加中转站</Button></div>}
    <GroupEditor account={groupEditor} groups={currentOverview?.groups.map((group) => ({ external_id: group.external_id, name: group.name, platform: group.platform, status: group.status, is_exclusive: group.is_exclusive, require_oauth_only: group.require_oauth_only, account_types: group.account_types, model_types: group.model_types, rate_multiplier: group.rate_multiplier })) ?? []} open={groupEditor != null} busy={busy} onOpenChange={(open) => { if (!open) setGroupEditor(null) }} onSave={(ids) => void saveGroups(ids)} />
    <BatchGroupEditor groups={currentOverview?.groups.map((group) => ({ external_id: group.external_id, name: group.name, platform: group.platform, status: group.status, is_exclusive: group.is_exclusive, require_oauth_only: group.require_oauth_only, account_types: group.account_types, model_types: group.model_types, rate_multiplier: group.rate_multiplier })) ?? []} count={selected.length} open={batchGroupDialogOpen} busy={busy} onOpenChange={setBatchGroupDialogOpen} onSave={(ids) => void saveBatchGroups(ids)} />
    <Dialog open={testGroup != null} onOpenChange={(open) => { if (!open && testingGroupID == null) setTestGroup(null) }}>
      <DialogContent className="w-[calc(100vw-2rem)] max-h-[calc(100dvh-2rem)] max-w-[772px] overflow-x-hidden overflow-y-auto p-0">
        <DialogHeader className="border-b border-border px-4 py-5 sm:px-6"><DialogTitle>快速测试分组</DialogTitle><DialogDescription className="sr-only">选择模型和调用次数后，使用管理员额度进行真实分组调用测试</DialogDescription></DialogHeader>
        <div className="space-y-4 px-4 py-4 sm:px-6">
          <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/30 px-3 py-2.5"><div className="min-w-0"><p className="truncate text-sm font-semibold text-foreground">{testGroup?.name || "-"}</p><p className="mt-0.5 text-[11px] text-muted-foreground">{testGroup?.platform || "未知平台"} · 分组 #{testGroup?.external_id || "-"}</p></div><span className={cn("shrink-0 rounded px-2 py-1 text-[11px] font-medium", testGroup?.status?.toLowerCase() === "active" ? "bg-success/10 text-success" : "bg-muted text-muted-foreground")}>{testGroup?.status?.toLowerCase() === "active" ? "已启用" : "未启用"}</span></div>
          <Alert className="border-warning/30 bg-warning/5 py-3 text-warning"><CircleDollarSign className="size-4" /><AlertDescription className="text-xs leading-5 text-warning"><strong>真实调用测试：</strong>会消耗该分组绑定的管理员额度，并写入使用记录、错误记录和分组监控。请按需设置次数。</AlertDescription></Alert>
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_132px]">
            <div className="space-y-1.5"><Label htmlFor="group-test-model">测试模型</Label><Select value={groupTestModel} onValueChange={setGroupTestModel} disabled={groupTestModelsLoading || groupTestModels.length === 0}><SelectTrigger id="group-test-model" className="w-full"><SelectValue placeholder={groupTestModelsLoading ? "正在读取模型..." : "没有可用模型"} /></SelectTrigger><SelectContent>{groupTestModels.map((model) => <SelectItem key={model} value={model}>{model}</SelectItem>)}</SelectContent></Select></div>
            <div className="space-y-1.5"><Label htmlFor="group-test-count">调用次数</Label><Input id="group-test-count" type="number" min={1} max={10} step={1} inputMode="numeric" value={groupTestCount} onChange={(event) => setGroupTestCount(event.target.value)} disabled={testingGroupID != null} className="font-mono tabular-nums" aria-describedby="group-test-count-help" /><p id="group-test-count-help" className="text-[10px] text-muted-foreground">1 - 10 次，串行执行</p></div>
          </div>
          {groupTestError ? <Alert variant="destructive" className="min-w-0 overflow-hidden py-3"><CircleAlert className="size-4 shrink-0" /><AlertDescription className="min-w-0 break-words text-xs leading-5"><div className="font-medium">{groupTestNeedsKey ? "未找到当前分组的管理员 API 密钥" : "分组快速测试失败"}</div><p className="mt-1 whitespace-pre-wrap">{groupTestError}</p>{groupTestNeedsKey && groupAPIKeysURL ? <a href={groupAPIKeysURL} target="_blank" rel="noopener noreferrer" className="mt-2 inline-flex cursor-pointer items-center gap-1.5 rounded border border-warning/40 bg-warning/10 px-2 py-1.5 font-medium text-warning underline underline-offset-2 transition-colors hover:bg-warning/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ExternalLink className="size-3.5" />前往创建 API 密钥</a> : null}</AlertDescription></Alert> : null}
          <div className="min-h-32 rounded-md border border-border bg-muted/20 p-3">
            {testingGroupID != null ? <p className="flex min-h-24 items-center justify-center gap-2 text-sm text-muted-foreground" aria-live="polite"><RefreshCw className="size-4 animate-spin text-brand" />正在执行 {groupTestCount} 次真实调用，请稍候...</p> : groupTestResult ? <div className="min-w-0 max-w-full space-y-3 overflow-hidden"><div className="grid grid-cols-3 divide-x rounded-md border border-border bg-card text-center"><div className="px-2 py-2"><p className="text-[10px] text-muted-foreground">请求</p><p className="font-mono text-sm font-semibold tabular-nums">{groupTestResult.requested}</p></div><div className="px-2 py-2"><p className="text-[10px] text-muted-foreground">成功</p><p className="font-mono text-sm font-semibold tabular-nums text-success">{groupTestResult.succeeded}</p></div><div className="px-2 py-2"><p className="text-[10px] text-muted-foreground">失败</p><p className="font-mono text-sm font-semibold tabular-nums text-danger">{groupTestResult.failed}</p></div></div><div className="max-h-56 min-w-0 max-w-full space-y-1.5 overflow-x-hidden overflow-y-auto">{groupTestResult.results.map((item) => <div key={item.index} className="flex min-w-0 max-w-full items-start gap-2 overflow-hidden rounded-md border border-border bg-card px-2.5 py-2 text-xs"><span className={cn("mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full", item.success ? "bg-success/10 text-success" : "bg-danger/10 text-danger")}>{item.success ? <Check className="size-3" /> : <CircleAlert className="size-3" />}</span><div className="min-w-0 max-w-full flex-1"><div className="flex min-w-0 flex-wrap items-center justify-between gap-2"><span className="font-medium">第 {item.index} 次调用</span><span className={cn("font-mono tabular-nums", item.success ? "text-muted-foreground" : "text-danger")}>{item.status_code ? `HTTP ${item.status_code}` : "请求失败"} · {formatLatency(item.duration_ms)}</span></div><p className={cn("mt-1 max-h-40 overflow-x-hidden overflow-y-auto break-all whitespace-pre-wrap pr-1 text-[11px]", item.success ? "text-muted-foreground" : "text-danger")}>{item.output || (item.success ? "调用成功" : "无错误详情")}</p></div></div>)}</div></div> : <p className="flex min-h-24 items-center justify-center text-sm text-muted-foreground">选择模型和调用次数后开始真实测试。</p>}
          </div>
        </div>
        <DialogFooter className="border-t border-border px-4 py-4 sm:px-6"><Button type="button" variant="outline" disabled={testingGroupID != null} onClick={() => setTestGroup(null)}>关闭</Button><Button type="button" className="gap-1.5" disabled={testingGroupID != null || groupTestModelsLoading || !groupTestModel} onClick={() => void runGroupTest()}><Play className="size-3.5" />开始测试</Button></DialogFooter>
      </DialogContent>
    </Dialog>
    <Dialog open={testAccount != null} onOpenChange={(open) => { if (!open && testingAccountID == null) setTestAccount(null) }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] max-w-xl overflow-y-auto p-0">
        <DialogHeader className="border-b border-border px-4 py-5 sm:px-6"><DialogTitle>测试账号连接</DialogTitle><DialogDescription className="sr-only">选择模型和测试模式后测试账号连接</DialogDescription></DialogHeader>
        <div className="space-y-4 px-4 sm:px-6">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5"><Label>选择测试模型</Label><Select value={testModel} onValueChange={setTestModel} disabled={testModelsLoading || testModels.length === 0}><SelectTrigger className="w-full"><SelectValue placeholder={testModelsLoading ? "正在读取模型..." : "没有可用模型"} /></SelectTrigger><SelectContent>{testModels.map((model) => <SelectItem key={model} value={model}>{model}</SelectItem>)}</SelectContent></Select></div>
            <div className="space-y-1.5"><Label>测试模式</Label><Select value={testMode} onValueChange={setTestMode}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="regular">常规请求</SelectItem><SelectItem value="stream">流式请求</SelectItem></SelectContent></Select></div>
          </div>
          <div className="min-h-56 rounded-md border border-slate-800 bg-slate-950 p-4 font-mono text-xs leading-5 text-slate-300">
            <p>开始测试账号：<span className="text-sky-400">{testAccount?.name || "-"}</span></p>
            <p>账号类型：<span className="text-slate-100">{testAccount?.type || "-"}</span></p>
            <p>测试模式：<span className="text-slate-100">{testMode === "stream" ? "流式请求" : "常规请求"}</span></p>
            {testingAccountID != null ? <p className="mt-2 flex items-center gap-2 text-teal-300"><RefreshCw className="size-3.5 animate-spin" />正在连接 API...</p> : null}
            {testError ? <><p className="mt-2 text-red-300">连接测试失败</p><p className="max-h-36 overflow-auto whitespace-pre-wrap break-words text-red-200">{testError}</p></> : null}
            {testResult ? <><p className="mt-2 text-emerald-300">已连接到 API</p><p>使用模型：<span className="text-cyan-300">{testOutput.model || testModel}</span></p><p>发送测试消息：<span className="text-slate-100">&quot;hi&quot;</span></p><p className="mt-2 text-amber-300">响应：</p><div className="max-h-44 overflow-auto whitespace-pre-wrap break-words text-emerald-200">{testOutput.content || testOutput.fallback || "上游未返回文本内容"}</div><p className="mt-3 flex items-center gap-2 text-emerald-300"><Check className="size-3.5" />测试完成 <span className="text-slate-500">HTTP {testResult.status_code} · {formatLatency(testResult.duration_ms)}</span></p></> : null}
            {!testingAccountID && !testError && !testResult ? <p className="mt-2 text-slate-500">准备测试，点击开始测试。</p> : null}
          </div>
        </div>
        <DialogFooter className="border-t border-border px-4 py-4 sm:px-6"><Button type="button" variant="outline" disabled={testingAccountID != null} onClick={() => setTestAccount(null)}>关闭</Button><Button type="button" className="gap-1.5 bg-teal-600 hover:bg-teal-700" disabled={testingAccountID != null || testModelsLoading || !testModel} onClick={() => void runAccountTest()}><Play className="size-3.5" />开始测试</Button></DialogFooter>
      </DialogContent>
    </Dialog>
    {confirmDialog}
  </section>
}
