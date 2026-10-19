"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { AlertTriangle, History, RefreshCw, ShieldAlert } from "lucide-react"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import type { RelayUserAutoDisableLogResponse, UserRiskConfig, UserRiskReason } from "@/lib/api-types"
import { cn } from "@/lib/utils"

interface UserAutoDisableLogDialogProps {
  open: boolean
  stationID: number
  onOpenChange: (open: boolean) => void
}

function dateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN")
}

function RuleCard({ title, condition, observed, threshold, hitThreshold, base, increment, weight, unit }: {
  title: string
  condition: string
  observed: string
  threshold: string
  hitThreshold?: number
  base?: number
  increment?: number
  weight?: number
  unit?: string
}) {
  const formula = base == null
    ? `命中 +${weight ?? 0} 分；未命中 0 分`
    : base <= 0
      ? "基础分为 0，本项不计分"
      : `观察值 < ${hitThreshold} 时 0 分；达到后 min(100, ${base} + max(观察值 − ${hitThreshold}, 0) × ${increment ?? 0}) 分`
  return <article className="rounded-md border border-border bg-card p-3">
    <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="text-xs font-semibold text-foreground">{title}</h4><Badge variant="outline" className="font-mono text-[10px]">{base == null ? `${weight ?? 0} 分` : `${base} 基础分 + ${increment ?? 0} 分/${unit}`}</Badge></div>
    <p className="mt-1.5 text-[11px] leading-5 text-muted-foreground"><span className="font-medium text-foreground">触发条件：</span>{condition}</p>
    <p className="text-[11px] leading-5 text-muted-foreground"><span className="font-medium text-foreground">观察与阈值：</span>{observed}；{threshold}</p>
    <p className="mt-1 rounded bg-muted/50 px-2 py-1.5 font-mono text-[10px] leading-4 text-foreground">{formula}</p>
  </article>
}

function RiskRules({ config }: { config: UserRiskConfig }) {
  return <section className="space-y-2 rounded-lg border border-border p-3">
    <div className="flex items-center gap-2"><ShieldAlert className="size-4 text-brand" /><h3 className="text-sm font-semibold">当前触发条件与分数标准</h3></div>
    <p className="text-[11px] leading-5 text-muted-foreground">当前总分达到自动禁用阈值才触发；单项分数相加后总分封顶 100。观察窗口为最近 {config.lookback_minutes} 分钟，并发窗口为 {config.concurrency_window_seconds} 秒。</p>
    <div className="grid gap-2 sm:grid-cols-2">
      <RuleCard title="同注册 IP 多账号" condition={`同一注册 IP 的账号数 ≥ ${config.registration_ip_min_accounts}`} observed="统计可读取到的成功注册审计记录中，同一注册 IP 关联的账号数（不受并发窗口限制）" threshold={`${config.registration_ip_min_accounts} 个账号起计分`} hitThreshold={config.registration_ip_min_accounts} base={config.registration_ip_weight} increment={config.registration_ip_increment_weight} unit="账号" />
      <RuleCard title="同 IP 不同账号并发" condition={`同一 IP 在并发窗口内同时调用的不同账号数 ≥ ${config.same_ip_concurrent_min_users}`} observed="统计发生并发重叠的不同账号数" threshold={`${config.same_ip_concurrent_min_users} 个账号起计分`} hitThreshold={config.same_ip_concurrent_min_users} base={config.same_ip_concurrent_weight} increment={config.same_ip_concurrent_increment_weight} unit="账号" />
      <RuleCard title="同账号多 IP 并发" condition={`同一账号在并发窗口内通过不同 IP 同时调用，IP 数 ≥ ${config.same_user_multi_ip_min_ips}`} observed="统计发生并发调用的不同 IP 数" threshold={`${config.same_user_multi_ip_min_ips} 个 IP 起计分`} hitThreshold={config.same_user_multi_ip_min_ips} base={config.same_user_multi_ip_weight} increment={config.same_user_multi_ip_increment_weight} unit="IP" />
      <RuleCard title="疑似机器注册邮箱" condition="Gmail 邮箱本地部分按 . 或 _ 分为两段；每段至少 2 个字符，第二段至少 5 个字符且包含数字" observed="按邮箱格式条件判断命中/未命中" threshold="命中时一次性加分" weight={config.suspicious_email_weight} />
      <RuleCard title="新账号暂无 API 请求" condition="账号创建不超过 24 小时、无最后使用时间记录，且最近观察窗口 Token 总数为 0" observed="核对账号创建时间、最后使用时间及窗口 Token 数" threshold="三项同时满足时命中" weight={config.new_account_no_usage_weight} />
    </div>
    <div className="rounded-md border border-warning/25 bg-warning/5 px-3 py-2 text-[11px] leading-5 text-muted-foreground">
      <p className="font-medium text-warning">执行门槛</p>
      <p>风险评估与自动禁用均需开启，风险数据必须完整；仅禁用普通且当前 active 的账号。管理员、已禁用账号和数据不完整的评估会跳过。</p>
      <p>自动禁用总分阈值：<span className="font-mono font-semibold text-foreground">{config.auto_disable_threshold} 分</span>；自动禁用：<span className="font-medium text-foreground">{config.auto_disable_enabled ? "已开启" : "未开启"}</span>。</p>
    </div>
  </section>
}

