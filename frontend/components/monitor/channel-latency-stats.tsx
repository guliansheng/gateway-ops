"use client"

import { useMemo, useState } from "react"
import { BarChart3 } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { cn } from "@/lib/utils"
import type { RateSnapshot, RelayLatencySample } from "@/lib/api-types"

export type LatencyTone = "good" | "warn" | "slow" | "critical"

export interface LatencyStats {
  count: number
  averageFirst: number
  averageDuration: number
  p50First: number
  p50Duration: number
  p95Duration: number
  latestAt?: string
  tone: LatencyTone
}

export interface LatencyStatsRow extends LatencyStats {
  groupName: string
  modelName: string
  samples: RelayLatencySample[]
}

const toneLabel: Record<LatencyTone, string> = {
  good: "流畅",
  warn: "关注",
  slow: "较慢",
  critical: "高延迟",
}

const toneClass: Record<LatencyTone, string> = {
  good: "bg-emerald-500/10 text-emerald-700 ring-emerald-500/25 dark:text-emerald-400",
  warn: "bg-amber-500/10 text-amber-700 ring-amber-500/25 dark:text-amber-400",
  slow: "bg-orange-500/10 text-orange-700 ring-orange-500/25 dark:text-orange-400",
  critical: "bg-red-500/10 text-red-700 ring-red-500/25 dark:text-red-400",
}

const toneDotClass: Record<LatencyTone, string> = {
  good: "bg-emerald-500",
  warn: "bg-amber-500",
  slow: "bg-orange-500",
  critical: "bg-red-500",
}

const latencyColor: Record<LatencyTone, string> = {
  good: "#10b981",
  warn: "#f59e0b",
  slow: "#f97316",
  critical: "#ef4444",
}

function validSamples(samples: RelayLatencySample[]) {
  return samples.filter((sample) => (
    Number.isFinite(sample.first_token_ms) && sample.first_token_ms >= 0 &&
    Number.isFinite(sample.duration_ms) && sample.duration_ms >= 0
  ))
}

function percentile(values: number[], ratio: number) {
  if (values.length === 0) return 0
  const ordered = [...values].sort((a, b) => a - b)
  const index = Math.min(ordered.length - 1, Math.max(0, Math.ceil(ordered.length * ratio) - 1))
  return ordered[index]
}

export function latencyToneForMetric(value: number, metric: "first" | "duration"): LatencyTone {
  if (!Number.isFinite(value) || value < 0) return "critical"
  const thresholds = metric === "first"
    ? { warn: 10_000, slow: 30_000, critical: 60_000 }
    : { warn: 60_000, slow: 180_000, critical: 300_000 }
  if (value >= thresholds.critical) return "critical"
  if (value >= thresholds.slow) return "slow"
  if (value >= thresholds.warn) return "warn"
  return "good"
}

export function latencyTone(value: number): LatencyTone {
  return latencyToneForMetric(value, "duration")
}

function toneSeverity(tone: LatencyTone) {
  return tone === "critical" ? 3 : tone === "slow" ? 2 : tone === "warn" ? 1 : 0
}

function combinedLatencyTone(first: number, duration: number): LatencyTone {
  const firstTone = latencyToneForMetric(first, "first")
  const durationTone = latencyToneForMetric(duration, "duration")
  return toneSeverity(firstTone) >= toneSeverity(durationTone) ? firstTone : durationTone
}

function average(values: number[]) {
  return values.length === 0 ? 0 : values.reduce((total, value) => total + value, 0) / values.length
}

function normalize(value: string | undefined) {
  return (value ?? "").trim().toLocaleLowerCase()
}

function formatLatency(value: number) {
  if (!Number.isFinite(value) || value < 0) return "-"
  if (value < 1_000) return `${Math.round(value)}ms`
  if (value < 60_000) return `${(value / 1_000).toFixed(value < 10_000 ? 1 : 0)}秒`
  return `${(value / 60_000).toFixed(1)}分`
}

