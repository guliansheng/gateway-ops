"use client"

import { useEffect, useState } from "react"
import { ChevronLeft, ChevronRight, Loader2, ReceiptText, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { degradationStatusMeta, formatDegradationCost, type DegradationUsagePage, type DegradationUsageScope } from "@/lib/group-degradation"
import { cn } from "@/lib/utils"

const usagePageSize = 20

function formatUsageTime(raw: string) {
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? "未知时间" : date.toLocaleString("zh-CN", { hour12: false })
}

function formatTokens(value: number) {
  return Number(value ?? 0).toLocaleString("zh-CN")
}

function Totals({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return <div className="min-w-0 rounded-md border border-border px-3 py-2"><dt className="text-[11px] text-muted-foreground">{label}</dt><dd className={cn("truncate font-mono text-sm font-medium", tone)} title={value}>{value}</dd></div>
}

function DegradationUsageTable({ stationID }: { stationID: number }) {
  const [scope, setScope] = useState<DegradationUsageScope>("all")
  const [page, setPage] = useState(1)
  const [data, setData] = useState<DegradationUsagePage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [version, setVersion] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError("")
    void apiFetch<DegradationUsagePage>(`/relay-stations/${stationID}/degradation-usage?scope=${scope}&page=${page}&page_size=${usagePageSize}`, { signal: controller.signal })
      .then((result) => { if (!controller.signal.aborted) setData(result) })
      .catch((requestError: unknown) => { if (!controller.signal.aborted) setError(requestError instanceof Error ? requestError.message : "费用记录读取失败") })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [stationID, scope, page, version])

  const items = data?.items ?? []
  const totals = data?.totals
  const pages = Math.max(1, data?.pages ?? 1)
  return <div className="space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <Select value={scope} onValueChange={(value) => { setScope(value as DegradationUsageScope); setPage(1) }}>
        <SelectTrigger className="h-9 w-40 text-xs" aria-label="筛选降智测试范围"><SelectValue /></SelectTrigger>
        <SelectContent><SelectItem value="all">分组和账号</SelectItem><SelectItem value="group">仅分组检测</SelectItem><SelectItem value="account">仅账号测试</SelectItem></SelectContent>
      </Select>
      <div className="flex items-center gap-2"><span className="text-[11px] text-muted-foreground">共 {data?.total ?? 0} 条记录</span><Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" disabled={loading} onClick={() => setVersion((value) => value + 1)}><RefreshCw className={cn("size-3.5", loading && "animate-spin")} />刷新</Button></div>
    </div>
    <dl className="grid grid-cols-2 gap-2 sm:grid-cols-5">
      <Totals label="测试次数" value={formatTokens(totals?.runs ?? 0)} />
      <Totals label="输入 token" value={formatTokens(totals?.input_tokens ?? 0)} />
      <Totals label="输出 token" value={formatTokens(totals?.output_tokens ?? 0)} />
      <Totals label="基础成本" value={`$${formatDegradationCost(totals?.base_cost)}`} />
      <Totals label="实际扣费" value={`$${formatDegradationCost(totals?.actual_cost)}`} tone="text-brand" />
    </dl>
    {error ? <p role="alert" className="text-sm text-danger">{error}</p> : null}
    <div className="overflow-x-auto rounded-md border border-border">
      <Table className="min-w-[900px] text-xs">
        <TableHeader><TableRow className="hover:bg-transparent">
          <TableHead className="whitespace-nowrap">时间</TableHead>
          <TableHead className="whitespace-nowrap">范围</TableHead>
          <TableHead className="whitespace-nowrap">方式 · 模型</TableHead>
          <TableHead className="whitespace-nowrap">调用账号</TableHead>
          <TableHead className="whitespace-nowrap text-right">输入 token</TableHead>
          <TableHead className="whitespace-nowrap text-right">输出 token</TableHead>
          <TableHead className="whitespace-nowrap text-right">基础成本</TableHead>
          <TableHead className="whitespace-nowrap text-right">账号倍率</TableHead>
          <TableHead className="whitespace-nowrap text-right">实际费用</TableHead>
          <TableHead className="whitespace-nowrap">结果</TableHead>
        </TableRow></TableHeader>
        <TableBody>
          {loading && !items.length ? <TableRow><TableCell colSpan={10} className="h-24 text-center text-muted-foreground"><span className="inline-flex items-center gap-2"><Loader2 className="size-4 animate-spin" />正在读取费用记录</span></TableCell></TableRow> : null}
          {!loading && !items.length ? <TableRow><TableCell colSpan={10} className="h-24 text-center text-muted-foreground">暂无降智测试费用记录。测试结束约几秒后自动写入。</TableCell></TableRow> : null}
          {items.map((item) => {
            const meta = degradationStatusMeta[item.status] ?? degradationStatusMeta.inconclusive
            return <TableRow key={`${item.scope}-${item.id}`}>
              <TableCell className="whitespace-nowrap font-mono text-[11px]">{formatUsageTime(item.created_at)}</TableCell>
              <TableCell className="max-w-40"><span className="block truncate" title={`${item.scope === "group" ? "分组" : "账号"} · ${item.scope_name || item.scope_id}`}>{item.scope === "group" ? "分组" : "账号"} · {item.scope_name || item.scope_id}</span></TableCell>
              <TableCell className="max-w-48"><span className="block truncate" title={`${item.method === "candy" ? "糖果" : "鹈鹕"} · ${item.model}`}>{item.method === "candy" ? "糖果" : "鹈鹕"} · {item.model || "未记录模型"}</span></TableCell>
              <TableCell className="max-w-40"><span className={cn("block truncate", item.used_account_name ? "" : "text-muted-foreground")} title={item.used_account_name || "尚未匹配到扣费记录"}>{item.used_account_name || "未匹配"}</span></TableCell>
              <TableCell className="text-right font-mono">{formatTokens(item.input_tokens)}</TableCell>
              <TableCell className="text-right font-mono">{formatTokens(item.output_tokens)}</TableCell>
              <TableCell className="text-right font-mono">${formatDegradationCost(item.base_cost)}</TableCell>
              <TableCell className="text-right font-mono">{item.cost_multiplier > 0 ? `×${formatDegradationCost(item.cost_multiplier)}` : "—"}</TableCell>
              <TableCell className="text-right font-mono font-medium">${formatDegradationCost(item.actual_cost)}</TableCell>
              <TableCell className={cn("whitespace-nowrap", meta.tone)}>{meta.label}</TableCell>
            </TableRow>
          })}
        </TableBody>
      </Table>
    </div>
    <div className="flex flex-wrap items-center justify-between gap-2">
      <p className="text-[11px] leading-5 text-muted-foreground">费用取自 Sub2API 扣费记录，按测试实际使用的账号倍率折算；未匹配到扣费记录的行会显示回退账号与 0 费用。</p>
      <div className="flex items-center gap-2"><span className="font-mono text-[11px] text-muted-foreground">第 {data?.page ?? page} / {pages} 页</span><Button type="button" variant="outline" size="sm" className="h-8 gap-1" disabled={loading || page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}><ChevronLeft className="size-3.5" />上一页</Button><Button type="button" variant="outline" size="sm" className="h-8 gap-1" disabled={loading || page >= pages} onClick={() => setPage((value) => Math.min(pages, value + 1))}>下一页<ChevronRight className="size-3.5" /></Button></div>
    </div>
  </div>
}

/** 工具栏入口：统计分组与账号降智测试的真实扣费，分页展示。 */
export function DegradationUsageButton({ stationID, disabled = false }: { stationID: number; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  useEffect(() => setOpen(false), [stationID])
  return <Dialog open={open} onOpenChange={setOpen}>
    <Button type="button" variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9" disabled={disabled} aria-label="降智测试费用统计" onClick={() => setOpen(true)}><ReceiptText className="size-3.5" />测试费用</Button>
    {open ? <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-6xl"><DialogHeader className="border-b border-border pb-4"><DialogTitle>降智测试费用统计</DialogTitle><DialogDescription>汇总分组定时检测与账号手动测试的实际扣费，按测试账号倍率折算。</DialogDescription></DialogHeader><DegradationUsageTable key={stationID} stationID={stationID} /></DialogContent> : null}
  </Dialog>
}
