"use client"

import { useEffect, useMemo, useState } from "react"
import { AlertCircle, Pencil, Plus, ReceiptText, RefreshCw, RotateCcw, Trash2, WalletCards } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { useChannels, useOperationLedger, useOperationSummary, useRelayStations } from "@/lib/queries"
import type { OperationLedgerEntry, OperationRange } from "@/lib/api-types"
import { cn } from "@/lib/utils"
import { cny, OperationSummaryCards } from "@/components/operations/summary-cards"

const ranges: { value: OperationRange; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "today", label: "今天" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
]

const categories = [
  { value: "user_revenue", label: "用户收入", direction: "income" },
  { value: "other_income", label: "其他收入", direction: "income" },
  { value: "account_purchase", label: "账号采购", direction: "expense" },
  { value: "upstream_recharge", label: "上游充值", direction: "expense" },
  { value: "refund", label: "退款", direction: "expense" },
  { value: "operating_expense", label: "运营支出", direction: "expense" },
  { value: "other_expense", label: "其他支出", direction: "expense" },
] as const

type Direction = "income" | "expense"
type LedgerDirection = Direction | "all"

function localDateTimeValue(date = new Date()) {
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 19)
}

function categoryLabel(value: string) {
  return categories.find((category) => category.value === value)?.label ?? value
}

function rangeLabel(range: OperationRange) {
  return ranges.find((item) => item.value === range)?.label ?? range
}

function relationLabel(row: OperationLedgerEntry) {
  return [row.channel_name, row.relay_station_name].filter(Boolean).join(" / ") || "未关联"
}

function occurredAtLabel(value: string) {
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(new Date(value))
}

interface LedgerRowProps {
  row: OperationLedgerEntry
  onEdit: (row: OperationLedgerEntry) => void
  onDelete: (row: OperationLedgerEntry) => void
}

function LedgerRow({ row, onEdit, onDelete }: LedgerRowProps) {
  return (
    <div className="grid min-w-[1220px] grid-cols-[188px_102px_132px_142px_182px_114px_minmax(252px,1fr)_108px] items-center border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-2.5">
      <span className="font-mono tabular-nums text-muted-foreground">{occurredAtLabel(row.occurred_at)}</span>
      <span className={cn("font-medium", row.direction === "income" ? "text-success" : "text-danger")}>{row.direction === "income" ? "收入" : "支出"}</span>
      <span className="truncate text-muted-foreground" title={categoryLabel(row.category)}>{categoryLabel(row.category)}</span>
      <span className="font-mono font-semibold tabular-nums">{cny(row.amount)}</span>
      <span className="truncate text-muted-foreground" title={relationLabel(row)}>{relationLabel(row)}</span>
      <span className="text-muted-foreground">{row.source === "local_account" ? "本地号池" : "手工"}</span>
      <span className="truncate text-foreground" title={row.description}>{row.description}</span>
      <span className="fixed-column-shadow-right sticky right-0 z-10 flex min-h-8 self-stretch items-center bg-card">
        {row.source === "manual" ? <><Button type="button" variant="ghost" size="icon" className="size-8" aria-label="编辑账本记录" onClick={() => onEdit(row)}><Pencil className="size-3.5" /></Button><Button type="button" variant="ghost" size="icon" className="size-8 text-muted-foreground hover:text-danger" aria-label="删除账本记录" onClick={() => onDelete(row)}><Trash2 className="size-3.5" /></Button></> : null}
      </span>
    </div>
  )
}

