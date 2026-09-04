"use client"

import { useEffect, useMemo, useState } from "react"
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, CircleAlert, CircleDollarSign, Gauge, RefreshCw, RotateCcw, Save, Search, ShieldAlert, Trash2, Users } from "lucide-react"
import { toast } from "sonner"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import type { RelayUsageRange, RelayUserManagementItem, RelayUserRiskLevel, RelayUserSortKey } from "@/lib/api-types"
import { useRelayUsers } from "@/lib/queries"
import { cn } from "@/lib/utils"

const ranges: Array<{ value: RelayUsageRange; label: string }> = [
  { value: "today", label: "今天" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
  { value: "all", label: "全部" },
]

const compactNumber = new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 2 })

const sortLabels: Record<RelayUserSortKey, string> = {
  id: "ID",
  balance: "余额",
  usage: "区间消费",
  risk_score: "风险分数",
  registration_ip_count: "同 IP 账号数",
  current_concurrency: "并发",
  last_used_at: "最后使用时间",
  created_at: "创建时间",
}

const tableColumns = "grid-cols-[48px_64px_minmax(170px,1fr)_minmax(110px,0.8fr)_210px_138px_120px_150px_128px_112px_112px_168px_168px]"
const tableMinWidth = "min-w-[1590px]"

function riskTone(level: RelayUserManagementItem["risk_level"]) {
  switch (level) {
    case "high": return "border-danger/30 bg-danger/10 text-danger"
    case "medium": return "border-warning/30 bg-warning/10 text-warning"
    case "low": return "border-brand/30 bg-brand/10 text-brand"
    default: return "border-border bg-muted text-muted-foreground"
  }
}

function riskLabel(level: RelayUserManagementItem["risk_level"]) {
  return level === "high" ? "高风险" : level === "medium" ? "中风险" : level === "low" ? "低风险" : "正常"
}

function usePersistedOpen(storageKey: string, defaultOpen = true) {
  const [open, setOpen] = useState(() => {
    try {
      const stored = window.localStorage.getItem(storageKey)
      return stored === "true" || stored === "false" ? stored === "true" : defaultOpen
    } catch {
      return defaultOpen
    }
  })
  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, String(open))
    } catch {
      // The disclosure still works for the current session without persistence.
    }
  }, [open, storageKey])
  return [open, setOpen] as const
}

function currency(value: number) {
  return `$${value.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`
}

function tokenAmount(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "-"
  return compactNumber.format(value)
}

function chargeAmount(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "-"
  return `$${value.toLocaleString("en-US", { minimumFractionDigits: 6, maximumFractionDigits: 6 })}`
}

function capacityTone(user: RelayUserManagementItem) {
  if (user.concurrency <= 0 || user.current_concurrency <= 0) return "border-border bg-muted text-muted-foreground"
  if (user.current_concurrency >= user.concurrency) return "border-danger/30 bg-danger/10 text-danger"
  return "border-warning/30 bg-warning/10 text-warning"
}

function dateTime(value?: string) {
  if (!value) return "-"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? "-" : date.toLocaleString("zh-CN")
}

function SortButton({ sortBy, sortOrder, field, onChange, label, children }: { sortBy: RelayUserSortKey; sortOrder: "asc" | "desc"; field: RelayUserSortKey; onChange: (field: RelayUserSortKey) => void; label?: string; children?: string }) {
  const active = sortBy === field
  const Icon = active ? (sortOrder === "asc" ? ArrowUp : ArrowDown) : ArrowUpDown
  const title = children || label || sortLabels[field]
  return <button type="button" onClick={() => onChange(field)} className="inline-flex cursor-pointer items-center gap-1 rounded-sm font-medium transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`${title}排序${active ? `，当前${sortOrder === "asc" ? "升序" : "降序"}` : ""}`}><span>{title}</span><Icon className="size-3" /></button>
}

