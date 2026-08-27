"use client"

import { useEffect, useMemo, useState } from "react"
import { Activity, Box, Boxes, CircleDollarSign, LayoutGrid, List, RefreshCw, RotateCcw, Search, Users, UsersRound } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import type { DashboardRange, RelayAccountView } from "@/lib/api-types"
import { useRelayOverview, useRelayStations, useRelayUsage } from "@/lib/queries"
import { cn } from "@/lib/utils"

type ViewMode = "aggregate" | "accounts"

const ranges: { value: DashboardRange; label: string }[] = [
  { value: "today", label: "今天" }, { value: "24h", label: "24 小时" }, { value: "7d", label: "7 天" }, { value: "30d", label: "30 天" },
]

function money(value: number) { return `¥${value.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` }
function integer(value: number) { return value.toLocaleString("zh-CN") }
function planName(account: RelayAccountView) {
  const raw = account.account_plan?.trim().toLowerCase()
  if (raw && raw !== "unknown" && raw !== "<nil>") return raw
  const source = `${account.name} ${account.current_groups.map((group) => group.name).join(" ")}`.toLowerCase()
  for (const value of ["k12", "team", "pro", "plus", "free", "enterprise"]) if (source.includes(value)) return value
  return "未识别"
}
function statusName(account: RelayAccountView) {
  if (account.status?.toLowerCase() !== "active") return "异常"
  return account.schedulable ? "正常" : "未调度"
}