function ReasonDetails({ reasons }: { reasons: UserRiskReason[] }) {
  if (reasons.length === 0) return <p className="text-xs text-muted-foreground">没有保存到具体风险信号明细。</p>
  return <div className="space-y-2">{reasons.map((reason, index) => {
    const quantitySignal = reason.key === "registration_ip" || reason.key === "same_ip_concurrent" || reason.key === "same_user_multi_ip"
    const unit = reason.key === "same_user_multi_ip" ? "个 IP" : "个账号"
    const observed = reason.observed_value ?? `${reason.observed ?? "-"}${unit}`
    return <div key={`${reason.key}-${index}`} className="rounded-md border border-border bg-muted/15 p-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-xs font-semibold text-foreground">{reason.label}</p><Badge variant="outline" className="font-mono text-[10px]">+{reason.score} 分</Badge></div>
      <p className="mt-1 text-[11px] leading-5 text-muted-foreground"><span className="font-medium text-foreground">命中条件：</span>{reason.condition}</p>
      <p className="text-[11px] leading-5 text-muted-foreground"><span className="font-medium text-foreground">观察值：</span>{observed}{quantitySignal && reason.threshold != null ? `；命中阈值：${reason.threshold} ${unit}` : ""}</p>
      {reason.base_score != null ? <p className="text-[11px] leading-5 text-muted-foreground"><span className="font-medium text-foreground">分数标准：</span>{quantitySignal ? `基础 ${reason.base_score} 分，递增 ${reason.increment_score ?? 0} 分/${unit}` : `命中固定加 ${reason.base_score} 分`}</p> : null}
      <p className="mt-1 rounded bg-muted px-2 py-1.5 font-mono text-[10px] leading-4 text-foreground">{reason.calculation}</p>
    </div>
  })}</div>
}