function UserRow({ user, selected, busy, onSelect, onStatusChange, onRegistrationIPClick }: { user: RelayUserManagementItem; selected: boolean; busy: boolean; onSelect: (checked: boolean) => void; onStatusChange: (status: "active" | "disabled") => void; onRegistrationIPClick: (ip: string) => void }) {
  const isAdmin = user.role === "admin"
  return <div className={`grid ${tableMinWidth} ${tableColumns} items-center gap-0 border-b border-border text-xs last:border-0 [&>*]:px-3 [&>*]:py-3`}>
    <div><Checkbox checked={selected} onCheckedChange={(value) => onSelect(value === true)} aria-label={`选择用户 ${user.email}`} /></div>
    <div className="font-mono tabular-nums text-muted-foreground">#{user.id}</div>
    <div className="min-w-0"><p className="truncate font-medium text-foreground" title={user.email}>{user.email || "-"}</p><p className="mt-0.5 truncate text-[11px] text-muted-foreground" title={user.username}>{user.username || "未设置用户名"}</p></div>
    <div className="min-w-0 truncate" title={user.username}>{user.username || "-"}</div>
    <div className="min-w-0">{user.registration_ip ? <button type="button" className="block max-w-full cursor-pointer truncate font-mono text-left text-[11px] text-brand underline decoration-brand/40 underline-offset-2 hover:text-brand/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" title={`筛选注册 IP ${user.registration_ip}`} onClick={() => onRegistrationIPClick(user.registration_ip!)}>{user.registration_ip}</button> : <p className="truncate font-mono text-[11px] text-muted-foreground">未记录</p>}{user.registration_ip_count > 1 ? <p className="mt-0.5 text-[11px] font-medium text-warning">同 IP {user.registration_ip_count} 个账号</p> : <p className="mt-0.5 text-[11px] text-muted-foreground">独立或未知</p>}</div>
    <div><Badge variant="outline" className={cn("gap-1.5 font-semibold", riskTone(user.risk_level))}><ShieldAlert className="size-3" />{riskLabel(user.risk_level)} {user.risk_score}</Badge>{user.risk_reasons?.length ? <p className="mt-1 max-w-[104px] truncate text-[10px] text-muted-foreground" title={user.risk_reasons.join("；")}>{user.risk_reasons[0]}</p> : null}</div>
    <div className="font-mono text-sm font-semibold tabular-nums text-foreground">{currency(user.balance)}</div>
    <div className="min-w-0 space-y-0.5 font-mono tabular-nums"><span className="block whitespace-nowrap text-sm font-semibold text-foreground">{tokenAmount(user.usage_total_tokens)} Token</span><span className="block whitespace-nowrap text-[11px] font-medium text-brand">用户扣费 {chargeAmount(user.usage)}</span></div>
    <div><span className={cn("inline-flex items-center gap-1 rounded-md border px-1.5 py-1 font-mono text-[11px] font-semibold tabular-nums", capacityTone(user))} title="实时并发占用 / 并发上限"><Gauge className="size-3" />{user.current_concurrency} / {user.concurrency}</span></div>
    <div className="font-mono tabular-nums text-muted-foreground">{user.rpm_limit > 0 ? user.rpm_limit.toLocaleString("zh-CN") : "不限"}</div>
    <div className="flex items-center gap-2"><Switch checked={user.status === "active"} onCheckedChange={(checked) => onStatusChange(checked ? "active" : "disabled")} disabled={busy || isAdmin} aria-label={`${user.email}${user.status === "active" ? "禁用" : "启用"}`} /><span className={cn("text-[11px] font-medium", user.status === "active" ? "text-success" : "text-muted-foreground")}>{user.status === "active" ? "启用" : "禁用"}</span>{isAdmin ? <Tooltip><TooltipTrigger asChild><button type="button" className="rounded-sm text-warning focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label="管理员账号不可切换状态"><CircleAlert className="size-3.5" /></button></TooltipTrigger><TooltipContent>管理员账号不可在此处切换状态</TooltipContent></Tooltip> : null}</div>
    <div className="font-mono text-[11px] text-muted-foreground" title={dateTime(user.last_used_at)}>{dateTime(user.last_used_at)}</div>
    <div className="font-mono text-[11px] text-muted-foreground" title={dateTime(user.created_at)}>{dateTime(user.created_at)}</div>
  </div>
}

export function UserManagement({ stationID }: { stationID: number }) {
  const [open, setOpen] = usePersistedOpen("uh_relay_user_management_open")
  const [searchDraft, setSearchDraft] = useState("")
  const [search, setSearch] = useState("")
  const [registrationIPDraft, setRegistrationIPDraft] = useState("")
  const [registrationIP, setRegistrationIP] = useState("")
  const [range, setRange] = useState<RelayUsageRange>("today")
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [sortBy, setSortBy] = useState<RelayUserSortKey>("balance")
  const [sortOrder, setSortOrder] = useState<"asc" | "desc">("desc")
  const [riskLevel, setRiskLevel] = useState<RelayUserRiskLevel>("all")
  const [selected, setSelected] = useState<number[]>([])
  const [batchConcurrency, setBatchConcurrency] = useState("")
  const [batchRPM, setBatchRPM] = useState("")
  const [busy, setBusy] = useState(false)
  const [statusBusyID, setStatusBusyID] = useState<number | null>(null)
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)
  const users = useRelayUsers(stationID, { page, pageSize, search, range, sortBy, sortOrder, riskLevel, registrationIP })
  const rows = useMemo(() => users.data?.items ?? [], [users.data])
  const allPageSelected = rows.length > 0 && rows.every((user) => selected.includes(user.id))
  const selectedOnPage = useMemo(() => rows.filter((user) => selected.includes(user.id)), [rows, selected])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setPage(1)
      setSearch(searchDraft.trim())
    }, 300)
    return () => window.clearTimeout(timer)
  }, [searchDraft])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setPage(1)
      setRegistrationIP(registrationIPDraft.trim())
    }, 300)
    return () => window.clearTimeout(timer)
  }, [registrationIPDraft])

  useEffect(() => {
    setSelected((current) => current.filter((id) => rows.some((user) => user.id === id)))
  }, [rows])

  function changeSort(field: RelayUserSortKey) {
    setPage(1)
    if (sortBy !== field) {
      setSortBy(field)
      setSortOrder(field === "balance" || field === "usage" ? "desc" : "asc")
      return
    }
    setSortOrder((current) => current === "asc" ? "desc" : "asc")
  }

  function reset() {
    setSearchDraft("")
    setSearch("")
    setRegistrationIPDraft("")
    setRegistrationIP("")
    setRange("today")
    setSortBy("balance")
    setSortOrder("desc")
    setRiskLevel("all")
    setPage(1)
    setSelected([])
  }

  function filterByRegistrationIP(ip: string) {
    const value = ip.trim()
    if (!value) return
    setRegistrationIPDraft(value)
    setRegistrationIP(value)
    setPage(1)
    setSelected([])
  }

  async function updateStatus(user: RelayUserManagementItem, status: "active" | "disabled") {
    setStatusBusyID(user.id)
    try {
      await apiFetch(`/relay-stations/${stationID}/users/${user.id}/status`, { method: "PUT", body: JSON.stringify({ status }) })
      await users.refetch()
      toast.success(status === "active" ? "用户已启用" : "用户已禁用")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新用户状态失败")
    } finally {
      setStatusBusyID(null)
    }
  }

  async function saveLimits() {
    if (selectedOnPage.length === 0) return
    const concurrency = batchConcurrency.trim() === "" ? undefined : Number(batchConcurrency)
    const rpmLimit = batchRPM.trim() === "" ? undefined : Number(batchRPM)
    if (concurrency === undefined && rpmLimit === undefined) {
      toast.error("至少填写并发上限或每分钟请求数")
      return
    }
    if ((concurrency !== undefined && (!Number.isInteger(concurrency) || concurrency < 0)) || (rpmLimit !== undefined && (!Number.isInteger(rpmLimit) || rpmLimit < 0))) {
      toast.error("并发上限和每分钟请求数必须是非负整数")
      return
    }
    setBusy(true)
    try {
      await apiFetch(`/relay-stations/${stationID}/users/batch-limits`, { method: "POST", body: JSON.stringify({ user_ids: selectedOnPage.map((user) => user.id), ...(concurrency === undefined ? {} : { concurrency }), ...(rpmLimit === undefined ? {} : { rpm_limit: rpmLimit }) }) })
      setSelected([])
      setBatchConcurrency("")
      setBatchRPM("")
      await users.refetch()
      toast.success(`已批量更新 ${selectedOnPage.length} 个用户的限额`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "批量更新用户限额失败")
    } finally {
      setBusy(false)
    }
  }

  async function deleteSelected() {
    if (selected.length === 0) return
    setBusy(true)
    try {
      const result = await apiFetch<{ affected: number; skipped_admins: number; failed: number[] }>(`/relay-stations/${stationID}/users/batch-delete`, { method: "POST", body: JSON.stringify({ user_ids: selected }) })
      setSelected([])
      setDeleteDialogOpen(false)
      await users.refetch()
      const suffix = result.failed.length ? `，${result.failed.length} 个失败` : ""
      toast.success(`已删除 ${result.affected} 个用户${result.skipped_admins ? `，跳过 ${result.skipped_admins} 个管理员` : ""}${suffix}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "批量删除用户失败")
    } finally {
      setBusy(false)
    }
  }

  const pageButtons = useMemo(() => {
    const total = users.data?.pages ?? 1
    const start = Math.max(1, Math.min(page - 2, total - 4))
    return Array.from({ length: Math.min(5, total) }, (_, index) => start + index)
  }, [page, users.data?.pages])

  return <Card className="gap-0 border border-border py-0 shadow-none">
    <CardHeader className="gap-3 px-4 py-3">
      <div className="flex items-center justify-between gap-3"><div className="min-w-0"><CardTitle className="flex items-center gap-2 text-sm font-semibold"><Users className="size-4 text-brand" />用户管理</CardTitle>{open ? <p className="mt-1 text-xs text-muted-foreground">中转站用户、余额、并发与 RPM 限额；区间消费按当前时间范围统计，排序基于全部匹配数据。</p> : null}</div><div className="flex items-center gap-2">{open && users.data ? <span className="text-xs text-muted-foreground">共 {users.data.total} 个</span> : null}<Button type="button" variant="ghost" size="icon" className="size-9" aria-label={open ? "收起用户管理" : "展开用户管理"} aria-expanded={open} onClick={() => setOpen((value) => !value)}><ChevronDown className={cn("size-4 transition-transform duration-200", open && "rotate-180")} /></Button></div></div>
      {open ? <div className="flex flex-wrap items-center justify-end gap-2"><div className="mr-auto flex min-w-40 items-center gap-2" aria-live="polite"><span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-success/10 text-success"><CircleDollarSign className="size-4" /></span><div><p className="text-[10px] text-muted-foreground">当前所有用户总余额（不包含管理员余额）</p><p className="font-mono text-sm font-semibold tabular-nums text-foreground">{users.data ? currency(users.data.total_balance) : users.loading ? "读取中…" : "-"}</p></div></div><div className="relative"><Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="用户名或邮箱" aria-label="按用户名或邮箱筛选用户" className="h-9 w-56 pl-8 text-xs" /></div><Input value={registrationIPDraft} onChange={(event) => setRegistrationIPDraft(event.target.value)} placeholder="注册 IP；点击列表 IP 可快速筛选" aria-label="按注册 IP 筛选用户" className="h-9 w-56 font-mono text-xs" />{registrationIP ? <Button type="button" variant="ghost" size="sm" className="h-8 text-xs" onClick={() => { setRegistrationIPDraft(""); setRegistrationIP(""); setPage(1); setSelected([]) }}>清除 IP 筛选</Button> : null}<Select value={range} onValueChange={(value) => { setRange(value as RelayUsageRange); setPage(1) }}><SelectTrigger className="h-9 w-28 text-xs" aria-label="用户消费时间范围"><SelectValue /></SelectTrigger><SelectContent>{ranges.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select><Select value={riskLevel} onValueChange={(value) => { setRiskLevel(value as RelayUserRiskLevel); setPage(1); setSelected([]) }}><SelectTrigger className="h-9 w-[132px] text-xs" aria-label="风险等级筛选"><ShieldAlert className="size-3.5 text-brand" /><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部风险</SelectItem><SelectItem value="high">高风险</SelectItem><SelectItem value="medium">中风险</SelectItem><SelectItem value="low">低风险</SelectItem><SelectItem value="normal">正常</SelectItem></SelectContent></Select><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={reset}><RotateCcw className="size-3.5" />重置</Button><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" disabled={users.refreshing} onClick={() => void users.refetch().catch(() => undefined)}><RefreshCw className={cn("size-3.5", users.refreshing && "animate-spin")} />刷新</Button></div> : null}
    </CardHeader>
    {open ? <CardContent className="border-t border-border p-0">
      {selected.length ? <div className="flex flex-wrap items-center gap-2 border-b border-brand/20 bg-brand/5 px-4 py-2.5"><span className="mr-1 text-xs font-medium text-brand">已选 {selected.length} 个用户</span><Input className="h-9 w-36 font-mono text-xs" inputMode="numeric" value={batchConcurrency} onChange={(event) => setBatchConcurrency(event.target.value)} placeholder="并发上限" aria-label="批量设置并发上限" /><Input className="h-9 w-40 font-mono text-xs" inputMode="numeric" value={batchRPM} onChange={(event) => setBatchRPM(event.target.value)} placeholder="每分钟请求数" aria-label="批量设置每分钟请求数" /><Button type="button" size="sm" className="h-9 gap-1.5" disabled={busy} onClick={() => void saveLimits()}><Save className="size-3.5" />保存限额</Button><Button type="button" variant="destructive" size="sm" className="h-9 gap-1.5" disabled={busy} onClick={() => setDeleteDialogOpen(true)}><Trash2 className="size-3.5" />删除用户</Button><span className="text-[11px] text-muted-foreground">留空字段保持不变，填 0 表示不限</span></div> : null}
      {users.data && !users.data.risk_data_complete ? <Alert className="m-4 border-warning/30 bg-warning/10 text-warning"><ShieldAlert /><AlertDescription>注册审计日志读取失败，当前风险分数可能不完整，请刷新或检查中转站管理 API。</AlertDescription></Alert> : null}
      {users.error ? <Alert variant="destructive" className="m-4"><CircleAlert /><AlertDescription>{users.error}</AlertDescription></Alert> : null}
      <div className="relative h-[520px] max-h-[520px]"><div className="h-full overflow-auto">{users.loading && !users.data ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">正在读取用户...</div> : <div className={`isolate ${tableMinWidth}`}><div className={`sticky top-0 z-10 grid ${tableMinWidth} ${tableColumns} border-b border-border bg-muted text-[11px] font-medium text-foreground/90 [&>*]:px-3 [&>*]:py-2`}><span><Checkbox checked={allPageSelected} onCheckedChange={(value) => { const ids = rows.map((user) => user.id); setSelected((current) => value === true ? Array.from(new Set([...current, ...ids])) : current.filter((id) => !ids.includes(id))) }} aria-label="选择当前页用户" /></span><SortButton field="id" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort} /><span>邮箱</span><span>用户名</span><SortButton field="registration_ip_count" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort}>注册 IP / 簇</SortButton><SortButton field="risk_score" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort}>风险分数</SortButton><SortButton field="balance" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort} /><SortButton field="usage" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort} /><SortButton field="current_concurrency" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort} /><span>每分钟请求数</span><span>状态</span><SortButton field="last_used_at" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort} /><SortButton field="created_at" sortBy={sortBy} sortOrder={sortOrder} onChange={changeSort} /></div>{rows.length === 0 ? <div className="px-4 py-12 text-center text-sm text-muted-foreground">没有匹配的用户</div> : rows.map((user) => <UserRow key={user.id} user={user} selected={selected.includes(user.id)} busy={busy || statusBusyID === user.id} onSelect={(checked) => setSelected((current) => checked ? Array.from(new Set([...current, user.id])) : current.filter((id) => id !== user.id))} onStatusChange={(status) => void updateStatus(user, status)} onRegistrationIPClick={filterByRegistrationIP} />)}</div>}</div>{users.refreshing && users.data ? <div className="pointer-events-none absolute inset-x-0 top-10 z-20 flex justify-center"><span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><RefreshCw className="size-3.5 animate-spin text-brand" />正在刷新用户列表</span></div> : null}</div><div className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-4 py-3"><div className="flex items-center gap-2 text-xs text-muted-foreground"><span>每页</span><Select value={String(pageSize)} onValueChange={(value) => { setPageSize(Number(value)); setPage(1); setSelected([]) }}><SelectTrigger className="h-8 w-20 text-xs"><SelectValue /></SelectTrigger><SelectContent>{[20, 50, 100].map((size) => <SelectItem key={size} value={String(size)}>{size} 条</SelectItem>)}</SelectContent></Select><span>{users.data ? `${users.data.page} / ${users.data.pages} 页` : "-"}</span>{users.data && !users.data.complete ? <span className="text-warning">{users.data.failed_users} 个用户消费读取失败</span> : null}</div><div className="flex items-center gap-1"><Button type="button" variant="outline" size="sm" className="h-8" disabled={page <= 1 || users.refreshing} onClick={() => setPage((current) => current - 1)}>上一页</Button>{pageButtons.map((value) => <Button key={value} type="button" variant={value === page ? "default" : "outline"} size="sm" className="size-8 p-0" disabled={users.refreshing} onClick={() => setPage(value)}>{value}</Button>)}<Button type="button" variant="outline" size="sm" className="h-8" disabled={page >= (users.data?.pages ?? 1) || users.refreshing} onClick={() => setPage((current) => current + 1)}>下一页</Button></div></div></CardContent> : null}
    <AlertDialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>确认删除用户？</AlertDialogTitle><AlertDialogDescription>将软删除已选的 {selected.length} 个用户及其 API Key。管理员账号会被自动跳过，此操作不会立即物理清除历史记录。</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel disabled={busy}>取消</AlertDialogCancel><AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" disabled={busy} onClick={(event) => { event.preventDefault(); void deleteSelected() }}>{busy ? "删除中..." : "确认删除"}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </Card>
}
