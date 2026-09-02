"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import {
  CircleAlert,
  CheckCircle2,
  ChevronDown,
  Loader2,
  LogIn,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  Trash2,
  XCircle,
} from "lucide-react"
import { Card } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { useChannels, useChannelLatencyTrends, useChannelMetrics, useChannelRates, useLatestRatioChanges } from "@/lib/queries"
import { apiFetch } from "@/lib/api"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { channelTypeLabel, money, ratioArrow, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { syncChannelStream, testLoginStream, type ProgressEvent } from "@/lib/sync-stream"
import type { Channel, ChannelAccount, RateSnapshot, RelayLatencySample, RelayUsageRange } from "@/lib/api-types"
import { ChannelFormDialog } from "@/components/monitor/channel-form-dialog"
import { PlatformBadge } from "@/components/relay/platform-badge"

type Status = "healthy" | "low" | "failed" | "idle"
type MonitorStatusFilter = "enabled" | "disabled" | "all"
type ManagementModeFilter = "auto" | "manual" | "all"
type AccountCountFilter = "multi" | "single" | "all"

function statusOf(c: Channel, balance?: number | null): Status {
  if (c.last_error) return "failed"
  const current = balance ?? c.last_balance
  if (current == null) return "idle"
  if (c.balance_threshold > 0 && current <= c.balance_threshold) return "low"
  return "healthy"
}

const statusMap: Record<Status, { label: string; cls: string }> = {
  healthy: { label: "健康", cls: "text-success bg-success/10" },
  low: { label: "低余额", cls: "text-warning bg-warning/10" },
  failed: { label: "登录失败", cls: "text-danger bg-danger/10" },
  idle: { label: "尚未采集", cls: "text-muted-foreground bg-muted/40" },
}

function Row({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("flex min-w-0 items-center justify-between gap-3 bg-card px-3 py-2.5", className)}>
      <span className="shrink-0 text-[11px] font-medium text-muted-foreground">{label}</span>
      <div className="min-w-0 truncate text-right text-xs font-semibold text-foreground">{children}</div>
    </div>
  )
}

function siteHost(value: string) {
  try {
    return new URL(value).host
  } catch {
    return value
  }
}