export default function OperationsCostsPage() {
  const [range, setRange] = useState<OperationRange>("today")
  const [ledgerDirection, setLedgerDirection] = useState<LedgerDirection>("all")
  const [ledgerCategory, setLedgerCategory] = useState("all")
  const summary = useOperationSummary(range)
  const ledger = useOperationLedger(range, { direction: ledgerDirection, category: ledgerCategory })
  const channels = useChannels()
  const stations = useRelayStations()
  const [editing, setEditing] = useState<OperationLedgerEntry | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [direction, setDirection] = useState<Direction>("expense")
  const [category, setCategory] = useState("upstream_recharge")
  const [amount, setAmount] = useState("")
  const [description, setDescription] = useState("")
  const [occurredAt, setOccurredAt] = useState(localDateTimeValue)
  const [channelID, setChannelID] = useState("none")
  const [stationID, setStationID] = useState("none")
  const [busy, setBusy] = useState(false)
  const data = summary.data
  const rows = ledger.data ?? []
  const formCategories = useMemo(() => categories.filter((item) => item.direction === direction), [direction])
  const filterCategories = useMemo(() => categories.filter((item) => ledgerDirection === "all" || item.direction === ledgerDirection), [ledgerDirection])

  function resetForm(row?: OperationLedgerEntry) {
    const nextDirection = row?.direction ?? "expense"
    setEditing(row ?? null)
    setDirection(nextDirection)
    setCategory(row?.category ?? (nextDirection === "expense" ? "upstream_recharge" : categories.find((item) => item.direction === nextDirection)?.value ?? ""))
    setAmount(row ? String(row.amount) : "")
    setDescription(row?.description ?? "")
    setOccurredAt(row ? localDateTimeValue(new Date(row.occurred_at)) : localDateTimeValue())
    setChannelID(row?.channel_id ? String(row.channel_id) : "none")
    setStationID(row?.relay_station_id ? String(row.relay_station_id) : stations.data?.[0] ? String(stations.data[0].id) : "none")
    setFormOpen(true)
  }

  useEffect(() => {
    if (!formOpen || editing || stationID !== "none" || !stations.data?.[0]) return
    setStationID(String(stations.data[0].id))
  }, [editing, formOpen, stationID, stations.data])

  function changeDirection(value: Direction) {
    setDirection(value)
    setCategory(categories.find((item) => item.direction === value)?.value ?? "")
  }

  function changeLedgerDirection(value: LedgerDirection) {
    setLedgerDirection(value)
    if (value !== "all" && ledgerCategory !== "all" && categories.find((item) => item.value === ledgerCategory)?.direction !== value) {
      setLedgerCategory("all")
    }
  }

  async function saveEntry(event: React.FormEvent) {
    event.preventDefault()
    const value = Number(amount)
    if (!Number.isFinite(value) || value <= 0 || !description.trim()) {
      toast.error("请填写有效金额和说明")
      return
    }
    setBusy(true)
    try {
      const body = JSON.stringify({
        direction,
        category,
        amount: value,
        description: description.trim(),
        occurred_at: occurredAt,
        channel_id: channelID === "none" ? null : Number(channelID),
        relay_station_id: stationID === "none" ? null : Number(stationID),
      })
      await apiFetch(editing ? `/operations/ledger/${editing.id}` : "/operations/ledger", { method: editing ? "PUT" : "POST", body })
      setFormOpen(false)
      summary.refetch()
      ledger.refetch()
      toast.success(editing ? "账本记录已更新" : "账本记录已添加")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存账本记录失败")
    } finally {
      setBusy(false)
    }
  }

  async function deleteEntry(row: OperationLedgerEntry) {
    if (!window.confirm(`确认删除“${row.description}”吗？`)) return
    setBusy(true)
    try {
      await apiFetch(`/operations/ledger/${row.id}`, { method: "DELETE" })
      summary.refetch()
      ledger.refetch()
      toast.success("账本记录已删除")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除账本记录失败")
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="space-y-4">
      <header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3">
        <div><h1 className="flex items-center gap-2 text-xl font-bold text-foreground"><WalletCards className="size-5 text-brand" />成本管理</h1><p className="mt-1 text-xs text-muted-foreground">维护显式收支账本，汇总渠道成本、中转站用户扣费与账号采购成本。</p></div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border border-border bg-muted/30 p-0.5" role="group" aria-label="成本统计时间范围">{ranges.map((item) => <button type="button" key={item.value} onClick={() => setRange(item.value)} className={cn("h-10 rounded px-3 text-xs transition-colors sm:h-8", range === item.value ? "bg-background font-medium text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground")}>{item.label}</button>)}</div>
          <Button type="button" size="sm" className="h-9 gap-1.5" onClick={() => resetForm()}><Plus className="size-3.5" />记一笔</Button>
        </div>
      </header>

      {summary.error ? <p className="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger"><AlertCircle className="size-4" />汇总读取失败：{summary.error}</p> : null}

      <OperationSummaryCards data={data} loading={summary.loading} />

      <p className="rounded-md border border-success/20 bg-success/5 px-3 py-2 text-xs text-muted-foreground">所选时间范围内，中转站用户实际扣费 {cny(data?.ledger.relay_revenue_amount)} 已自动计入收入，并与手工新增收入合并。</p>

      <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
        <CardHeader className="gap-3 px-4 py-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div><CardTitle className="flex items-center gap-2 text-sm font-semibold"><ReceiptText className="size-4 text-brand" />收支账本</CardTitle><p className="mt-1 text-xs text-muted-foreground">{rangeLabel(range)} · {rows.length} 条。现金净额只表示已记录收支差额，不代表利润。</p></div>
          </div>
          <div className="flex flex-wrap justify-end gap-2">
            <Select value={ledgerDirection} onValueChange={(value) => changeLedgerDirection(value as LedgerDirection)}><SelectTrigger className="h-9 w-28 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部方向</SelectItem><SelectItem value="income">收入</SelectItem><SelectItem value="expense">支出</SelectItem></SelectContent></Select>
            <Select value={ledgerCategory} onValueChange={setLedgerCategory}><SelectTrigger className="h-9 w-32 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部类别</SelectItem>{filterCategories.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select>
            <Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => { setLedgerDirection("all"); setLedgerCategory("all") }}><RotateCcw className="size-3.5" />重置</Button>
            <Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => { summary.refetch(); ledger.refetch() }}><RefreshCw className="size-3.5" />刷新</Button>
          </div>
          {ledger.error ? <p className="flex items-center gap-2 text-xs text-danger"><AlertCircle className="size-3.5" />账本刷新失败，保留上次结果：{ledger.error}</p> : null}
        </CardHeader>
        <CardContent className="border-t border-border p-0">
          <div className="isolate max-h-[min(52vh,520px)] overflow-auto">
            <div className="min-w-[1220px]">
              <div className="sticky top-0 z-30 grid grid-cols-[188px_102px_132px_142px_182px_114px_minmax(252px,1fr)_108px] border-b border-border bg-muted text-[11px] text-foreground/90 font-medium [&>*]:px-4 [&>*]:py-2"><span>发生时间</span><span>方向</span><span>类别</span><span>金额</span><span>关联对象</span><span>来源</span><span>说明</span><span className="fixed-column-shadow-right sticky right-0 z-40 flex self-stretch items-center bg-muted">操作</span></div>
              {rows.length === 0 ? <p className="py-12 text-center text-sm text-muted-foreground">当前筛选没有显式账本记录</p> : rows.map((row) => <LedgerRow key={row.id} row={row} onEdit={resetForm} onDelete={deleteEntry} />)}
            </div>
          </div>
        </CardContent>
      </Card>

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader><DialogTitle>{editing ? "编辑账本记录" : "新增账本记录"}</DialogTitle><DialogDescription>手工收入、支出与关联对象</DialogDescription></DialogHeader>
          <form onSubmit={saveEntry} className="grid gap-3 sm:grid-cols-2">
            <div className="min-w-0 space-y-1.5"><Label>方向</Label><Select value={direction} onValueChange={(value) => changeDirection(value as Direction)}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="income">收入</SelectItem><SelectItem value="expense">支出</SelectItem></SelectContent></Select></div>
            <div className="min-w-0 space-y-1.5"><Label>类别</Label><Select value={category} onValueChange={setCategory}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent>{formCategories.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select></div>
            <div className="space-y-1.5"><Label htmlFor="operation-amount">金额 CNY</Label><Input id="operation-amount" value={amount} onChange={(event) => setAmount(event.target.value)} type="number" min="0.01" step="0.01" required /></div>
            <div className="space-y-1.5"><Label htmlFor="operation-date">发生时间</Label><Input id="operation-date" value={occurredAt} onChange={(event) => setOccurredAt(event.target.value)} type="datetime-local" step="1" required /></div>
            <div className="min-w-0 space-y-1.5"><Label>关联渠道</Label><Select value={channelID} onValueChange={setChannelID}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">不关联渠道</SelectItem>{(channels.data ?? []).map((channel) => <SelectItem key={channel.id} value={String(channel.id)}>{channel.name}</SelectItem>)}</SelectContent></Select></div>
            <div className="min-w-0 space-y-1.5"><Label className="flex items-center gap-1.5">关联中转站 <span className="text-[10px] font-normal text-muted-foreground">非必填</span></Label><Select value={stationID} onValueChange={setStationID}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">不关联中转站</SelectItem>{(stations.data ?? []).map((station) => <SelectItem key={station.id} value={String(station.id)}>{station.name}</SelectItem>)}</SelectContent></Select></div>
            <div className="space-y-1.5 sm:col-span-2"><Label htmlFor="operation-description">说明</Label><Input id="operation-description" value={description} onChange={(event) => setDescription(event.target.value)} placeholder="例如：用户充值、退款" required /></div>
            <DialogFooter className="sm:col-span-2"><Button type="button" variant="outline" onClick={() => setFormOpen(false)}>取消</Button><Button type="submit" disabled={busy}>{editing ? "保存修改" : "添加记录"}</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  )
}
