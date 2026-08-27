import { useEffect, useMemo, useState } from "react"
import { ChevronDown, History, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { RelayAdjustmentView } from "@/lib/api-types"
import { relativeTime } from "@/lib/format"
import { isRelayGroupAdjustment, matchesRelayAdjustmentFilter, relayAdjustmentActionLabel, relayAdjustmentDetail, relayAdjustmentTabs, type RelayAdjustmentFilter } from "@/lib/relay-adjustments"
import { cn } from "@/lib/utils"

interface RelayAdjustmentLogProps {
  rows: RelayAdjustmentView[]
  title?: string
  storageKey?: string
  showStation?: boolean
  refreshing?: boolean
}

export function RelayAdjustmentLog({ rows, title = "调整记录", storageKey = "uh_relay_adjustment_log_open", showStation = false, refreshing = false }: RelayAdjustmentLogProps) {
  const [open, setOpen] = useState(() => {
    try {
      const stored = window.localStorage.getItem(storageKey)
      return stored === "true" || stored === "false" ? stored === "true" : true
    } catch {
      return true
    }
  })
  const [filter, setFilter] = useState<RelayAdjustmentFilter>("all")

  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, String(open))
    } catch {
      // Disclosure state can remain session-only when storage is unavailable.
    }
  }, [open, storageKey])

  const filteredRows = useMemo(() => rows.filter((row) => matchesRelayAdjustmentFilter(row, filter)), [filter, rows])

  return <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
    <CardHeader className="gap-3 px-4 py-3">
      <div className="flex flex-row items-center justify-between gap-3">
        <div className="min-w-0">
          <CardTitle className="flex items-center gap-2 text-sm font-semibold"><History className="size-4 text-brand" />{title}</CardTitle>
          {open ? <p className="mt-1 text-xs text-muted-foreground">调组、调度开关和优先级变更均有记录；每类最多展示最近 50 条。</p> : null}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {open ? <span className="text-xs text-muted-foreground">{filteredRows.length} / {rows.length} 条</span> : null}
          <Button type="button" variant="ghost" size="icon" className="size-9" aria-label={open ? `收起${title}` : `展开${title}`} aria-expanded={open} onClick={() => setOpen((value) => !value)}>
            <ChevronDown className={cn("size-4 transition-transform duration-200", open && "rotate-180")} />
          </Button>
        </div>
      </div>
      {open ? <Tabs value={filter} onValueChange={(value) => setFilter(value as RelayAdjustmentFilter)}><TabsList className="h-8 max-w-full overflow-x-auto rounded-md p-0.5"><TabsTrigger value="all" className="h-7 px-2 text-xs">全部</TabsTrigger>{relayAdjustmentTabs.slice(1).map((tab) => <TabsTrigger key={tab.value} value={tab.value} className="h-7 px-2 text-xs">{tab.label}</TabsTrigger>)}</TabsList></Tabs> : null}
    </CardHeader>
    {open ? <CardContent className="relative h-[min(46vh,480px)] border-t border-border p-0">
      <div className="h-full overflow-auto">
        <div className="divide-y divide-border">
          {filteredRows.length === 0 ? <p className="px-4 py-8 text-center text-sm text-muted-foreground">当前筛选没有调整记录</p> : filteredRows.map((row) => <div key={`${row.relay_station_id}-${row.id}`} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3 text-xs">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-foreground">{row.account_name}</span>
                {showStation ? <span className="text-muted-foreground">{row.relay_station_name || `中转站 #${row.relay_station_id}`} · {row.account_platform || "-"}</span> : <span className="text-muted-foreground">{row.account_platform || "-"}</span>}
                <span className={cn("rounded px-1.5 py-0.5", row.success ? "bg-success/10 text-success" : "bg-danger/10 text-danger")}>{row.success ? "已完成" : "失败"}</span>
                <span className={cn("rounded px-1.5 py-0.5 font-medium", row.source === "auto" ? "bg-brand/10 text-brand" : "bg-muted text-muted-foreground")}>{row.source === "auto" ? "自动" : "手动"}</span>
                <span className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground">{relayAdjustmentActionLabel(row)}</span>
              </div>
              <p className="mt-1 break-words text-muted-foreground">{relayAdjustmentDetail(row)}</p>
              {isRelayGroupAdjustment(row) ? <p className="mt-1 text-muted-foreground">成本 {(row.cost_multiplier ?? 0).toFixed(3)} · 账号 #{row.relay_account_external_id}</p> : null}
            </div>
            <div className="text-right text-muted-foreground">
              <p>{relativeTime(row.applied_at)}</p>
              {row.error_message ? <p className="max-w-[360px] truncate text-danger" title={row.error_message}>{row.error_message}</p> : null}
            </div>
          </div>)}
        </div>
      </div>
      {refreshing ? <div className="pointer-events-none absolute inset-x-0 top-10 z-20 flex justify-center"><span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><RefreshCw className="size-3.5 animate-spin text-brand" />正在刷新调整记录</span></div> : null}
    </CardContent> : null}
  </Card>
}