function cny(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "—"
  return `¥${value.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

function charge(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "—"
  return `$${value.toLocaleString("en-US", { minimumFractionDigits: 6, maximumFractionDigits: 6 })}`
}

function balanceTone(channel: Channel): string {
  if (channel.last_balance == null || !Number.isFinite(channel.last_balance)) return "text-muted-foreground"
  if (channel.last_balance <= 0) return "text-danger"
  if (channel.balance_threshold > 0 && channel.last_balance <= channel.balance_threshold) return "text-warning"
  return "text-success"
}

function channelAccounts(channel: Channel): ChannelAccount[] {
  if (channel.accounts?.length > 0) return channel.accounts
  return [{
    id: 0,
    is_primary: true,
    username: channel.username,
    credential_mode: channel.credential_mode,
    turnstile_enabled: channel.turnstile_enabled,
    captcha_config_id: channel.captcha_config_id,
    last_balance: channel.last_balance,
    last_balance_at: channel.last_balance_at,
    last_error: channel.last_error,
    created_at: channel.created_at,
    updated_at: channel.updated_at,
  }]
}

function accountName(account: ChannelAccount, index: number) {
  return account.username.trim() || `账号 ${index + 1}`
}

function AccountPreview({ accounts }: { accounts: ChannelAccount[] }) {
  const first = accounts[0]
  if (!first) return <span>未填写</span>
  const extra = accounts.slice(1)
  return (
    <span className="inline-flex min-w-0 items-center justify-end gap-1.5">
      <span className="truncate" title={accountName(first, 0)}>{accountName(first, 0)}</span>
      {extra.length > 0 ? (
        <Tooltip delayDuration={150}>
          <TooltipTrigger asChild>
            <button type="button" className="shrink-0 rounded border border-border bg-muted/60 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground transition-colors hover:border-brand/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`查看其余 ${extra.length} 个账号`}>
              +{extra.length} 更多
            </button>
          </TooltipTrigger>
          <TooltipContent side="top" className="max-w-xs p-2 text-xs">
            <p className="mb-1.5 font-medium">其余账号</p>
            <ul className="space-y-1.5">
              {extra.map((account, index) => (
                <li key={account.id} className="flex min-w-0 items-center justify-between gap-4">
                  <span className="truncate">{accountName(account, index + 1)}</span>
                  <span className={cn("shrink-0 font-mono tabular-nums", account.last_error && "text-warning")}>{money(account.last_balance)}</span>
                </li>
              ))}
            </ul>
          </TooltipContent>
        </Tooltip>
      ) : null}
    </span>
  )
}

function AccountBalance({ accounts, total }: { accounts: ChannelAccount[]; total: number | null | undefined }) {
  if (accounts.length <= 1) return <>{money(total)}</>
  return (
    <Tooltip delayDuration={150}>
      <TooltipTrigger asChild>
        <span className="block truncate" tabIndex={0} aria-label="查看各账号余额">{money(total)}</span>
      </TooltipTrigger>
      <TooltipContent side="top" className="max-w-xs p-2 text-xs">
        <p className="mb-1.5 font-medium">账号余额明细</p>
        <ul className="space-y-1.5">
          {accounts.map((account, index) => (
            <li key={account.id} className="flex min-w-0 items-center justify-between gap-4">
              <span className="truncate">{accountName(account, index)}</span>
              <span className={cn("shrink-0 font-mono tabular-nums", account.last_error && "text-warning")}>{money(account.last_balance)}</span>
            </li>
          ))}
        </ul>
        <div className="mt-2 flex items-center justify-between border-t border-background/20 pt-1.5 font-medium">
          <span>合计</span><span className="font-mono tabular-nums">{money(total)}</span>
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

const usageRanges: { value: RelayUsageRange; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "today", label: "今天" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
]

/** ratioTone 按倍率给 chip 上色，与 ChannelRatesPanel 共用同一套规则。 */
function ratioTone(r: number): string {
  if (r < 1) return "bg-brand/10 text-brand ring-brand/20"
  if (r > 1.2) return "bg-warning/10 text-warning ring-warning/20"
  return "bg-muted text-foreground ring-border"
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
  const seconds = milliseconds / 1_000
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}秒`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}分 ${String(Math.round(seconds % 60)).padStart(2, "0")}秒`
}

function formatSampleTime(value: string | undefined) {
  if (!value) return "-"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  })
}

function formatTokens(value: number | undefined) {
  if (value == null || !Number.isFinite(value)) return "-"
  return Math.max(0, Math.round(value)).toLocaleString("zh-CN")
}

function ChannelLatencyTrend({ samples }: { samples: RelayLatencySample[] }) {
  const ordered = useMemo(() => [...samples].reverse(), [samples])
  const latestSample = useMemo(() => samples.reduce<RelayLatencySample | null>((latest, sample) => {
    if (!latest || new Date(sample.created_at).getTime() > new Date(latest.created_at).getTime()) return sample
    return latest
  }, null), [samples])
  return (
    <div className="mt-3 border-t border-border pt-3">
      <div className="mb-1.5 flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
        <span className="text-[11px] font-medium text-muted-foreground">
          最近调用延时
          {latestSample ? <span className="ml-1 inline-flex items-center gap-1 font-mono text-[11px] font-medium tabular-nums text-foreground/80"><span aria-hidden="true">·</span><span>最后调用</span><time dateTime={latestSample.created_at} className="text-xs font-semibold text-foreground">{formatSampleTime(latestSample.created_at)}</time></span> : null}
        </span>
        <span className="font-mono text-[10px] tabular-nums text-muted-foreground">最近 {samples.length} / 60 条</span>
      </div>
      {ordered.length === 0 ? <div className="flex h-9 items-center rounded-md border border-dashed border-border px-2 text-[10px] text-muted-foreground">暂无延时数据</div> : (
        <Tooltip delayDuration={160}>
          <TooltipTrigger asChild>
            <button type="button" className="block h-10 w-full rounded-md px-1 py-1 transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`查看最近 ${samples.length} 次调用延时`}>
              <span className="flex h-8 w-full items-stretch gap-px overflow-hidden" aria-hidden="true">
                {ordered.map((sample, index) => {
                  const first = latencyTone(sample.first_token_ms, "first")
                  const duration = latencyTone(sample.duration_ms, "duration")
                  return <span key={`${sample.created_at}-${index}`} className="min-w-0 flex-1 rounded-[2px] opacity-90 transition-opacity hover:opacity-100" style={{ background: `linear-gradient(to top, ${latencyColor[duration]} 0 50%, ${latencyColor[first]} 50% 100%)` }} />
                })}
              </span>
            </button>
          </TooltipTrigger>
          <TooltipContent side="top" align="end" className="max-h-96 w-[540px] max-w-[calc(100vw-24px)] overflow-y-auto border border-slate-200 bg-white p-0 text-[11px] text-slate-900 shadow-xl dark:border-slate-200 dark:bg-white dark:text-slate-900 [&>svg]:hidden">
            <div className="sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium"><span>最近 {samples.length} 次调用</span><span className="text-slate-500">上半段首字 · 下半段总耗时</span></div>
            <div className="divide-y divide-slate-100 px-3">
              {[...ordered].reverse().map((sample, index) => {
                const first = latencyTone(sample.first_token_ms, "first")
                const duration = latencyTone(sample.duration_ms, "duration")
                const sub2apiGroup = sample.group_name?.trim() || "未标记分组"
                const sub2apiMultiplier = sample.group_multiplier == null ? "-" : `${sample.group_multiplier.toFixed(3)}×`
                const channelMultiplier = sample.channel_group_multiplier == null ? "-" : `${sample.channel_group_multiplier.toFixed(3)}×`
                const cacheCreationTokens = sample.cache_creation_tokens ?? 0
                const cacheCreation5mTokens = sample.cache_creation_5m_tokens ?? 0
                const cacheCreation1hTokens = sample.cache_creation_1h_tokens ?? 0
                const cacheCreationTotal = cacheCreationTokens || cacheCreation5mTokens + cacheCreation1hTokens
                const totalTokens = (sample.input_tokens ?? 0) + (sample.output_tokens ?? 0) + (sample.cache_read_tokens ?? 0) + cacheCreationTotal
                return (
                  <div key={`${sample.created_at}-detail-${index}`} className="grid grid-cols-[112px_minmax(0,1fr)] gap-2 py-2">
                    <time dateTime={sample.created_at} className="whitespace-nowrap font-mono text-xs font-semibold leading-5 tabular-nums text-slate-700">{formatSampleTime(sample.created_at)}</time>
                    <div className="min-w-0">
                      <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
                        <div className="min-w-0 rounded-md border border-emerald-200 bg-emerald-50 px-2 py-1.5">
                          <p className="text-[9px] font-semibold uppercase text-emerald-700">Sub2API 分组</p>
                          <p className="mt-0.5 flex min-w-0 items-center gap-1.5 whitespace-nowrap text-emerald-950">
                            <span className="min-w-0 truncate font-semibold" title={sub2apiGroup}>{sub2apiGroup}</span>
                            <span className="shrink-0 rounded bg-emerald-600 px-1.5 py-0.5 font-mono text-xs font-semibold tabular-nums text-white">{sub2apiMultiplier}</span>
                          </p>
                        </div>
                        <div className="min-w-0 rounded-md border border-sky-200 bg-sky-50 px-2 py-1.5">
                          <p className="text-[9px] font-semibold uppercase text-sky-700">渠道倍率</p>
                          <p className="mt-0.5 font-mono text-sm font-semibold tabular-nums text-sky-950">{channelMultiplier}</p>
                        </div>
                      </div>
                      <div className="mt-2 flex min-w-0 flex-wrap items-center gap-2 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-2">
                        <PlatformBadge platform={sample.platform} />
                        <span className="min-w-0 flex-1 truncate font-mono text-sm font-semibold leading-5 text-slate-950" title={sample.model || "-"}>{sample.model || "-"}</span>
                        {sample.request_type ? <span className="shrink-0 text-[11px] font-medium text-slate-600">{sample.request_type}</span> : null}
                      </div>
                      <p className="mt-1 font-mono text-xs font-semibold tabular-nums text-slate-900">Token 总量 {formatTokens(totalTokens)}</p>
                      <p className="mt-1 flex flex-wrap gap-x-4 gap-y-1 font-mono text-xs font-semibold tabular-nums">
                        <span className={latencyTextClass[first]}>首字 {formatLatency(sample.first_token_ms)}</span>
                        <span className={latencyTextClass[duration]}>总耗时 {formatLatency(sample.duration_ms)}</span>
                      </p>
                    </div>
                  </div>
                )
              })}
            </div>
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

/** InlineRates 在渠道卡片内部展示当前所有分组倍率，默认完整展开。 */
function InlineRates({ channelID }: { channelID: number }) {
  const { data, loading } = useChannelRates(channelID)
  const rateChanges = useLatestRatioChanges(channelID)
  const latestChanges = new Map(
    (rateChanges.data ?? []).map((change) => [change.model_name, change]),
  )
  const rates = [...(data ?? [])].sort((a, b) => a.ratio - b.ratio)
  const [expanded, setExpanded] = useState(true)
  const [hasOverflow, setHasOverflow] = useState(false)
  const chipBoxRef = useRef<HTMLDivElement>(null)

  // 监听 chip 容器尺寸变化，决定是否要显示"展开"按钮。
  // 收起状态下 scrollHeight > clientHeight 表示有内容被裁剪。
  useEffect(() => {
    const el = chipBoxRef.current
    if (!el) return
    const check = () => {
      if (expanded) return
      setHasOverflow(el.scrollHeight > el.clientHeight + 1)
    }
    check()
    const ro = new ResizeObserver(check)
    ro.observe(el)
    return () => ro.disconnect()
  }, [rates.length, expanded])

  if (loading) return null
  if (rates.length === 0) return null

  const showToggle = hasOverflow || expanded

  return (
    <div className="py-3">
      <div className="mb-1.5 flex items-center justify-between">
        <p className="text-[11px] text-muted-foreground">
          {rates.length} 个分组
        </p>
        {showToggle ? (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="inline-flex items-center gap-0.5 text-[11px] text-muted-foreground hover:text-foreground"
          >
            {expanded ? "收起" : "展开"}
            <ChevronDown
              className={cn(
                "size-3 transition-transform duration-200",
                expanded && "rotate-180",
              )}
            />
          </button>
        ) : null}
      </div>

      <div className="relative">
        <div
          ref={chipBoxRef}
          className={cn(
            "flex flex-wrap gap-1.5 overflow-hidden pt-1.5 transition-[max-height] duration-300 ease-out",
            // 收起：max-h-16 为两行标签及其右上角标留出空间；展开：足够大的上限，留点缓冲让 transition 不立即消失。
            expanded ? "max-h-150" : "max-h-16",
          )}
        >
          {rates.map((r) => {
            const latestChangeForRate = latestChanges.get(r.model_name)
            const lowered = latestChangeForRate != null
              && latestChangeForRate.old_ratio != null
              && latestChangeForRate.new_ratio < latestChangeForRate.old_ratio

            return (
              <Tooltip key={r.id} delayDuration={150}>
                <TooltipTrigger asChild>
                  <span
                    className={cn(
                      "relative inline-flex cursor-default items-center gap-1 rounded px-1.5 py-0.5 text-[11px] ring-1 ring-inset transition-colors hover:bg-muted/60",
                      ratioTone(r.ratio),
                    )}
                  >
                    {latestChangeForRate ? (
                      <span
                        aria-label={lowered ? "倍率最近下降" : "倍率最近上升"}
                        className={cn(
                          "absolute -right-0.5 -top-0.5 size-2 rounded-full ring-2 ring-background",
                          lowered ? "bg-success" : "bg-danger",
                        )}
                      />
                    ) : null}
                    <span className="font-medium">{r.model_name}</span>
                    <span className="font-semibold tabular-nums">{r.ratio.toFixed(3)}</span>
                  </span>
                </TooltipTrigger>
                <TooltipContent side="top" className="max-w-xs text-xs">
                  <p className="font-medium">{r.model_name}</p>
                  {r.description ? (
                    <p className="mt-0.5 text-muted-foreground">{r.description}</p>
                  ) : (
                    <p className="mt-0.5 italic text-muted-foreground">{"(无描述)"}</p>
                  )}
                  <p className="mt-0.5 text-muted-foreground">
                    {"最近更新："}
                    {relativeTime(r.last_seen_at)}
                  </p>
                  {latestChangeForRate ? (
                    <p className={cn("mt-0.5", lowered ? "text-success" : "text-danger")}>
                      {"最近倍率变动："}
                      {ratioArrow(latestChangeForRate.old_ratio, latestChangeForRate.new_ratio)}
                    </p>
                  ) : null}
                </TooltipContent>
              </Tooltip>
            )
          })}
        </div>
        {/* 折叠时底部淡出，提示还有更多内容 */}
        {!expanded && hasOverflow ? (
          <div className="pointer-events-none absolute inset-x-0 bottom-0 h-4 bg-linear-to-t from-background to-transparent" />
        ) : null}
      </div>
    </div>
  )
}

function ManualRatesEditor({ channelID }: { channelID: number }) {
  const { data, loading, refetch } = useChannelRates(channelID)
  const [editingID, setEditingID] = useState<number | null>(null)
  const [name, setName] = useState("")
  const [ratio, setRatio] = useState("1")
  const [saving, setSaving] = useState(false)

  function reset() {
    setEditingID(null)
    setName("")
    setRatio("1")
  }

  function beginEdit(rate: RateSnapshot) {
    if (rate.source === "relay_account") return
    setEditingID(rate.id)
    setName(rate.model_name)
    setRatio(String(rate.ratio))
  }

  async function save() {
    const trimmed = name.trim()
    const value = Number(ratio)
    if (!trimmed) return toast.error("请填写分组名称")
    if (!Number.isFinite(value) || value < 0) return toast.error("倍率必须是非负数字")
    setSaving(true)
    try {
      await apiFetch(`/channels/${channelID}/rates${editingID ? `/${editingID}` : ""}`, {
        method: editingID ? "PUT" : "POST",
        body: JSON.stringify({ model_name: trimmed, ratio: value, completion_ratio: value }),
      })
      toast.success(editingID ? "分组已更新" : "分组已创建")
      reset()
      await refetch()
    } catch (error) {
      toast.error((error as Error).message || "保存分组失败")
    } finally {
      setSaving(false)
    }
  }

  async function remove(rate: RateSnapshot) {
    if (!window.confirm(`删除分组“${rate.model_name}”？`)) return
    try {
      await apiFetch(`/channels/${channelID}/rates/${rate.id}`, { method: "DELETE" })
      toast.success("分组已删除")
      if (editingID === rate.id) reset()
      await refetch()
    } catch (error) {
      toast.error((error as Error).message || "删除分组失败")
    }
  }

  return (
    <div className="mt-3 flex-1 border-t border-border pt-3">
      <div className="mb-2.5 flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <p className="text-xs font-semibold text-foreground">渠道分组</p>
          <span className="rounded-full bg-brand/10 px-1.5 py-0.5 text-[10px] font-medium text-brand">成本绑定</span>
        </div>
        <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">{(data ?? []).length} 个</span>
      </div>
      <div className="rounded-lg border border-border bg-muted/20 p-2">
        <div className="mb-1.5 flex items-center justify-between px-0.5">
          <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{editingID ? "编辑分组" : "新增分组"}</span>
          {editingID ? <button type="button" className="min-h-11 px-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground" onClick={reset}>取消</button> : null}
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_5.25rem_auto] gap-1.5">
          <input aria-label="分组名称" value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：Claude 主力" className="h-10 min-w-0 rounded-md border border-input bg-background px-2.5 text-xs outline-none transition-shadow placeholder:text-muted-foreground/60 focus-visible:ring-2 focus-visible:ring-ring" />
          <div className="relative">
            <input aria-label="倍率" value={ratio} onChange={(event) => setRatio(event.target.value)} inputMode="decimal" placeholder="倍率" className="h-10 w-full rounded-md border border-input bg-background px-2.5 pr-6 text-xs tabular-nums outline-none transition-shadow placeholder:text-muted-foreground/60 focus-visible:ring-2 focus-visible:ring-ring" />
            <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-muted-foreground">×</span>
          </div>
          <Button type="button" size="sm" className="h-10 min-w-16 gap-1 px-2 text-xs" disabled={saving} onClick={() => void save()}>{editingID ? <CheckCircle2 className="size-3.5" /> : <Plus className="size-3.5" />}{editingID ? "保存" : "添加"}</Button>
        </div>
      </div>
      {loading ? <p className="mt-2 px-1 text-[11px] text-muted-foreground">加载分组中…</p> : (data ?? []).length === 0 ? <p className="mt-2 px-1 text-[11px] text-muted-foreground">暂无渠道分组</p> : (
        <div className="mt-2 overflow-hidden rounded-lg border border-border bg-card">
          {(data ?? []).map((rate) => (
            <div key={rate.id} className="group border-b border-border last:border-b-0 hover:bg-muted/30">
              <div className="flex min-h-11 items-center gap-2 px-2.5">
                <span className="size-1.5 shrink-0 rounded-full bg-brand/70" />
                <span className="min-w-0 flex-1 truncate text-xs font-medium" title={rate.model_name}>{rate.model_name}</span>
                <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] font-medium tabular-nums text-foreground">{rate.ratio.toFixed(3)}×</span>
                {rate.source === "relay_account" ? <span className="rounded bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-medium text-sky-700 dark:text-sky-400">自动关联</span> : <><Button type="button" variant="ghost" size="icon" aria-label={`编辑 ${rate.model_name}`} className="size-9 text-muted-foreground opacity-70 transition-opacity hover:text-foreground group-hover:opacity-100" onClick={() => beginEdit(rate)}><Pencil className="size-3.5" /></Button><Button type="button" variant="ghost" size="icon" aria-label={`删除 ${rate.model_name}`} className="size-9 text-muted-foreground opacity-70 transition-opacity hover:text-destructive group-hover:opacity-100" onClick={() => void remove(rate)}><Trash2 className="size-3.5" /></Button></>}
              </div>
              <div className="flex flex-wrap items-center gap-1.5 border-t border-border/70 bg-muted/15 px-2.5 py-2">
                <span className="mr-1 text-[10px] font-medium text-muted-foreground">已绑定 {rate.bound_accounts?.length ?? 0} 个账号</span>
                {(rate.bound_accounts ?? []).length === 0 ? <span className="text-[10px] text-muted-foreground">暂无</span> : (rate.bound_accounts ?? []).map((account) => (
                  <span key={`${account.relay_station_id}-${account.relay_account_external_id}`} className="max-w-full truncate rounded bg-brand/10 px-1.5 py-0.5 text-[10px] font-medium text-brand" title={`${account.relay_station_name} · ${account.relay_account_name} · #${account.relay_account_external_id}`}>{account.relay_station_name} · {account.relay_account_name}</span>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