export function UserAutoDisableLogDialog({ open, stationID, onOpenChange }: UserAutoDisableLogDialogProps) {
  const [data, setData] = useState<RelayUserAutoDisableLogResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")
  const requestID = useRef(0)

  const load = useCallback(async (refresh = false) => {
    const request = ++requestID.current
    if (refresh) setRefreshing(true)
    else setLoading(true)
    setError("")
    try {
      const result = await apiFetch<RelayUserAutoDisableLogResponse>(`/relay-stations/${stationID}/user-risk-auto-disable-logs?limit=100`)
      if (request === requestID.current) setData(result)
    } catch (cause) {
      if (request === requestID.current) setError(cause instanceof Error ? cause.message : "读取自动禁用记录失败")
    } finally {
      if (request === requestID.current) {
        setLoading(false)
        setRefreshing(false)
      }
    }
  }, [stationID])

  useEffect(() => {
    if (open) void load()
    else requestID.current += 1
  }, [open, load])

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col overflow-hidden p-0 sm:max-w-[960px]">
      <DialogHeader className="shrink-0 border-b border-border px-4 py-4 sm:px-6">
        <div className="flex flex-wrap items-center justify-between gap-3 pr-7"><div><DialogTitle className="flex items-center gap-2"><History className="size-4 text-brand" />自动禁用记录</DialogTitle><DialogDescription className="mt-1">只展示远端禁用成功后保存的真实记录。记录中的原因与分数为触发当时的快照。</DialogDescription></div><Button type="button" variant="outline" size="sm" className="h-8 gap-1.5" disabled={loading || refreshing} onClick={() => void load(true)}><RefreshCw className={cn("size-3.5", (loading || refreshing) && "animate-spin")} />刷新</Button></div>
      </DialogHeader>
      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:px-6">
        {error ? <Alert variant="destructive"><AlertTriangle /><AlertDescription>{error}</AlertDescription></Alert> : null}
        {loading && !data ? <div className="flex min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground"><RefreshCw className="size-4 animate-spin" />正在读取自动禁用记录…</div> : null}
        {data ? <>
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-brand/20 bg-brand/5 px-3 py-2.5 text-xs"><span className="text-muted-foreground">最近最多展示 100 条成功禁用记录</span><span className="font-medium text-foreground">当前记录 {data.logs.length} 条</span></div>
          {data.logs.length === 0 ? <div className="rounded-lg border border-dashed border-border px-4 py-10 text-center"><History className="mx-auto size-6 text-muted-foreground" /><p className="mt-2 text-sm font-medium text-foreground">暂无自动禁用记录</p><p className="mt-1 text-xs text-muted-foreground">自动禁用成功后，用户、风险分数和触发原因会记录在此处。</p></div> : <div className="space-y-3">{data.logs.map((log) => <article key={log.id} className="overflow-hidden rounded-lg border border-border">
            <header className="flex flex-wrap items-start justify-between gap-3 border-b border-border bg-muted/30 px-3 py-3 sm:px-4"><div className="min-w-0"><p className="truncate text-sm font-semibold text-foreground">{log.email || `用户 #${log.user_id}`} <span className="font-mono text-[11px] font-normal text-muted-foreground">#{log.user_id}</span></p>{log.username ? <p className="mt-0.5 text-xs text-muted-foreground">用户名：{log.username}</p> : null}<p className="mt-1 text-[11px] text-muted-foreground">触发时间：{dateTime(log.triggered_at)}</p></div><div className="flex shrink-0 items-center gap-2"><Badge variant="outline" className="border-danger/30 bg-danger/10 font-mono text-danger">风险 {log.risk_score} 分</Badge><span className="text-xs text-muted-foreground">/ 阈值 {log.threshold} 分</span></div></header>
            <div className="space-y-2.5 p-3 sm:p-4"><h4 className="text-xs font-semibold text-foreground">自动禁用原因与逐项计分</h4><ReasonDetails reasons={log.reasons} /></div>
          </article>)}</div>}
          <RiskRules config={data.config} />
          {data.examples.length ? <section className="space-y-2 rounded-lg border border-border p-3"><h3 className="text-sm font-semibold">当前配置的计算示例</h3><div className="overflow-x-auto"><Table className="min-w-[600px] text-xs"><TableHeader><TableRow><TableHead>风险场景</TableHead><TableHead>触发场景</TableHead><TableHead className="text-right">得分</TableHead><TableHead>建议</TableHead></TableRow></TableHeader><TableBody>{data.examples.map((example, index) => <TableRow key={`${example.name}-${index}`}><TableCell className="font-medium">{example.name}</TableCell><TableCell className="whitespace-normal text-muted-foreground">{example.scenario}</TableCell><TableCell className="text-right font-mono font-semibold">{example.score}</TableCell><TableCell className="whitespace-normal text-muted-foreground">{example.recommendation}</TableCell></TableRow>)}</TableBody></Table></div></section> : null}
        </> : null}
      </div>
      <DialogFooter className="shrink-0 border-t border-border px-4 py-3 sm:px-6"><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>关闭</Button></DialogFooter>
    </DialogContent>
  </Dialog>
}
