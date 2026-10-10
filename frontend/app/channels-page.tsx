import { useEffect, useState } from "react"
import { Clock3, LayoutList, Save } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { useSyncSettings } from "@/lib/queries"
import { apiFetch } from "@/lib/api"
import { ChannelCards } from "@/components/monitor/channel-cards"
import { KpiRow } from "@/components/monitor/kpi-row"
import { MultiplierChanges } from "@/components/monitor/multiplier-changes"
import type { RelayUsageRange } from "@/lib/api-types"
import { cn } from "@/lib/utils"

const intervals = [5, 10, 15, 30, 60, 180, 360, 720, 1440]
// 倍率探测间隔参考中转站“倍率探测”，支持秒级节奏。
const rateIntervals = [5, 10, 20, 30, 60, 300, 600, 1800, 3600, 18000, 43200, 86400]

function rateIntervalLabel(value: number) {
  if (value < 60) return `每 ${value} 秒`
  if (value === 86400) return "每天"
  if (value % 3600 === 0) return `每 ${value / 3600} 小时`
  return `每 ${value / 60} 分钟`
}
const ranges: { value: RelayUsageRange; label: string }[] = [
  { value: "today", label: "今天" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
  { value: "all", label: "全部" },
]

export default function ChannelsPage() {
  const settings = useSyncSettings()
  const [enabled, setEnabled] = useState(false)
  const [interval, setInterval] = useState(30)
  const [rateEnabled, setRateEnabled] = useState(false)
  const [rateInterval, setRateInterval] = useState(60)
  const [saving, setSaving] = useState(false)
  const [range, setRange] = useState<RelayUsageRange>("today")

  useEffect(() => {
    if (!settings.data) return
    setEnabled(settings.data.channel_enabled)
    setInterval(settings.data.channel_interval_minutes || 30)
    setRateEnabled(settings.data.channel_rate_enabled)
    setRateInterval(settings.data.channel_rate_interval_seconds || 60)
  }, [settings.data])

  async function save() {
    const current = settings.data
    if (!current) return
    setSaving(true)
    try {
      await apiFetch("/sync-settings", {
        method: "PUT",
        body: JSON.stringify({
          channel_enabled: enabled,
          channel_interval_minutes: interval,
          channel_rate_enabled: rateEnabled,
          channel_rate_interval_seconds: rateInterval,
          relay_rate_enabled: current.relay_rate_enabled,
          relay_rate_interval_minutes: current.relay_rate_interval_minutes,
          relay_rate_interval_seconds: current.relay_rate_interval_seconds,
          relay_snapshot_enabled: current.relay_snapshot_enabled,
          relay_snapshot_interval_minutes: current.relay_snapshot_interval_minutes,
          relay_snapshot_interval_seconds: current.relay_snapshot_interval_seconds,
        }),
      })
      settings.refetch()
      toast.success(`渠道自动同步已保存：余额${enabled ? `每 ${interval} 分钟` : "已关闭"}，倍率${rateEnabled ? rateIntervalLabel(rateInterval) : "已关闭"}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="space-y-4">
      <header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-foreground"><LayoutList className="size-5 text-brand" />{"渠道管理"}</h1>
          <p className="mt-1 text-xs text-muted-foreground">{"按最近分组倍率变动排序，管理监控状态、账号、余额与同步任务。"}</p>
        </div>
        <div className="inline-flex rounded-md border border-border bg-muted/30 p-0.5" role="group" aria-label="渠道统计时间范围">
          {ranges.map((item) => (
            <button key={item.value} type="button" onClick={() => setRange(item.value)} className={cn("h-11 rounded px-3 text-xs transition-colors sm:h-8", range === item.value ? "bg-background font-medium text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground")}>
              {item.label}
            </button>
          ))}
        </div>
      </header>

      <KpiRow range={range} />

      <Card className="gap-0 border border-border py-0 shadow-none">
        <CardHeader className="flex flex-row items-center justify-between gap-3 px-4 py-3">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-sm font-semibold"><Clock3 className="size-4 text-brand" />{"渠道自动同步"}</CardTitle>
            <p className="mt-1 truncate text-xs text-muted-foreground">{"余额与倍率分开调度：余额按分钟级间隔刷新，倍率按更短的探测间隔刷新。暂停监控的渠道不会被自动同步。"}</p>
          </div>
          <Button size="sm" className="h-9 shrink-0 gap-1.5" onClick={() => void save()} disabled={saving || !settings.data}><Save className="size-3.5" />{"保存"}</Button>
        </CardHeader>
        <CardContent className="grid divide-y divide-border border-t border-border px-4 md:grid-cols-2 md:divide-x md:divide-y-0">
          <div className="grid min-h-11 grid-cols-[auto_minmax(0,1fr)_7rem] items-center gap-3 py-4 md:pr-6">
            <Switch checked={enabled} onCheckedChange={setEnabled} disabled={!settings.data} aria-label="启用渠道余额同步" />
            <div className="min-w-0"><Label className="truncate text-xs font-medium">{"余额同步"}</Label><p className="truncate text-[11px] leading-4 text-muted-foreground">{"刷新渠道与账号余额"}</p></div>
            <Select value={String(interval)} onValueChange={(value) => setInterval(Number(value))} disabled={!enabled}>
              <SelectTrigger className="h-9 w-28 text-xs"><SelectValue /></SelectTrigger>
              <SelectContent>{intervals.map((value) => <SelectItem key={value} value={String(value)}>{value < 60 ? `每 ${value} 分钟` : value % 60 === 0 && value < 1440 ? `每 ${value / 60} 小时` : value === 1440 ? "每天" : `每 ${value} 分钟`}</SelectItem>)}</SelectContent>
            </Select>
          </div>
          <div className="grid min-h-11 grid-cols-[auto_minmax(0,1fr)_7rem] items-center gap-3 border-t border-border py-4 md:border-t-0 md:pl-6">
            <Switch checked={rateEnabled} onCheckedChange={setRateEnabled} disabled={!settings.data} aria-label="启用渠道倍率探测" />
            <div className="min-w-0"><Label className="truncate text-xs font-medium">{"倍率探测"}</Label><p className="truncate text-[11px] leading-4 text-muted-foreground">{"采集分组倍率与变动，最短 5 秒"}</p></div>
            <Select value={String(rateInterval)} onValueChange={(value) => setRateInterval(Number(value))} disabled={!rateEnabled}>
              <SelectTrigger className="h-9 w-28 text-xs"><SelectValue /></SelectTrigger>
              <SelectContent>{rateIntervals.map((value) => <SelectItem key={value} value={String(value)}>{rateIntervalLabel(value)}</SelectItem>)}</SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <MultiplierChanges range={range} />

      <ChannelCards usageRange={range} />
    </section>
  )
}
