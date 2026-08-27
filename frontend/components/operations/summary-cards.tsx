import { BookOpenCheck, CircleDollarSign, ReceiptText, WalletCards } from "lucide-react"
import { Card } from "@/components/ui/card"
import type { OperationSummary } from "@/lib/api-types"
import { cn } from "@/lib/utils"

export function cny(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "—"
  return `¥${value.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

export function OperationSummaryCards({ data, loading }: { data: OperationSummary | null; loading: boolean }) {
  const items = [
    { label: "总收入", value: cny(data?.ledger.income_amount), footer: "所选范围内经营收入", tone: "text-success", background: "bg-success/10", icon: ReceiptText },
    { label: "支出", value: cny(data?.ledger.expense_amount), footer: "所选范围内经营支出", tone: "text-danger", background: "bg-danger/10", icon: CircleDollarSign },
    { label: "账号采购", value: cny(data?.ledger.account_purchase_amount), footer: "已计入支出的账号成本", tone: "text-warning", background: "bg-warning/10", icon: BookOpenCheck },
    { label: "现金净额", value: cny(data?.ledger.net_amount), footer: "收入减支出，不代表利润", tone: "text-success", background: "bg-success/10", icon: WalletCards },
  ]

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {items.map(({ label, value, footer, tone, background, icon: Icon }) => (
        <Card key={label} className="flex flex-row items-start justify-between gap-0 border border-border p-4 shadow-none">
          <div className="flex min-w-0 flex-col">
            <p className="text-xs text-muted-foreground">{label}</p>
            <p className={cn("mt-1 text-2xl font-bold tracking-tight tabular-nums", tone)}>{loading && !data ? "加载中…" : value}</p>
            <p className="mt-1 text-xs text-muted-foreground">{footer}</p>
          </div>
          <span className={cn("flex size-10 shrink-0 items-center justify-center rounded-xl", background)}>
            <Icon className={cn("size-5", tone)} />
          </span>
        </Card>
      ))}
    </div>
  )
}
