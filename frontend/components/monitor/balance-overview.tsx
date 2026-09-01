"use client"

import { useEffect, useRef, useState } from "react"
import { ExternalLink } from "lucide-react"
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis, CartesianGrid } from "recharts"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useBalanceTrend, useDashboardSummary } from "@/lib/queries"
import type { DashboardRange } from "@/lib/api-types"
import { money } from "@/lib/format"
import { cn } from "@/lib/utils"

function formatY(n: number) {
  if (n === 0) return "$0"
  if (n >= 1000) return `$${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}K`
  if (n >= 100) return `$${n.toFixed(0)}`
  return `$${n.toFixed(n >= 10 ? 1 : 2)}`
}

/**
 * niceCeil 把最大值向上取整到一个"好看的"刻度，避免曲线贴顶。
 * 例如 47 → 50；478 → 500；12,300 → 15,000。
 */
function niceCeil(n: number): number {
  if (!Number.isFinite(n) || n <= 0) return 10
  const padded = n * 1.15
  const mag = Math.pow(10, Math.floor(Math.log10(padded)))
  const norm = padded / mag
  const step = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10
  return step * mag
}

function formatDay(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return `${d.getMonth() + 1}月${d.getDate()}日`
}

function formatHour(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const now = new Date()
  const time = d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false })
  if (
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  ) {
    return time
  }
  return `${d.getMonth() + 1}/${d.getDate()} ${time}`
}

interface TooltipPayloadItem { value: number }

function ChartTooltip({ active, payload, label }: { active?: boolean; payload?: TooltipPayloadItem[]; label?: string }) {
  if (!active || !payload?.length) return null
  return (
    <div className="rounded-lg border border-border bg-popover px-3 py-2 shadow-md">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="text-sm font-semibold text-foreground">
        {"$"}{payload[0].value.toLocaleString("en-US")}
      </p>
    </div>
  )
}

export function BalanceOverview({ range = "today" }: { range?: DashboardRange }) {
  const trend = useBalanceTrend(range)
  const summary = useDashboardSummary(range)
  const chartRef = useRef<HTMLDivElement>(null)
  const [tooltipPinned, setTooltipPinned] = useState(false)

  useEffect(() => {
    if (!tooltipPinned) return

    const closeOnOutsideClick = (event: PointerEvent) => {
      if (event.target instanceof Node && chartRef.current?.contains(event.target)) return
      setTooltipPinned(false)
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setTooltipPinned(false)
    }

    document.addEventListener("pointerdown", closeOnOutsideClick, true)
    document.addEventListener("keydown", closeOnEscape)
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsideClick, true)
      document.removeEventListener("keydown", closeOnEscape)
    }
  }, [tooltipPinned])

  const data = (trend.data ?? []).map((p) => ({
    day: range === "today" || range === "24h" ? formatHour(p.day) : formatDay(p.day),
    balance: p.balance,
  }))

  const channels = (summary.data?.channels ?? []).filter((channel) => channel.monitor_enabled)
  const yMax = data.length > 0 ? niceCeil(Math.max(...data.map((d) => d.balance))) : 10

  return (
    <Card className="border border-border shadow-none lg:h-100">
      <CardHeader className="flex shrink-0 flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"渠道余额趋势"}</CardTitle>
        <span className="shrink-0 rounded-md border border-border bg-muted/30 px-2 py-1 text-xs text-foreground">{range === "today" ? "今天" : range === "24h" ? "24 小时" : range === "7d" ? "7 天" : "30 天"}</span>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col">
        <div ref={chartRef} className="min-h-0 w-full flex-1">
          {trend.loading ? (
            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">{"加载中…"}</div>
          ) : data.length === 0 ? (
            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
              {range === "today" ? "今天暂无余额采样，等待下次扫描或手动刷新" : range === "24h" ? "暂无 24 小时余额采样，等待下次扫描或手动刷新" : "暂无余额采样，等待下次扫描或手动刷新"}
            </div>
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <LineChart
                data={data}
                margin={{ top: 8, right: 12, left: 0, bottom: 0 }}
                onClick={(state) => setTooltipPinned((state.activeTooltipIndex ?? -1) >= 0)}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis
                  dataKey="day"
                  tickLine={false}
                  axisLine={false}
                  tick={{ fill: "var(--muted-foreground)", fontSize: 11 }}
                  dy={8}
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={48}
                  tick={{ fill: "var(--muted-foreground)", fontSize: 11 }}
                  tickFormatter={formatY}
                  domain={[0, yMax]}
                />
                <Tooltip
                  active={tooltipPinned || undefined}
                  content={<ChartTooltip />}
                  cursor={{ stroke: "var(--border)", strokeDasharray: "4 4" }}
                />
                <Line
                  type="monotone"
                  dataKey="balance"
                  stroke="var(--brand)"
                  strokeWidth={2}
                  dot={{ r: 4, fill: "var(--background)", stroke: "var(--brand)", strokeWidth: 2 }}
                  activeDot={{ r: 5, fill: "var(--brand)", strokeWidth: 0 }}
                />
              </LineChart>
            </ResponsiveContainer>
          )}
        </div>

        {/* per-channel chips */}
        {channels.length > 0 ? (
          <div className="mt-3 flex shrink-0 flex-wrap items-center gap-x-5 gap-y-2 border-t border-border pt-3">
            {channels.map((c) => {
              const isFailed = !!c.last_error
              const isUnknown = c.last_balance == null
              return (
                <a
                  key={c.id}
                  href={c.site_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  aria-label={`在新标签页打开渠道 ${c.name}`}
                  title={c.site_url}
                  className="group inline-flex min-h-11 cursor-pointer items-center gap-1.5 rounded px-1 text-xs transition-colors duration-200 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 sm:min-h-8"
                >
                  <span
                    className={cn(
                      "size-2 rounded-full",
                      isFailed ? "bg-danger" : isUnknown ? "bg-muted-foreground/40" : "bg-success",
                    )}
                  />
                  <span className="font-medium text-foreground transition-colors group-hover:text-brand">{c.name}</span>
                  <span className="tabular-nums text-muted-foreground">{money(c.last_balance)}</span>
                  <ExternalLink className="size-3 shrink-0 text-muted-foreground transition-colors group-hover:text-brand" aria-hidden="true" />
                </a>
              )
            })}
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}
