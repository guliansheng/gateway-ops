"use client"

import { useMemo } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useChannels, useDashboardSummary } from "@/lib/queries"
import { relativeTime, shortTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { RelayUsageRange } from "@/lib/api-types"

export function RecentGroups({ range = "today" }: { range?: RelayUsageRange }) {
  const summary = useDashboardSummary(range)
  const channels = useChannels()

  const channelMap = useMemo(() => {
    const map = new Map<number, { name: string; type: string; site_url: string }>()
    for (const channel of channels.data ?? []) map.set(channel.id, { name: channel.name, type: channel.type, site_url: channel.site_url })
    return map
  }, [channels.data])

  const items = summary.data?.recent_groups ?? []

  return (
    <Card role="region" aria-label="最近新增分组" className="h-[clamp(18rem,60dvh,25rem)] min-h-0 min-w-0 overflow-hidden border border-border shadow-none">
      <CardHeader className="flex shrink-0 flex-row items-start justify-between gap-3 pb-2">
        <div>
          <CardTitle className="text-base font-semibold">最近新增分组</CardTitle>
          <p className="mt-1 text-xs text-muted-foreground">所选时间范围内最近 50 条，按首次发现时间排序</p>
        </div>
        <span className="shrink-0 text-xs text-muted-foreground">{items.length > 0 ? `${items.length} 条` : ""}</span>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 overflow-hidden px-0">
        {summary.loading ? (
          <p className="px-6 py-6 text-xs text-muted-foreground">加载中…</p>
        ) : summary.error && !summary.data ? (
          <p role="alert" className="px-6 py-6 text-xs text-danger">读取新增分组失败：{summary.error}</p>
        ) : items.length === 0 ? (
          <p className="px-6 py-6 text-xs text-muted-foreground">暂无新增分组记录</p>
        ) : (
          <ScrollArea type="hover" className="h-full min-h-0">
            <ul className="divide-y divide-border">
              {items.map((group) => {
                const channel = channelMap.get(group.channel_id)
                const channelType = channel?.type ?? ""
                const channelName = channel?.name ?? `#${group.channel_id}`
                return (
                  <li key={group.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 px-4 py-3.5 sm:grid-cols-[auto_auto_minmax(0,1fr)_auto] sm:px-6">
                    <span className="col-start-1 row-start-1 mt-1.5 size-2 rounded-full bg-brand" />
                    <div className="col-start-2 row-start-1 shrink-0 text-xs leading-relaxed text-muted-foreground">
                      <p>{relativeTime(group.first_seen_at)}</p>
                      <p>{shortTime(group.first_seen_at)}</p>
                    </div>
                    <div className="col-span-2 col-start-2 row-start-2 min-w-0 sm:col-span-1 sm:col-start-3 sm:row-start-1">
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        <span className="min-w-0 break-all text-sm font-semibold text-foreground">{group.model_name}</span>
                      </div>
                      {group.description ? <p className="mt-1 break-words text-xs text-muted-foreground">{group.description}</p> : null}
                      <p className="mt-1.5 text-xs text-muted-foreground">当前倍率 <span className="font-medium tabular-nums text-foreground">{group.ratio.toFixed(3)}</span></p>
                    </div>
                    <span
                      title={channelName}
                      className={cn(
                        "col-start-3 row-start-1 inline-flex max-w-32 min-w-0 items-center justify-self-end rounded-md px-2 py-1 text-xs font-semibold ring-1 ring-inset sm:col-start-4 sm:max-w-40",
                        channelType === "sub2api"
                          ? "bg-success/10 text-success ring-success/20"
                          : channelType === "newapi"
                            ? "bg-brand/10 text-brand ring-brand/20"
                            : "bg-foreground/5 text-foreground ring-border",
                      )}
                    >
                      {channel?.site_url ? (
                        <a
                          href={channel.site_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          aria-label={`在新标签页打开渠道 ${channelName}`}
                          className="min-w-0 truncate rounded-sm hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          {channelName}
                        </a>
                      ) : (
                        <span className="min-w-0 truncate">{channelName}</span>
                      )}
                    </span>
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