function makeStats(samples: RelayLatencySample[]): LatencyStats | null {
  const valid = validSamples(samples)
  if (valid.length === 0) return null
  const first = valid.map((sample) => sample.first_token_ms)
  const duration = valid.map((sample) => sample.duration_ms)
  const latestAt = valid.reduce<string | undefined>((latest, sample) => {
    if (!latest || sample.created_at > latest) return sample.created_at
    return latest
  }, undefined)
  return {
    count: valid.length,
    averageFirst: average(first),
    averageDuration: average(duration),
    p50First: percentile(first, 0.5),
    p50Duration: percentile(duration, 0.5),
    p95Duration: percentile(duration, 0.95),
    latestAt,
    tone: combinedLatencyTone(percentile(first, 0.5), percentile(duration, 0.5)),
  }
}

function sampleGroupName(sample: RelayLatencySample) {
  return sample.group_name?.trim() || "未标记分组"
}

function sampleModelName(sample: RelayLatencySample) {
  return sample.model?.trim() || "未标记模型"
}

function matchesName(sample: RelayLatencySample, name: string) {
  const target = normalize(name)
  return normalize(sample.group_name) === target || normalize(sample.model) === target
}

export function latencyStatsForGroup(samples: RelayLatencySample[], groupName: string): LatencyStats | null {
  return makeStats(recentSamples(samples.filter((sample) => matchesName(sample, groupName))))
}

export function latencyStatsRows(samples: RelayLatencySample[]): LatencyStatsRow[] {
  const models = new Map<string, { samples: RelayLatencySample[]; groups: Set<string> }>()
  for (const sample of validSamples(samples)) {
    const groupName = sampleGroupName(sample)
    const modelName = sampleModelName(sample)
    const key = normalize(modelName)
    const current = models.get(key) ?? { samples: [], groups: new Set<string>() }
    current.samples.push(sample)
    current.groups.add(groupName)
    models.set(key, current)
  }
  return [...models.values()]
    .map(({ samples: modelSamples, groups }) => {
      const recent = recentSamples(modelSamples)
      return {
      ...makeStats(recent)!,
      groupName: [...groups].join(" / "),
      modelName: sampleModelName(recent[0]),
      samples: recent,
      }
    })
    .sort((a, b) => a.p50Duration - b.p50Duration || a.p50First - b.p50First || b.count - a.count)
}

function recentSamples(samples: RelayLatencySample[]) {
  return [...samples]
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .slice(0, 60)
}

// "最近调用"列固定 18rem（288px），按最多 60 条竖线 + 1px 间距计算单条固定宽度：
// (288 - 1 * 59) / 60 ≈ 3.82px。无论实际条数多少，每条都用这个宽度，不平分容器。
const BAR_AREA_WIDTH_PX = 288
const BAR_MAX_COUNT = 60
const BAR_GAP_PX = 1
const BAR_WIDTH_PX = (BAR_AREA_WIDTH_PX - BAR_GAP_PX * (BAR_MAX_COUNT - 1)) / BAR_MAX_COUNT

function LatencyBars({ samples }: { samples: RelayLatencySample[] }) {
  const ordered = [...samples].sort((a, b) => a.created_at.localeCompare(b.created_at))
  return (
    <div className="flex h-8 items-stretch gap-px" aria-label={`最近 ${samples.length} 次调用延迟`}>
      {ordered.map((sample, index) => {
        const first = latencyToneForMetric(sample.first_token_ms, "first")
        const duration = latencyToneForMetric(sample.duration_ms, "duration")
        return <span key={`${sample.created_at}-${index}`} className="shrink-0 rounded-[2px] opacity-90" title={`首字 ${formatLatency(sample.first_token_ms)} · 总耗时 ${formatLatency(sample.duration_ms)}`} style={{ width: `${BAR_WIDTH_PX}px`, background: `linear-gradient(to top, ${latencyColor[duration]} 0 50%, ${latencyColor[first]} 50% 100%)` }} />
      })}
    </div>
  )
}