interface ChannelSyncState {
  running: boolean
  events: ProgressEvent[]
  latest: ProgressEvent | null
  finalOk: boolean | null
  fading: boolean
}

function emptySyncState(): ChannelSyncState {
  return { running: false, events: [], latest: null, finalOk: null, fading: false }
}

const stageLabel: Record<ProgressEvent["stage"], string> = {
  captcha: "打码",
  session: "会话",
  login: "登录",
  balance: "余额",
  rates: "倍率",
  done: "完成",
  error: "失败",
}

const stageOrder: Record<ProgressEvent["stage"], number> = {
  captcha: 1,
  session: 2,
  login: 3,
  balance: 4,
  rates: 5,
  done: 9,
  error: 9,
}

/** 按 stage 去重，每个 stage 只留最后一条事件（"在做中→完成"会被覆盖成完成态）。 */
function deriveSteps(events: ProgressEvent[]): ProgressEvent[] {
  const byStage = new Map<ProgressEvent["stage"], ProgressEvent>()
  for (const ev of events) byStage.set(ev.stage, ev)
  return [...byStage.values()].sort((a, b) => stageOrder[a.stage] - stageOrder[b.stage])
}

function SyncProgressStrip({ state }: { state: ChannelSyncState }) {
  if (!state.running && state.latest == null) return null
  const steps = deriveSteps(state.events)

  return (
    <div
      className={cn(
        "mt-3 rounded-lg border border-border bg-muted/30 px-3 py-2.5",
        // 入场：上方滑入 + 淡入
        "animate-in fade-in slide-in-from-top-1 duration-300",
        // 出场：和 scheduleHide 里的 500ms 对齐
        "transition-all duration-500 ease-out",
        state.fading ? "-translate-y-0.5 opacity-0" : "opacity-100",
      )}
    >
      {steps.length === 0 ? (
        <div className="flex items-center gap-2 text-xs">
          <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
          <span className="text-foreground/80">{"准备中…"}</span>
        </div>
      ) : (
        <ul className="space-y-1.5">
          {steps.map((ev) => {
            // 终止态：stage=done 或 error；显式 ok=true / false 也算
            const failed = ev.stage === "error" || ev.ok === false
            const succeeded = ev.stage === "done" || ev.ok === true
            const running = !failed && !succeeded
            const Icon = running ? Loader2 : failed ? XCircle : CheckCircle2
            const tone = running ? "text-muted-foreground" : failed ? "text-danger" : "text-success"
            return (
              <li
                key={ev.stage}
                className="flex items-center gap-2 text-xs animate-in fade-in duration-200"
              >
                <Icon
                  className={cn("size-3.5 shrink-0", tone, running && "animate-spin")}
                />
                <span className="w-9 shrink-0 text-[11px] text-muted-foreground">
                  {stageLabel[ev.stage]}
                </span>
                <span
                  className={cn(
                    "truncate",
                    failed ? "text-danger" : running ? "text-foreground/80" : "text-foreground",
                  )}
                >
                  {ev.message}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

export function ChannelCards({ usageRange = "today" }: { usageRange?: RelayUsageRange }) {
  const { data: channels, loading, error, refetch } = useChannels()
  const [monitorStatus, setMonitorStatus] = useState<MonitorStatusFilter>("enabled")
  const [managementMode, setManagementMode] = useState<ManagementModeFilter>("all")
  const [accountCount, setAccountCount] = useState<AccountCountFilter>("all")
  const [channelNameQuery, setChannelNameQuery] = useState("")
  const [remarkQuery, setRemarkQuery] = useState("")
  const hasActiveFilters = monitorStatus !== "enabled" || managementMode !== "all" || accountCount !== "all" || channelNameQuery.trim() !== "" || remarkQuery.trim() !== ""
  const metrics = useChannelMetrics(usageRange)
  const latencyTrends = useChannelLatencyTrends(60)
  const metricByChannel = new Map((metrics.data ?? []).map((item) => [item.channel_id, item]))
  const latencyByChannel = new Map((latencyTrends.data ?? []).map((item) => [item.channel_id, item.samples]))
  const filteredChannels = useMemo(() => {
    if (!channels) return []
    const nameQuery = channelNameQuery.trim().toLocaleLowerCase()
    const normalizedRemarkQuery = remarkQuery.trim().toLocaleLowerCase()
    return channels.filter((channel) => {
      const statusMatches = monitorStatus === "all" || channel.monitor_enabled === (monitorStatus === "enabled")
      const managementMatches = managementMode === "all" || channel.balance_mode === managementMode
      const isMultiAccount = (channel.accounts?.length ?? 0) > 1
      const accountCountMatches = accountCount === "all" || isMultiAccount === (accountCount === "multi")
      const nameMatches = nameQuery === "" || channel.name.toLocaleLowerCase().includes(nameQuery)
      const remarkMatches = normalizedRemarkQuery === "" || channel.remark?.toLocaleLowerCase().includes(normalizedRemarkQuery)
      return statusMatches && managementMatches && accountCountMatches && nameMatches && remarkMatches
    })
  }, [accountCount, channelNameQuery, channels, managementMode, monitorStatus, remarkQuery])
  const refresh = useTriggerRefresh()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [editing, setEditing] = useState<Channel | null>(null)
  const [creating, setCreating] = useState(false)
  const [busyAction, setBusyAction] = useState<string | null>(null)
  // 每个渠道当前 sync 进度（最新一条事件） + 历史事件
  const [syncState, setSyncState] = useState<Record<number, ChannelSyncState>>({})

  // 成功后自动消失需要的两段定时器：先 5s 显示，再 500ms 过渡（与 strip 的 transition-opacity duration-500 对齐）。
  const hideTimers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map())

  useEffect(() => {
    const timers = hideTimers.current
    return () => {
      timers.forEach((t) => clearTimeout(t))
      timers.clear()
    }
  }, [])

  function clearHideTimer(id: number) {
    const t = hideTimers.current.get(id)
    if (t != null) {
      clearTimeout(t)
      hideTimers.current.delete(id)
    }
  }

  function scheduleHide(id: number) {
    clearHideTimer(id)
    const t1 = setTimeout(() => {
      patchSync(id, (prev) => ({ ...prev, fading: true }))
      const t2 = setTimeout(() => {
        setSyncState((s) => {
          const { [id]: _gone, ...rest } = s
          void _gone
          return rest
        })
        hideTimers.current.delete(id)
      }, 500)
      hideTimers.current.set(id, t2)
    }, 5000)
    hideTimers.current.set(id, t1)
  }

  function patchSync(id: number, fn: (prev: ChannelSyncState) => ChannelSyncState) {
    setSyncState((s) => ({ ...s, [id]: fn(s[id] ?? emptySyncState()) }))
  }

  async function startStream(channel: Channel, action: "sync" | "test-login") {
    clearHideTimer(channel.id)
    patchSync(channel.id, () => ({
      running: true,
      events: [],
      latest: null,
      finalOk: null,
      fading: false,
    }))
    let sawError = false
    const stream = action === "sync" ? syncChannelStream : testLoginStream
    try {
      await stream(channel.id, {
        onEvent: (ev) => {
          if (ev.stage === "error" || ev.ok === false) sawError = true
          patchSync(channel.id, (prev) => ({
            ...prev,
            events: [...prev.events, ev],
            latest: ev,
          }))
        },
      })
      const ok = !sawError
      patchSync(channel.id, (prev) => ({
        ...prev,
        running: false,
        finalOk: ok,
      }))
      if (ok) scheduleHide(channel.id)
    } catch (e) {
      const err = e as Error
      const failureLabel = action === "sync" ? "同步失败" : "测试登录失败"
      patchSync(channel.id, (prev) => ({
        ...prev,
        running: false,
        finalOk: false,
        latest: {
          stage: "error",
          message: err.message || failureLabel,
          time: new Date().toISOString(),
        },
      }))
      // 失败保留，不调度自动隐藏
    } finally {
      refresh()
    }
  }

  async function withBusy(key: string, fn: () => Promise<unknown>) {
    setBusyAction(key)
    try {
      await fn()
      refresh()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "操作失败")
    } finally {
      setBusyAction(null)
    }
  }

  async function syncAll() {

    setBusyAction("sync-all")
    try {
      const result = await apiFetch<{ total: number; succeeded: number; failed: number }>("/channels/sync-all", { method: "POST" })
      toast.success(`已同步 ${result.succeeded}/${result.total} 个渠道${result.failed > 0 ? `，${result.failed} 个失败` : ""}`)
      refresh()
    } catch (e) {
      toast.error((e as Error).message || "批量同步失败")
    } finally {
      setBusyAction(null)
    }
  }

  function resetFilters() {
    setMonitorStatus("enabled")
    setManagementMode("all")
    setAccountCount("all")
    setChannelNameQuery("")
    setRemarkQuery("")
  }

  return (
    <section>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-baseline gap-3">
          <h2 className="text-base font-semibold text-foreground">{"渠道列表"}</h2>
          <p className="text-xs text-muted-foreground">{"监控状态、当前余额与最近同步结果"}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2 sm:gap-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <Input value={channelNameQuery} onChange={(event) => setChannelNameQuery(event.target.value)} placeholder="筛选渠道名称" aria-label="按渠道名称筛选" className="h-11 w-40 pl-8 text-xs sm:h-9" />
          </div>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <Input value={remarkQuery} onChange={(event) => setRemarkQuery(event.target.value)} placeholder="筛选备注" aria-label="按渠道备注筛选" className="h-11 w-40 pl-8 text-xs sm:h-9" />
          </div>
          <Select value={monitorStatus} onValueChange={(value) => setMonitorStatus(value as MonitorStatusFilter)}>
            <SelectTrigger className="h-11 w-28 text-xs sm:h-9" aria-label="渠道监控状态筛选"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="enabled">监控中</SelectItem>
              <SelectItem value="disabled">已暂停</SelectItem>
              <SelectItem value="all">全部状态</SelectItem>
            </SelectContent>
          </Select>
          <Select value={managementMode} onValueChange={(value) => setManagementMode(value as ManagementModeFilter)}>
            <SelectTrigger className="h-11 w-28 text-xs sm:h-9" aria-label="渠道管理类型筛选"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部管理</SelectItem>
              <SelectItem value="auto">自动读取</SelectItem>
              <SelectItem value="manual">手动管理</SelectItem>
            </SelectContent>
          </Select>
          <Select value={accountCount} onValueChange={(value) => setAccountCount(value as AccountCountFilter)}>
            <SelectTrigger className="h-11 w-32 text-xs sm:h-9" aria-label="渠道账号数量筛选"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部账号</SelectItem>
              <SelectItem value="multi">多账号渠道</SelectItem>
              <SelectItem value="single">单账号渠道</SelectItem>
            </SelectContent>
          </Select>
          <Button type="button" size="sm" variant="outline" className="h-11 gap-1.5 text-xs sm:h-9" disabled={!hasActiveFilters} onClick={resetFilters}>
            <RotateCcw className="size-3.5" />
            重置
          </Button>
          <span className="text-xs text-muted-foreground">
            {filteredChannels.length}{filteredChannels.length === channels?.length ? " 个渠道" : ` / ${channels?.length ?? 0} 个渠道`}
          </span>
          <Button
            size="sm"
            variant="outline"
            className="h-11 gap-1.5 text-xs sm:h-9"
            disabled={busyAction === "sync-all" || channels == null || channels.length === 0}
            onClick={() => void syncAll()}
          >
            <RefreshCw className={cn("size-3.5", busyAction === "sync-all" && "animate-spin")} />
            {"同步全部"}
          </Button>
          <Button
            size="sm"
            className="h-11 gap-1.5 text-xs sm:h-9"
            onClick={() => {
              setEditing(null)
              setCreating(true)
            }}
          >
            <Plus className="size-3.5" />
            {"新增"}
          </Button>
        </div>
      </div>

      {error ? (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>{"渠道信息读取失败"}</AlertTitle>
          <AlertDescription>
            <p>{error}</p>
            <Button type="button" size="sm" variant="outline" className="mt-2 h-11 sm:h-9" onClick={refetch}>
              <RefreshCw className="size-3.5" />
              {"重新读取"}
            </Button>
          </AlertDescription>
        </Alert>
      ) : loading ? (
        <p className="rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm text-muted-foreground">
          {"加载中…"}
        </p>
      ) : !channels || channels.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border px-4 py-10 text-center">
          <p className="text-sm text-muted-foreground">{"还没有任何渠道。"}</p>
          <Button
            size="sm"
            className="mt-3 h-11 gap-1.5 sm:h-9"
            onClick={() => {
              setEditing(null)
              setCreating(true)
            }}
          >
            <Plus className="size-3.5" />
            {"添加第一个渠道"}
          </Button>
        </div>
      ) : filteredChannels.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border px-4 py-10 text-center">
          <p className="text-sm text-muted-foreground">当前筛选没有渠道。</p>
          <Button type="button" size="sm" variant="outline" className="mt-3 h-11 sm:h-9" onClick={resetFilters}>
            清除筛选
          </Button>
        </div>
      ) : (
        <div className="grid grid-cols-1 items-stretch gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-3">
          {filteredChannels.map((c) => {
            const metric = metricByChannel.get(c.id)
            const currentBalance = c.balance_mode === "manual" ? (metric?.current_balance ?? c.manual_balance) : c.last_balance
			const accounts = channelAccounts(c)
            const status = statusOf(c, currentBalance)
            const meta = statusMap[status]
            return (
              <Card key={c.id} className="@container flex h-full flex-col gap-0 border border-border p-4 shadow-none">
                <div className="flex flex-wrap items-center gap-2 border-b border-border pb-3">
                  <a
                    href={c.site_url}
                    target="_blank"
                    rel="noreferrer"
                    className="text-sm font-semibold text-foreground hover:text-brand hover:underline"
                  >
                    {c.name}
                  </a>
                  {c.latest_ratio_changed_at ? (
                    <span className="text-[10px] text-muted-foreground" title="最近一次分组倍率变动">
                      {relativeTime(c.latest_ratio_changed_at)}
                    </span>
                  ) : null}
                  <div className="ml-auto flex shrink-0 items-center gap-2">
                    {accounts.length > 1 ? (
                      <span className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset bg-cyan-500/10 text-cyan-700 ring-cyan-500/20 dark:text-cyan-400">
                        多账号渠道
                      </span>
                    ) : null}
                    <span className={cn("inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset", c.balance_mode === "manual" ? "bg-amber-400/15 text-amber-700 ring-amber-500/30 dark:text-amber-400" : "bg-sky-500/10 text-sky-700 ring-sky-500/20 dark:text-sky-400")}>
                      {c.balance_mode === "manual" ? "手动管理" : "自动读取"}
                    </span>
                    <span
                      className={cn(
                        "inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset",
                        c.type === "sub2api"
                          ? "bg-emerald-500/10 text-emerald-700 ring-emerald-500/20 dark:text-emerald-400"
                          : c.type === "newapi"
                            ? "bg-brand/10 text-brand ring-brand/20"
                            : "bg-foreground/5 text-foreground ring-border",
                      )}
                    >
                      {channelTypeLabel(c.type)}
                    </span>
                    {!c.monitor_enabled ? (
                      <span className="inline-flex items-center rounded bg-muted/60 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                        {"已暂停"}
                      </span>
                    ) : null}
                  </div>
                </div>

                <div className="mt-3 grid grid-cols-1 gap-px overflow-hidden rounded-md border border-border bg-border @sm:grid-cols-2">
                  <Row label="站点">
                    <a href={c.site_url} target="_blank" rel="noreferrer" title={c.site_url} className="hover:text-brand hover:underline">
                      {siteHost(c.site_url)}
                    </a>
                  </Row>
				  {c.balance_mode !== "manual" ? <Row label="账号"><AccountPreview accounts={accounts} /></Row> : null}
                  <Row label="管理">{c.balance_mode === "manual" ? "手动余额" : "自动读取"}</Row>
                  {c.balance_mode !== "manual" ? <Row label="凭据">{c.credential_mode === "token" ? "Token" : "账号密码"}</Row> : null}
				  <Row label="余额"><span className={cn("font-mono text-base font-bold tabular-nums", balanceTone({ ...c, last_balance: currentBalance }))}>{metrics.loading && c.balance_mode === "manual" ? "读取中…" : c.balance_mode === "auto" ? <AccountBalance accounts={accounts} total={currentBalance} /> : money(currentBalance)}</span></Row>
                  <Row label="区间消耗"><span className="font-mono font-bold tabular-nums text-warning" title={c.balance_mode === "manual" ? `${usageRanges.find((item) => item.value === usageRange)?.label ?? usageRange}归属账号的上游成本消耗` : `${usageRanges.find((item) => item.value === usageRange)?.label ?? usageRange}余额下降累计，充值不计入`}>{metrics.loading && !metric ? "读取中…" : money(metric?.consumption_amount)}</span></Row>
                  <Row label="累计充值"><span className="font-mono tabular-nums">{metrics.loading && !metric ? "读取中…" : cny(metric?.cumulative_recharge_amount)}</span></Row>
                  <Row label="区间用户扣费"><span className={cn("font-mono tabular-nums", metric && !metric.user_charge_complete && "text-warning")} title={metric ? `${usageRanges.find((item) => item.value === usageRange)?.label ?? usageRange} · 匹配 ${metric.matched_account_count} 个归属账号${metric.user_charge_complete ? "" : "，部分账号统计失败"}` : metrics.error ?? ""}>{metrics.loading && !metric ? "读取中…" : charge(metric?.user_charge_amount)}</span></Row>
                  <Row label="阈值">{c.balance_threshold > 0 ? money(c.balance_threshold) : "未设置"}</Row>
                  <Row label="状态">
                    <span className={cn("inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium", meta.cls)}>
                      {meta.label}
                    </span>
                  </Row>
                  <Row label="备注"><span title={c.remark || "未填写备注"}>{c.remark || "未填写"}</span></Row>
                  <Row label="上次更新">{relativeTime(c.last_balance_at ?? c.updated_at)}</Row>
                  {c.last_error ? (
                    <Row label="错误信息" className="col-span-full">
                      <p className="truncate text-[11px] font-medium text-danger" title={c.last_error}>
                        {c.last_error.length > 80 ? c.last_error.slice(0, 80) + "…" : c.last_error}
                      </p>
                    </Row>
                  ) : null}
                </div>

                <ChannelLatencyTrend samples={latencyByChannel.get(c.id) ?? []} />
                {c.balance_mode !== "manual" ? <div className="mt-3 flex-1 border-t border-border"><InlineRates channelID={c.id} /></div> : <ManualRatesEditor channelID={c.id} />}

				<div className="mt-3 grid grid-cols-3 gap-2">
				  <Button
                    variant="outline"
                    size="sm"
                    className="h-11 gap-1 text-xs sm:h-9"
                    disabled={!!syncState[c.id]?.running}
                    onClick={() => startStream(c, "sync")}
                  >
                    <RefreshCw
                      className={cn("size-3", syncState[c.id]?.running && "animate-spin")}
                    />
                    {c.balance_mode === "manual" ? "计算余额" : "同步"}
                  </Button>
                  {c.balance_mode !== "manual" ? <Button
                    variant="outline"
                    size="sm"
                    className="h-11 gap-1 text-xs sm:h-9"
                    disabled={!!syncState[c.id]?.running}
                    onClick={() => startStream(c, "test-login")}
                  >
                    <LogIn className="size-3" />
                    {"测试登录"}
                  </Button> : <Button variant="outline" size="sm" className="h-11 gap-1 text-xs sm:h-9" disabled>无需登录</Button>}
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-11 gap-1 text-xs sm:h-9"
                    onClick={() => {
                      setEditing(c)
                      setCreating(true)
                    }}
                  >
					<Pencil className="size-3" />
					{c.balance_mode === "auto" ? "渠道 / 账号" : "编辑"}
                  </Button>
                </div>

                <SyncProgressStrip state={syncState[c.id] ?? emptySyncState()} />

                <div className="mt-3 flex items-center justify-between gap-2 border-t border-border pt-2.5">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-11 gap-1 text-xs text-muted-foreground sm:h-9"
                    disabled={busyAction === `toggle-${c.id}`}
                    onClick={() =>
                      withBusy(`toggle-${c.id}`, () =>
                        apiFetch(`/channels/${c.id}/${c.monitor_enabled ? "disable" : "enable"}`, {
                          method: "POST",
                        }),
                      )
                    }
                  >
                    {c.monitor_enabled ? (
                      <Pause className="size-3" />
                    ) : (
                      <Play className="size-3" />
                    )}
                    {c.monitor_enabled ? "暂停监控" : "恢复监控"}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-11 gap-1 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive sm:h-9"
                    disabled={busyAction === `delete-${c.id}`}
                    onClick={async () => {
                      const ok = await confirm({
                        title: `删除渠道 ${c.name}？`,
                        description: "删除后该渠道的余额历史、倍率快照与登录凭据都将一并清除，且无法恢复。",
                        confirmLabel: "删除",
                        destructive: true,
                      })
                      if (!ok) return
                      void withBusy(`delete-${c.id}`, () =>
                        apiFetch(`/channels/${c.id}`, { method: "DELETE" }),
                      )
                    }}
                  >
                    <Trash2 className="size-3" />
                    {"删除"}
                  </Button>
                </div>
              </Card>
            )
          })}
        </div>
      )}

      <ChannelFormDialog
        open={creating}
        onOpenChange={(v) => {
          setCreating(v)
          if (!v) setEditing(null)
        }}
        channel={editing}
      />

      {confirmDialog}
    </section>
  )
}
