"use client"

import { ArrowUpRight, CircleDollarSign, DollarSign, MessageSquare, WalletCards } from "lucide-react"
import { Card } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import { useDashboardSummary } from "@/lib/queries"
import type { RelayUsageRange } from "@/lib/api-types"
import { money } from "@/lib/format"
import type { LucideIcon } from "lucide-react"
import type { ReactNode } from "react"

interface Kpi {
  label: string
  value: ReactNode
  icon: LucideIcon
  iconBg: string
  iconColor: string
  footer: ReactNode
}

export function KpiRow({ range = "today" }: { range?: RelayUsageRange }) {
  const summary = useDashboardSummary(range)

  const data = summary.data
  const total = data?.total_channels ?? 0
  const active = data?.active_channels ?? 0
  const failed = data?.failed_channels ?? 0
  const totalBalance = data?.total_balance ?? 0
  const cumulativeRechargeAmount = data?.cumulative_recharge_amount ?? 0
  const consumptionAmount = data?.consumption_amount ?? 0
  const userChargeAmount = data?.user_charge_amount ?? 0
  const lowest = data?.lowest_balance ?? null

  const changeCount = data?.rate_change_count ?? 0
  const rangeLabel = range === "all" ? "全部时间" : range === "today" ? "今日" : range === "24h" ? "24 小时" : range === "7d" ? "7 天" : "30 天"

  const kpis: Kpi[] = [
    {
      label: "总余额",
      value: money(totalBalance),
      icon: DollarSign,
      iconBg: "bg-brand/10",
      iconColor: "text-brand",
      footer: lowest ? (
        <span className="text-muted-foreground">
          {"最低："}
          <span className="font-medium text-foreground">{lowest.name}</span>
          {" "}
          <span className="text-warning">{money(lowest.balance)}</span>
        </span>
      ) : (
        <span className="text-muted-foreground">{"—"}</span>
      ),
    },
    {
      label: "累计充值",
      value: money(cumulativeRechargeAmount),
      icon: WalletCards,
      iconBg: "bg-success/10",
      iconColor: "text-success",
      footer: <span className="text-muted-foreground">全部渠道历史充值合计</span>,
    },
    {
      label: "渠道状态",
      value: (
        <span>
          {active}
          <span className="mx-1 text-lg font-normal text-muted-foreground">{"/"}</span>
          <span className="text-lg font-normal text-muted-foreground">{total}</span>
        </span>
      ),
      icon: MessageSquare,
      iconBg: "bg-success/10",
      iconColor: "text-success",
      footer: (
        <span className="text-muted-foreground">
          <span className="text-success font-medium">{active} 健康</span>
          {failed > 0 ? (
            <>
              {" · "}
              <span className="text-danger font-medium">{failed} 失败</span>
            </>
          ) : null}
        </span>
      ),
    },
    {
      label: `${rangeLabel}渠道消耗`,
      value: money(consumptionAmount),
      icon: CircleDollarSign,
      iconBg: "bg-warning/10",
      iconColor: "text-warning",
      footer: <span className="text-muted-foreground">余额下降累计，充值不计入</span>,
    },
    {
      label: `${rangeLabel}用户扣费`,
      value: money(userChargeAmount, { precise: true }),
      icon: CircleDollarSign,
      iconBg: "bg-brand/10",
      iconColor: "text-brand",
      footer: (
        <span className={cn(data && !data.user_charge_complete ? "text-warning" : "text-muted-foreground")}>
          {data && !data.user_charge_complete
            ? `匹配 ${data.matched_account_count} 个账号，部分统计失败`
            : `匹配 ${data?.matched_account_count ?? 0} 个归属账号`}
        </span>
      ),
    },
    {
      label: `${rangeLabel}倍率变动`,
      value: (
        <span className={cn(changeCount > 0 ? "text-danger" : "text-foreground")}>
          {changeCount}
        </span>
      ),
      icon: ArrowUpRight,
      iconBg: "bg-danger/10",
      iconColor: "text-danger",
      footer: (
        <span className="text-muted-foreground">
          {changeCount > 0 ? `检测到 ${changeCount} 次变动` : `${rangeLabel}无变动`}
        </span>
      ),
    },
  ]

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-6">
      {kpis.map((k) => (
        <Card
          key={k.label}
          className="flex flex-row items-start justify-between gap-0 border border-border p-4 shadow-none"
        >
          <div className="flex min-w-0 flex-col">
            <span className="text-xs text-muted-foreground">{k.label}</span>
            <p className="mt-1 text-2xl font-bold tracking-tight text-foreground">{k.value}</p>
            <p className="mt-1 text-xs">{k.footer}</p>
          </div>
          <span className={cn("flex size-10 shrink-0 items-center justify-center rounded-xl", k.iconBg)}>
            <k.icon className={cn("size-5", k.iconColor)} />
          </span>
        </Card>
      ))}
    </div>
  )
}
