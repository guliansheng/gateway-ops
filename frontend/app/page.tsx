import { useMemo, useState } from "react"
import { Activity, CircleAlert, CircleDollarSign, GitCompareArrows, LayoutList, Server, ShieldCheck, Users, WalletCards, type LucideIcon } from "lucide-react"
import { Card } from "@/components/ui/card"
import { KpiRow } from "@/components/monitor/kpi-row"
import { BalanceOverview } from "@/components/monitor/balance-overview"
import { MultiplierChanges } from "@/components/monitor/multiplier-changes"
import { useDashboardSummary, useOperationSummary } from "@/lib/queries"
import type { DashboardRange } from "@/lib/api-types"
import { cn } from "@/lib/utils"
import { RelayAdjustmentLog } from "@/components/monitor/relay-adjustment-log"
import { OperationSummaryCards } from "@/components/operations/summary-cards"

const ranges: { value: DashboardRange; label: string }[] = [
  { value: "today", label: "今天" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
]

function RelayRiskOverview({ range }: { range: DashboardRange }) {
  const summary = useDashboardSummary(range)
  const stats = summary.data?.relay ?? []
  const totals = useMemo(() => stats.reduce((acc, stat) => ({
    accounts: acc.accounts + stat.account_count,
    assignments: acc.assignments + stat.assignment_count,
    knownCosts: acc.knownCosts + stat.mapped_account_count,
    risks: acc.risks + stat.risk_account_count,
    noCandidates: acc.noCandidates + stat.no_safe_candidate_count,
    protected: acc.protected + stat.protected_account_count,
  }), { accounts: 0, assignments: 0, knownCosts: 0, risks: 0, noCandidates: 0, protected: 0 }), [stats])
  const metrics: { label: string; value: string | number; icon: LucideIcon; color: string; iconColor: string; background: string }[] = [
    { label: "中转站", value: stats.length, icon: GitCompareArrows, color: "text-brand", iconColor: "text-brand", background: "bg-brand/10" },
    { label: "远端账号", value: totals.accounts, icon: Users, color: "text-foreground", iconColor: "text-foreground", background: "bg-muted" },
    { label: "成本已采集", value: `${totals.knownCosts}/${totals.accounts}`, icon: CircleDollarSign, color: "text-brand", iconColor: "text-brand", background: "bg-brand/10" },
    { label: "亏损风险", value: totals.risks, icon: CircleAlert, color: "text-danger", iconColor: "text-danger", background: "bg-danger/10" },
    { label: "无安全候选", value: totals.noCandidates, icon: CircleAlert, color: "text-yellow-600 dark:text-yellow-400", iconColor: "text-yellow-600 dark:text-yellow-400", background: "bg-muted" },
    { label: "分组安全", value: totals.protected, icon: ShieldCheck, color: "text-success", iconColor: "text-success", background: "bg-success/10" },
  ]

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-6">
        {metrics.map(({ label, value, icon: Icon, color, iconColor, background }) => (
          <Card key={label} className="border border-border p-4 shadow-none">
            <div className="flex items-start justify-between gap-2"><div><p className="text-xs text-muted-foreground">{label}</p><p className={cn("mt-1 text-xl font-bold tabular-nums", color)}>{value}</p></div><span className={cn("flex size-8 items-center justify-center rounded-lg", background)}><Icon className={cn("size-4", iconColor)} /></span></div>
          </Card>
        ))}
      </div>
    </div>
  )
}

function RelayAdjustmentsOverview({ range }: { range: DashboardRange }) {
  const summary = useDashboardSummary(range)
  const rows = summary.data?.recent_relay_adjustments ?? []
  return <RelayAdjustmentLog rows={rows} title="中转站调整记录" storageKey="uh_dashboard_relay_adjustment_log_open" showStation />
}

function CostOverview({ range }: { range: DashboardRange }) {
  const summary = useOperationSummary(range)
  return (
    <section aria-labelledby="cost-stats-heading" className="space-y-3">
      <h2 id="cost-stats-heading" className="flex items-center gap-2 border-l-2 border-brand pl-3 text-base font-bold text-foreground"><WalletCards className="size-4 text-brand" />成本统计</h2>
      {summary.error ? <p className="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger"><CircleAlert className="size-4" />汇总读取失败：{summary.error}</p> : null}
      <OperationSummaryCards data={summary.data} loading={summary.loading} />
    </section>
  )
}

export default function Page() {
  const [range, setRange] = useState<DashboardRange>("today")

  return (
    <section className="space-y-5">
      <header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3">
        <div><h1 className="flex items-center gap-2 text-xl font-bold text-foreground"><Activity className="size-5 text-brand" />{"运营总览"}</h1><p className="mt-1 text-xs text-muted-foreground">{"汇总渠道余额、倍率变化、中转站账号风险和经营收支；账本记录请在成本管理中维护。"}</p></div>
        <div className="inline-flex rounded-md border border-border bg-muted/30 p-0.5" role="group" aria-label="统计时间范围">{ranges.map((item) => <button key={item.value} type="button" onClick={() => setRange(item.value)} className={cn("h-11 rounded px-3 text-xs transition-colors sm:h-8", range === item.value ? "bg-background font-medium text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground")}>{item.label}</button>)}</div>
      </header>

      <CostOverview range={range} />

      <section aria-labelledby="channel-stats-heading" className="space-y-3">
        <h2 id="channel-stats-heading" className="flex items-center gap-2 border-l-2 border-brand pl-3 text-base font-bold text-foreground"><LayoutList className="size-4 text-brand" />{"渠道统计"}</h2>
        <KpiRow range={range} />
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-5"><div className="lg:col-span-3"><BalanceOverview range={range} /></div><div className="lg:col-span-2"><MultiplierChanges range={range} /></div></div>
      </section>

      <section aria-labelledby="relay-stats-heading" className="space-y-3">
        <h2 id="relay-stats-heading" className="flex items-center gap-2 border-l-2 border-brand pl-3 text-base font-bold text-foreground"><Server className="size-4 text-brand" />{"中转站统计"}</h2>
        <RelayRiskOverview range={range} />
        <RelayAdjustmentsOverview range={range} />
      </section>
    </section>
  )
}