function LatencyBadge({ stats, compact = false }: { stats: LatencyStats; compact?: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex w-fit shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset",
        toneClass[stats.tone],
      )}
      title={`近 ${stats.count} 次 · 平均首字 ${formatLatency(stats.averageFirst)} · 平均总耗时 ${formatLatency(stats.averageDuration)} · P95 ${formatLatency(stats.p95Duration)}`}
    >
      <span className={cn("size-1.5 rounded-full", toneDotClass[stats.tone])} aria-hidden="true" />
      {compact ? formatLatency(stats.p50Duration) : toneLabel[stats.tone]}
    </span>
  )
}

export function LatencyMarker({ stats }: { stats: LatencyStats | null }) {
  return stats ? <LatencyBadge stats={stats} compact /> : null
}

function StatCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-muted/20 px-3 py-2">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className="mt-1 font-mono text-sm font-semibold tabular-nums text-foreground">{value}</p>
    </div>
  )
}

export function ChannelLatencyStatsButton({ channelName, samples }: { channelName: string; samples: RelayLatencySample[] }) {
  const [open, setOpen] = useState(false)
  const stats = useMemo(() => makeStats(recentSamples(samples)), [samples])
  const rows = useMemo(() => latencyStatsRows(samples), [samples])

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-11 gap-1 text-xs sm:h-9"
        onClick={() => setOpen(true)}
      >
        <BarChart3 className="size-3" />
        统计
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-[1200px] sm:max-w-[1200px] overflow-x-auto overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{channelName} · 调用延迟统计</DialogTitle>
            <DialogDescription>渠道总览取最近 60 次调用；每个模型单独取最近 60 次，按延迟从低到高排序，优先展示更流畅的模型。</DialogDescription>
          </DialogHeader>
          {!stats ? (
            <div className="rounded-lg border border-dashed border-border px-4 py-10 text-center text-sm text-muted-foreground">暂无可用的模型调用延迟数据</div>
          ) : (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <StatCell label="有效调用" value={`${stats.count} 次`} />
                <StatCell label="平均首字" value={formatLatency(stats.averageFirst)} />
                <StatCell label="平均总耗时" value={formatLatency(stats.averageDuration)} />
                <StatCell label="P95 总耗时" value={formatLatency(stats.p95Duration)} />
              </div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                <span>分组/模型排序</span>
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-emerald-500" />流畅：首字&lt;10秒且总耗时&lt;60秒</span>
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-amber-500" />关注：首字≥10秒或总耗时≥60秒</span>
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-orange-500" />较慢：首字≥30秒或总耗时≥180秒</span>
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-red-500" />高延迟：首字≥60秒或总耗时≥300秒</span>
              </div>
              <div className="min-w-[1100px] overflow-hidden rounded-lg border border-border">
                <div className="grid grid-cols-[minmax(0,1.15fr)_minmax(0,1.15fr)_3.5rem_5rem_5rem_18rem_6rem] gap-3 bg-muted/40 px-3 py-2 text-[10px] font-medium text-muted-foreground">
                  <span>模型</span><span>分组</span><span>次数</span><span>P50 首字</span><span>P50 总耗时</span><span>最近调用</span><span>状态</span>
                </div>
                {rows.length === 0 ? <p className="px-3 py-6 text-center text-xs text-muted-foreground">暂无可用统计</p> : rows.map((row) => (
                  <div key={row.modelName} className="grid grid-cols-[minmax(0,1.15fr)_minmax(0,1.15fr)_3.5rem_5rem_5rem_18rem_6rem] items-center gap-3 border-t border-border px-3 py-2 text-[11px]">
                    <span className="min-w-0 truncate font-medium text-foreground" title={row.modelName}>{row.modelName}</span>
                    <span className="min-w-0 truncate text-muted-foreground" title={row.groupName}>{row.groupName}</span>
                    <span className="font-mono tabular-nums text-muted-foreground">{row.count}</span>
                    <span className="font-mono tabular-nums text-foreground">{formatLatency(row.p50First)}</span>
                    <span className="font-mono tabular-nums text-foreground">{formatLatency(row.p50Duration)}</span>
                    <LatencyBars samples={row.samples} />
                    <LatencyBadge stats={row} />
                  </div>
                ))}
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

export function latencyStatsForRate(samples: RelayLatencySample[], rate: RateSnapshot) {
  return latencyStatsForGroup(samples, rate.model_name)
}