export default function LocalPoolPage() {
  const stations = useRelayStations()
  const [stationID, setStationID] = useState<number | null>(null)
  const [range, setRange] = useState<DashboardRange>("today")
  const [view, setView] = useState<ViewMode>("aggregate")
  const [plan, setPlan] = useState("all")
  const [platform, setPlatform] = useState("all")
  const [status, setStatus] = useState("all")
  const [query, setQuery] = useState("")
  const [busy, setBusy] = useState(false)
  const overview = useRelayOverview(stationID)
  const usage = useRelayUsage(stationID, range)

  useEffect(() => { if (stationID == null && stations.data?.[0]) setStationID(stations.data[0].id) }, [stationID, stations.data])

  const oauthAccounts = useMemo(() => {
    const usageByID = new Map((usage.data?.accounts ?? []).map((item) => [item.external_id, item]))
    return (overview.data?.accounts ?? []).filter((account) => account.type?.toLowerCase() === "oauth").map((account) => ({
      ...account,
      plan: planName(account),
      requestCount: usageByID.get(account.external_id)?.request_count ?? 0,
      tokens: usageByID.get(account.external_id)?.usage_total_tokens ?? 0,
      charge: usageByID.get(account.external_id)?.user_charge_amount ?? 0,
    }))
  }, [overview.data, usage.data])

  const plans = useMemo(() => [...new Set(oauthAccounts.map((account) => account.plan))].sort(), [oauthAccounts])
  const platforms = useMemo(() => [...new Set(oauthAccounts.map((account) => account.platform || "未识别"))].sort(), [oauthAccounts])
  const filtered = useMemo(() => oauthAccounts.filter((account) => {
    if (plan !== "all" && account.plan !== plan) return false
    if (platform !== "all" && (account.platform || "未识别") !== platform) return false
    if (status !== "all" && statusName(account) !== status) return false
    const keyword = query.trim().toLowerCase()
    return !keyword || account.name.toLowerCase().includes(keyword) || String(account.external_id).includes(keyword)
  }), [oauthAccounts, plan, platform, query, status])

  const groups = useMemo(() => {
    const result = new Map<string, { plan: string; accounts: typeof filtered; requests: number; tokens: number; charge: number }>()
    for (const account of filtered) {
      const item = result.get(account.plan) ?? { plan: account.plan, accounts: [], requests: 0, tokens: 0, charge: 0 }
      item.accounts.push(account); item.requests += account.requestCount; item.tokens += account.tokens; item.charge += account.charge
      result.set(account.plan, item)
    }
    return [...result.values()].sort((a, b) => b.charge - a.charge || a.plan.localeCompare(b.plan))
  }, [filtered])

  const totalCharge = filtered.reduce((sum, account) => sum + account.charge, 0)
  const totalRequests = filtered.reduce((sum, account) => sum + account.requestCount, 0)
  const totalTokens = filtered.reduce((sum, account) => sum + account.tokens, 0)
  const normalCount = filtered.filter((account) => statusName(account) === "正常").length

  async function syncOAuth() {
    if (!stationID) return
    setBusy(true)
    try {
      await apiFetch(`/relay-stations/${stationID}/sync`, { method: "POST" })
      await Promise.all([overview.refetch(), usage.refetch()])
      toast.success("已同步该中转站的 OAuth 账号和统计")
    } catch (error) { toast.error(error instanceof Error ? error.message : "同步失败") } finally { setBusy(false) }
  }

  function resetFilters() { setPlan("all"); setPlatform("all"); setStatus("all"); setQuery("") }

  return <section className="space-y-4">
    <header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3">
      <div><h1 className="flex items-center gap-2 text-xl font-bold"><UsersRound className="size-5 text-brand" />本地号池</h1><p className="mt-1 text-xs text-muted-foreground">同步中转站 OAuth 账号，汇总账号状态、用户扣费、请求和 Token；仅作统计展示。</p></div>
      <Button size="sm" className="h-9 gap-1.5" disabled={!stationID || busy} onClick={() => void syncOAuth()}><RefreshCw className={cn("size-3.5", busy && "animate-spin")} />同步 OAuth 号池</Button>
    </header>

    <div className="flex flex-wrap items-center gap-2">
      <div className="inline-flex rounded-md border border-border p-0.5"><button type="button" onClick={() => setView("aggregate")} className={cn("inline-flex h-8 items-center gap-1.5 rounded px-3 text-xs", view === "aggregate" ? "bg-muted font-medium" : "text-muted-foreground")}><LayoutGrid className="size-3.5" />类型聚合</button><button type="button" onClick={() => setView("accounts")} className={cn("inline-flex h-8 items-center gap-1.5 rounded px-3 text-xs", view === "accounts" ? "bg-muted font-medium" : "text-muted-foreground")}><List className="size-3.5" />单独账号</button></div>
      <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
        <Select value={stationID == null ? "none" : String(stationID)} onValueChange={(value) => setStationID(value === "none" ? null : Number(value))}><SelectTrigger className="h-9 w-48 text-xs"><SelectValue placeholder="选择中转站" /></SelectTrigger><SelectContent>{(stations.data ?? []).map((station) => <SelectItem key={station.id} value={String(station.id)}>{station.name}</SelectItem>)}</SelectContent></Select>
        <div className="inline-flex rounded-md border border-border bg-muted/30 p-0.5">{ranges.map((item) => <button type="button" key={item.value} onClick={() => setRange(item.value)} className={cn("h-8 rounded px-3 text-xs transition-colors", range === item.value ? "bg-background font-medium shadow-xs" : "text-muted-foreground hover:text-foreground")}>{item.label}</button>)}</div>
      </div>
    </div>

    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {[
        { label: "OAuth 账号", value: integer(filtered.length), footer: `${normalCount} 个正常`, icon: Users, color: "text-foreground", background: "bg-muted" },
        { label: "用户扣费", value: money(totalCharge), footer: "中转站实际扣费", icon: CircleDollarSign, color: "text-brand", background: "bg-brand/10" },
        { label: "请求", value: integer(totalRequests), footer: "所选时间范围", icon: Activity, color: "text-success", background: "bg-success/10" },
        { label: "Token", value: integer(totalTokens), footer: "输入与输出合计", icon: Box, color: "text-yellow-600 dark:text-yellow-400", background: "bg-yellow-400/10" },
      ].map(({ label, value, footer, icon: Icon, color, background }) => <Card key={label} className="flex flex-row items-start justify-between gap-0 border border-border p-4 shadow-none"><div className="flex min-w-0 flex-col"><span className="text-xs text-muted-foreground">{label}</span><p className={cn("mt-1 text-2xl font-bold tracking-tight tabular-nums", color)}>{value}</p><p className="mt-1 text-xs text-muted-foreground">{footer}</p></div><span className={cn("flex size-10 shrink-0 items-center justify-center rounded-xl", background)}><Icon className={cn("size-5", color)} /></span></Card>)}
    </div>

    <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
      <CardHeader className="gap-3 px-4 py-3"><div className="flex items-center justify-between gap-3"><div><CardTitle className="flex items-center gap-2 text-sm"><Boxes className="size-4 text-brand" />OAuth 号池统计</CardTitle><p className="mt-1 text-xs text-muted-foreground">账号类型优先读取中转站元数据，缺失时根据账号名称和分组识别 Team、K12、Pro、Plus 等。</p></div>{usage.loading ? <span className="text-xs text-muted-foreground">统计读取中…</span> : usage.error ? <span className="text-xs text-danger">统计读取失败</span> : null}</div>
        <div className="flex flex-wrap justify-end gap-2"><div className="relative"><Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="账号名称或 ID" className="h-9 w-44 pl-8 text-xs" /></div><Select value={plan} onValueChange={setPlan}><SelectTrigger className="h-9 w-32 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部账号类型</SelectItem>{plans.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select><Select value={platform} onValueChange={setPlatform}><SelectTrigger className="h-9 w-32 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部平台</SelectItem>{platforms.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select><Select value={status} onValueChange={setStatus}><SelectTrigger className="h-9 w-28 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部状态</SelectItem><SelectItem value="正常">正常</SelectItem><SelectItem value="未调度">未调度</SelectItem><SelectItem value="异常">异常</SelectItem></SelectContent></Select><Button variant="outline" size="sm" className="h-9 gap-1.5" onClick={resetFilters}><RotateCcw className="size-3.5" />重置</Button><Button variant="outline" size="sm" className="h-9 gap-1.5" disabled={!stationID || busy} onClick={() => { void overview.refetch(); void usage.refetch() }}><RefreshCw className={cn("size-3.5", busy && "animate-spin")} />刷新</Button></div>
      </CardHeader>
      <CardContent className="border-t border-border p-0"><div className="isolate max-h-[min(65vh,680px)] overflow-auto">
        {view === "aggregate" ? <div className="min-w-[900px]"><div className="sticky top-0 z-30 grid grid-cols-[150px_100px_100px_130px_130px_150px_minmax(180px,1fr)] gap-0 border-b border-border bg-muted text-[11px] text-foreground/90 font-medium [&>*]:px-4 [&>*]:py-2"><span>账号类型</span><span>账号数</span><span>正常</span><span>用户扣费</span><span>请求</span><span>Token</span><span>状态分布</span></div>{groups.length ? groups.map((group) => <div key={group.plan} className="grid grid-cols-[150px_100px_100px_130px_130px_150px_minmax(180px,1fr)] items-center gap-0 border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-2.5"><span className="font-semibold capitalize">{group.plan}</span><span className="font-mono tabular-nums">{integer(group.accounts.length)}</span><span className="font-mono text-success">{group.accounts.filter((account) => statusName(account) === "正常").length}</span><span className="font-mono font-semibold text-brand">{money(group.charge)}</span><span className="font-mono">{integer(group.requests)}</span><span className="font-mono">{integer(group.tokens)}</span><span className="text-muted-foreground">正常 {group.accounts.filter((account) => statusName(account) === "正常").length} · 未调度 {group.accounts.filter((account) => statusName(account) === "未调度").length} · 异常 {group.accounts.filter((account) => statusName(account) === "异常").length}</span></div>) : <p className="py-12 text-center text-sm text-muted-foreground">当前中转站没有符合条件的 OAuth 账号</p>}</div> : <div className="min-w-[1120px]"><div className="sticky top-0 z-30 grid grid-cols-[80px_minmax(190px,1fr)_100px_100px_90px_90px_120px_130px_150px] gap-0 border-b border-border bg-muted text-[11px] text-foreground/90 font-medium [&>*]:px-4 [&>*]:py-2"><span>ID</span><span>账号</span><span>平台</span><span>账号类型</span><span>状态</span><span>优先级</span><span>用户扣费</span><span>请求</span><span>Token</span></div>{filtered.length ? filtered.map((account) => <div key={account.external_id} className="grid grid-cols-[80px_minmax(190px,1fr)_100px_100px_90px_90px_120px_130px_150px] items-center gap-0 border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-2.5"><span className="font-mono text-muted-foreground">#{account.external_id}</span><div className="min-w-0"><p className="truncate font-medium" title={account.name}>{account.name}</p><p className="mt-0.5 text-[11px] text-muted-foreground">最近调用 {account.last_used_at ? new Date(account.last_used_at).toLocaleString("zh-CN") : "暂无"}</p></div><span>{account.platform || "-"}</span><span className="capitalize">{account.plan}</span><span className={cn(statusName(account) === "正常" ? "text-success" : statusName(account) === "异常" ? "text-danger" : "text-warning")}>{statusName(account)}</span><span className="font-mono">{account.priority}</span><span className="font-mono font-semibold text-brand">{money(account.charge)}</span><span className="font-mono">{integer(account.requestCount)}</span><span className="font-mono">{integer(account.tokens)}</span></div>) : <p className="py-12 text-center text-sm text-muted-foreground">当前筛选没有账号</p>}</div>}
      </div></CardContent>
    </Card>
  </section>
}
