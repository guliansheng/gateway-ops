"use client"

import { useState } from "react"
import { ArrowUpRight, CircleDollarSign, DollarSign, Info, List, MessageSquare, RefreshCw, WalletCards } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useDashboardSummary, useUnlinkedAccounts } from "@/lib/queries"
import type { DashboardSummary, RelayUsageRange } from "@/lib/api-types"
import { cny, money } from "@/lib/format"
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

function UserChargeDetails({ data, rangeLabel, loading, onOpenUnlinked }: { data: DashboardSummary | null; rangeLabel: string; loading: boolean; onOpenUnlinked: () => void }) {
  const breakdown = data?.user_charge_breakdown

  return (
    <Tooltip delayDuration={150}>
      <TooltipTrigger asChild>
        <button type="button" className="inline-flex items-center gap-1.5 rounded-sm text-left outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`查看${rangeLabel}用户扣费明细`}>
          {money(data?.user_charge_amount ?? 0, { precise: true })}
          <Info className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom" align="end" sideOffset={8} className="w-80 max-w-[calc(100vw-24px)] border border-border bg-popover p-3 text-popover-foreground shadow-lg">
        <div className="space-y-3">
          <p className="font-semibold">{rangeLabel}用户扣费明细</p>
          {breakdown ? (
            <>
              <dl className="space-y-2">
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-muted-foreground">已关联渠道扣费</dt>
                  <dd className={cn("font-mono tabular-nums", !breakdown.channel_complete && "text-warning")}>
                    {money(breakdown.channel_amount, { precise: true })}{!breakdown.channel_complete && "（部分）"}
                  </dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-muted-foreground">未关联渠道账号扣费</dt>
                  <dd className={cn("flex items-center gap-2 font-mono tabular-nums", breakdown.unlinked_amount == null && "text-warning")}>
                    <span>{breakdown.unlinked_amount == null ? "暂不可用" : money(breakdown.unlinked_amount, { precise: true })}</span>
                    <Button type="button" variant="outline" size="sm" className="h-6 gap-1 px-2 text-[11px] font-sans" onClick={onOpenUnlinked}>
                      <List className="size-3" aria-hidden="true" />
                      账号明细
                    </Button>
                  </dd>
                </div>
                <div className="flex items-center justify-between gap-3 border-t border-border pt-2 font-semibold">
                  <dt>用户扣费合计</dt>
                  <dd className={cn("font-mono tabular-nums", !data?.user_charge_complete && "text-warning")}>{money(data?.user_charge_amount, { precise: true })}{!data?.user_charge_complete && "（部分）"}</dd>
                </div>
              </dl>
              {data && data.excluded_user_charge_amount > 0 ? (
                <p className="text-xs leading-5 text-muted-foreground">已从合计中剔除“不计入用户扣费统计”的账号扣费 {money(data.excluded_user_charge_amount, { precise: true })}，这些账号的原始扣费仍保留在明细里。</p>
              ) : null}
              {breakdown.unlinked_amount == null && <p className="text-warning">统计尚不完整或正在更新，暂无法准确拆分未关联扣费。</p>}
              <p className="text-muted-foreground">按当前渠道关联拆分；未关联扣费为合计减去渠道扣费，含临时、福利账号，不计入渠道消耗。</p>
            </>
          ) : <p className="text-muted-foreground">{loading ? "正在读取扣费明细…" : "暂无法读取扣费明细"}</p>}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

function UnlinkedAccountsDialog({ open, onOpenChange, range, rangeLabel }: { open: boolean; onOpenChange: (open: boolean) => void; range: RelayUsageRange; rangeLabel: string }) {
  const query = useUnlinkedAccounts(open ? range : null)
  const data = query.data

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-2xl overflow-y-auto p-0 sm:max-w-2xl">
        <DialogHeader className="border-b border-border px-4 py-4 sm:px-6">
          <DialogTitle>未关联渠道账号扣费明细</DialogTitle>
          <DialogDescription>
            {rangeLabel}内没有归属到任何监控渠道的中转站账号，与首页“未关联渠道账号扣费”使用同一套关联规则。
          </DialogDescription>
        </DialogHeader>
        <div className="px-4 py-4 sm:px-6">
          {query.loading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground" aria-live="polite">
              <RefreshCw className="size-4 animate-spin" aria-hidden="true" />
              正在读取账号扣费…
            </div>
          ) : query.error ? (
            <div className="rounded-md border border-danger/30 bg-danger/5 px-3 py-2.5 text-sm leading-5 text-danger">{query.error}</div>
          ) : !data || data.accounts.length === 0 ? (
            <div className="rounded-md border border-dashed border-border px-4 py-10 text-center text-sm text-muted-foreground">
              {rangeLabel}没有未关联渠道的账号扣费
            </div>
          ) : (
            <div className="space-y-3">
              {!data.complete ? (
                <p className="rounded-md border border-warning/30 bg-warning/5 px-3 py-2 text-xs leading-5 text-warning">部分账号的消费统计读取失败，明细可能不完整。</p>
              ) : null}
              <div className="divide-y divide-border overflow-hidden rounded-md border border-border">
                {data.accounts.map((account) => (
                  <div key={`${account.station_id}-${account.account_external_id}`} className="flex items-start justify-between gap-3 px-3 py-2">
                    <div className="min-w-0">
                      <p className="truncate text-xs font-medium text-foreground" title={`${account.account_name || "未命名账号"} (#${account.account_external_id})`}>
                        {account.account_name || `账号 ${account.account_external_id}`}
                        <span className="ml-1.5 font-mono text-[10px] font-normal text-muted-foreground">#{account.account_external_id}</span>
                      </p>
                      <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{account.station_name}</p>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      <span className="font-mono text-xs tabular-nums text-foreground">{account.failed ? "—" : money(account.user_charge, { precise: true })}</span>
                      {account.failed ? (
                        <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">读取失败</span>
                      ) : account.user_charge_excluded ? (
                        <span className="rounded bg-warning/10 px-1.5 py-0.5 text-[10px] font-medium text-warning">已排除</span>
                      ) : (
                        <span className="text-[10px] text-muted-foreground">计入</span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
              <div className="flex items-center justify-between gap-3 border-t border-border pt-2 text-sm font-semibold">
                <span>计入首页合计</span>
                <span className="font-mono tabular-nums">{money(data.total_amount, { precise: true })}</span>
              </div>
              {Math.abs(data.raw_amount - data.total_amount) > 1e-9 ? (
                <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
                  <span>未关联账号原始扣费</span>
                  <span className="font-mono tabular-nums">{money(data.raw_amount, { precise: true })}</span>
                </div>
              ) : null}
              <p className="text-xs leading-5 text-muted-foreground">在中转站账号列表中选中账号并选择“统计用户扣费”，可把重复计费的账号排除在用户扣费汇总之外。</p>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function KpiRow({ range = "today" }: { range?: RelayUsageRange }) {
  const summary = useDashboardSummary(range)
  const [unlinkedOpen, setUnlinkedOpen] = useState(false)

  const data = summary.data
  const total = data?.total_channels ?? 0
  const active = data?.active_channels ?? 0
  const failed = data?.failed_channels ?? 0
  const totalBalance = data?.total_balance ?? 0
  const cumulativeRechargeAmount = data?.cumulative_recharge_amount ?? 0
  const consumptionAmount = data?.consumption_amount ?? 0
  const lowest = data?.lowest_balance ?? null

  const changeCount = data?.rate_change_count ?? 0
  const rangeLabel = range === "all" ? "全部时间" : range === "today" ? "今日" : range === "24h" ? "24 小时" : range === "7d" ? "7 天" : "30 天"

  const kpis: Kpi[] = [
    {
      label: "总余额",
      value: cny(totalBalance),
      icon: DollarSign,
      iconBg: "bg-brand/10",
      iconColor: "text-brand",
      footer: lowest ? (
        <span className="text-muted-foreground">
          {"最低："}
          <span className="font-medium text-foreground">{lowest.name}</span>
          {" "}
          <span className="text-warning">{cny(lowest.balance)}</span>
        </span>
      ) : (
        <span className="text-muted-foreground">{"—"}</span>
      ),
    },
    {
      label: "累计充值",
      value: cny(cumulativeRechargeAmount),
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
      value: cny(consumptionAmount),
      icon: CircleDollarSign,
      iconBg: "bg-warning/10",
      iconColor: "text-warning",
      footer: <span className="text-muted-foreground">按各渠道充值倍率换算的实际消耗</span>,
    },
    {
      label: `${rangeLabel}用户扣费`,
      value: <UserChargeDetails data={data} rangeLabel={rangeLabel} loading={summary.loading} onOpenUnlinked={() => setUnlinkedOpen(true)} />,
      icon: CircleDollarSign,
      iconBg: "bg-brand/10",
      iconColor: "text-brand",
      footer: (
        <span className={cn(data && !data.user_charge_complete ? "text-warning" : "text-muted-foreground")}>
          {data && !data.user_charge_complete
            ? "部分中转站统计失败，扣费合计不完整"
            : "全部中转站，含未关联渠道账号"}
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
    <>
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
      <UnlinkedAccountsDialog open={unlinkedOpen} onOpenChange={setUnlinkedOpen} range={range} rangeLabel={rangeLabel} />
    </>
  )
}
