"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useChannels, useChannelRates } from "@/lib/queries"
import { channelTypeLabel, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Channel } from "@/lib/api-types"

/**
 * 按倍率给 chip 上色：
 *   < 1   折扣 → 蓝
 *   1 ~ 1.2 正常 → 默认
 *   > 1.2 加价 → 黄
 */
function ratioTone(r: number): string {
  if (r < 1) return "bg-brand/10 text-brand ring-brand/20"
  if (r > 1.2) return "bg-warning/10 text-warning ring-warning/20"
  return "bg-muted text-foreground ring-border"
}

function ChannelRateRow({ channel }: { channel: Channel }) {
  const { data, loading } = useChannelRates(channel.id)
  const rates = [...(data ?? [])].sort((a, b) => a.ratio - b.ratio)
  const latest = rates[0]?.last_seen_at

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border p-3 sm:flex-row sm:items-start sm:gap-4">
      {/* 左：渠道名 + 类型 */}
      <div className="flex shrink-0 items-center gap-2 sm:w-44 sm:flex-col sm:items-start">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold text-foreground">{channel.name}</span>
          <span
            className={cn(
              "inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset",
              channel.type === "newapi"
                ? "bg-brand/10 text-brand ring-brand/20"
                : "bg-foreground/5 text-foreground ring-border",
            )}
          >
            {channelTypeLabel(channel.type)}
          </span>
        </div>
        <span className="text-[11px] text-muted-foreground">
          {rates.length > 0
            ? `${rates.length} 个分组 · ${relativeTime(latest)}`
            : "暂无数据"}
        </span>
      </div>

      {/* 右：倍率 chip 流 */}
      <div className="min-w-0 flex-1">
        {loading ? (
          <p className="text-xs text-muted-foreground">{"加载中…"}</p>
        ) : rates.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {channel.last_error
              ? channel.last_error
              : "尚未采集，点渠道卡片的 [同步] 可立即拉取"}
          </p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {rates.map((r) => {
              const updated = `最近更新：${relativeTime(r.last_seen_at)}`
              const chip = (
                <span
                  key={r.id}
                  className={cn(
                    "inline-flex cursor-default items-center gap-1.5 rounded-md px-2 py-0.5 text-xs ring-1 ring-inset transition-colors",
                    "hover:bg-muted/60",
                    ratioTone(r.ratio),
                  )}
                >
                  <span className="font-medium">{r.model_name}</span>
                  <span className="font-semibold tabular-nums">{r.ratio.toFixed(3)}</span>
                </span>
              )
              return (
                <Tooltip key={r.id} delayDuration={150}>
                  <TooltipTrigger asChild>{chip}</TooltipTrigger>
                  <TooltipContent side="top" className="max-w-xs text-xs">
                    <p className="font-medium">{r.model_name}</p>
                    {r.description ? (
                      <p className="mt-0.5 text-muted-foreground">{r.description}</p>
                    ) : (
                      <p className="mt-0.5 text-muted-foreground italic">(无描述)</p>
                    )}
                    <p className="mt-0.5 text-muted-foreground">{updated}</p>
                  </TooltipContent>
                </Tooltip>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

export function ChannelRatesPanel() {
  const { data: channels, loading } = useChannels()
  const list = channels ?? []

  return (
    <Card className="border border-border shadow-none">
      <CardHeader className="flex flex-row items-baseline justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"分组倍率"}</CardTitle>
        <span className="text-xs text-muted-foreground">
          {"每个渠道的当前可用分组及倍率（蓝=折扣 · 黄=加价）"}
        </span>
      </CardHeader>
      <CardContent className="space-y-2">
        {loading ? (
          <p className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-xs text-muted-foreground">
            {"加载中…"}
          </p>
        ) : list.length === 0 ? (
          <p className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-xs text-muted-foreground">
            {"还没有渠道"}
          </p>
        ) : (
          list.map((c) => <ChannelRateRow key={c.id} channel={c} />)
        )}
      </CardContent>
    </Card>
  )
}
