"use client"

import { ArrowDownRight, ArrowUpRight } from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useDashboardSummary, useChannels } from "@/lib/queries"
import { channelTypeLabel, ratioDelta, relativeTime, shortTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useMemo } from "react"
import type { RelayUsageRange } from "@/lib/api-types"

export function MultiplierChanges({ range = "today" }: { range?: RelayUsageRange }) {
  const summary = useDashboardSummary(range)
  const channels = useChannels()

  const channelMap = useMemo(() => {
    const m = new Map<number, { name: string; type: string }>()
    for (const c of channels.data ?? []) m.set(c.id, { name: c.name, type: c.type })
    return m
  }, [channels.data])

  const items = summary.data?.recent_rate_changes ?? []

  return (
    <Card className="h-[clamp(18rem,60dvh,25rem)] min-h-0 overflow-hidden border border-border shadow-none">
      <CardHeader className="flex shrink-0 flex-row items-start justify-between gap-3 pb-2">
        <div><CardTitle className="text-base font-semibold">{"最近倍率变动"}</CardTitle><p className="mt-1 text-xs text-muted-foreground">{"所选时间范围内最近 20 条"}</p></div>
        <span className="text-xs text-muted-foreground">{items.length > 0 ? `${items.length} 条` : ""}</span>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 overflow-hidden px-0">
        {summary.loading ? (
          <p className="px-6 py-6 text-xs text-muted-foreground">{"加载中…"}</p>
        ) : items.length === 0 ? (
          <p className="px-6 py-6 text-xs text-muted-foreground">{"暂无倍率变动记录"}</p>
        ) : (
          <ScrollArea type="hover" className="h-full min-h-0">
            <ul className="divide-y divide-border">
              {items.map((m) => {
                const ch = channelMap.get(m.channel_id)
                const delta = ratioDelta(m.old_ratio, m.new_ratio)
                const isUp = delta.direction === "up"
                const chType = ch?.type ?? ""
                return (
                  <li key={m.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 px-4 py-3.5 sm:grid-cols-[auto_auto_minmax(0,1fr)_auto] sm:px-6">
                    <div className="col-start-1 row-start-1 flex flex-col items-center gap-0.5 pt-1">
                      <span className={cn("size-2 rounded-full", isUp ? "bg-danger" : "bg-success")} />
                    </div>
                    <div className="col-start-2 row-start-1 shrink-0 text-xs leading-relaxed text-muted-foreground">
                      <p>{relativeTime(m.changed_at)}</p>
                      <p>{shortTime(m.changed_at)}</p>
                    </div>

                    <div className="col-span-2 col-start-2 row-start-2 min-w-0 sm:col-span-1 sm:col-start-3 sm:row-start-1">
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        <span className="min-w-0 break-all text-sm font-semibold text-foreground">{m.model_name}</span>
                        <span
                          className={cn(
                            "inline-flex max-w-full min-w-0 items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset",
                            chType === "newapi"
                              ? "bg-brand/10 text-brand ring-brand/20"
                              : "bg-foreground/5 text-foreground ring-border",
                          )}
                        >
                          <span className="min-w-0 truncate">{ch?.name ?? `#${m.channel_id}`}</span>
                          {chType ? <span className="ml-1 shrink-0 opacity-60">{channelTypeLabel(chType)}</span> : null}
                        </span>
                      </div>
                      <div className="mt-1.5 flex items-center text-xs">
                        <div>
                          <span className="text-muted-foreground">{"倍率"}</span>
                          <p className="mt-0.5 tabular-nums">
                            <span className="text-muted-foreground">
                              {m.old_ratio == null ? "—" : m.old_ratio.toFixed(3)}
                            </span>
                            <span className="mx-1 text-muted-foreground">{"→"}</span>
                            <span className={cn("font-medium", isUp ? "text-danger" : "text-success")}>
                              {m.new_ratio.toFixed(3)}
                            </span>
                          </p>
                        </div>
                      </div>
                    </div>

                    <div className="col-start-3 row-start-1 shrink-0 pt-0.5 sm:col-start-4">
                      <span
                        className={cn(
                          "inline-flex items-center gap-0.5 rounded-md px-2 py-1 text-xs font-semibold",
                          isUp ? "bg-danger/10 text-danger" : "bg-success/10 text-success",
                        )}
                      >
                        {isUp ? <ArrowUpRight className="size-3" /> : <ArrowDownRight className="size-3" />}
                        {delta.pct}
                      </span>
                    </div>
                  </li>
                )
              })}
            </ul>
          </ScrollArea>
        )}
      </CardContent>
    </Card>
  )
}
