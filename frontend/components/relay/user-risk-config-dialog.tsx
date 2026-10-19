import { useEffect, useRef, useState, type ComponentType, type ReactNode } from "react"
import { AlertTriangle, CircleHelp, Gauge, ListChecks, Power, RefreshCw, ShieldCheck, Timer } from "lucide-react"
import { toast } from "sonner"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import type { RelayUserRiskConfigResponse, UserRiskConfig, UserRiskExample } from "@/lib/api-types"
import { cn } from "@/lib/utils"

interface UserRiskConfigDialogProps {
  open: boolean
  stationID: number
  onOpenChange: (open: boolean) => void
  onSaved: () => Promise<void> | void
}

type UserRiskConfigDraft = {
  enabled: boolean
  lookback_minutes: string
  concurrency_window_seconds: string
  registration_ip_weight: string
  registration_ip_increment_weight: string
  same_ip_concurrent_weight: string
  same_ip_concurrent_increment_weight: string
  same_user_multi_ip_weight: string
  same_user_multi_ip_increment_weight: string
  suspicious_email_weight: string
  new_account_no_usage_weight: string
  registration_ip_min_accounts: string
  same_ip_concurrent_min_users: string
  same_user_multi_ip_min_ips: string
  auto_disable_enabled: boolean
  auto_disable_threshold: string
}

type NumericRiskField = Exclude<keyof UserRiskConfigDraft, "enabled" | "auto_disable_enabled">

function toDraft(config: UserRiskConfig): UserRiskConfigDraft {
  return {
    enabled: config.enabled,
    lookback_minutes: String(config.lookback_minutes),
    concurrency_window_seconds: String(config.concurrency_window_seconds),
    registration_ip_weight: String(config.registration_ip_weight),
    registration_ip_increment_weight: String(config.registration_ip_increment_weight),
    same_ip_concurrent_weight: String(config.same_ip_concurrent_weight),
    same_ip_concurrent_increment_weight: String(config.same_ip_concurrent_increment_weight),
    same_user_multi_ip_weight: String(config.same_user_multi_ip_weight),
    same_user_multi_ip_increment_weight: String(config.same_user_multi_ip_increment_weight),
    suspicious_email_weight: String(config.suspicious_email_weight),
    new_account_no_usage_weight: String(config.new_account_no_usage_weight),
    registration_ip_min_accounts: String(config.registration_ip_min_accounts),
    same_ip_concurrent_min_users: String(config.same_ip_concurrent_min_users),
    same_user_multi_ip_min_ips: String(config.same_user_multi_ip_min_ips),
    auto_disable_enabled: config.auto_disable_enabled,
    auto_disable_threshold: String(config.auto_disable_threshold),
  }
}

function toConfig(draft: UserRiskConfigDraft): UserRiskConfig {
  return {
    enabled: draft.enabled,
    lookback_minutes: Number(draft.lookback_minutes),
    concurrency_window_seconds: Number(draft.concurrency_window_seconds),
    registration_ip_weight: Number(draft.registration_ip_weight),
    registration_ip_increment_weight: Number(draft.registration_ip_increment_weight),
    same_ip_concurrent_weight: Number(draft.same_ip_concurrent_weight),
    same_ip_concurrent_increment_weight: Number(draft.same_ip_concurrent_increment_weight),
    same_user_multi_ip_weight: Number(draft.same_user_multi_ip_weight),
    same_user_multi_ip_increment_weight: Number(draft.same_user_multi_ip_increment_weight),
    suspicious_email_weight: Number(draft.suspicious_email_weight),
    new_account_no_usage_weight: Number(draft.new_account_no_usage_weight),
    registration_ip_min_accounts: Number(draft.registration_ip_min_accounts),
    same_ip_concurrent_min_users: Number(draft.same_ip_concurrent_min_users),
    same_user_multi_ip_min_ips: Number(draft.same_user_multi_ip_min_ips),
    auto_disable_enabled: draft.auto_disable_enabled,
    auto_disable_threshold: Number(draft.auto_disable_threshold),
  }
}

function Section({ icon: Icon, title, description, tone = "brand", action, children }: {
  icon: ComponentType<{ className?: string }>
  title: string
  description: string
  tone?: "brand" | "warning"
  action?: ReactNode
  children: ReactNode
}) {
  return <section className="overflow-hidden rounded-lg border border-border bg-card">
    <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border bg-muted/30 px-3 py-2.5">
      <div className="flex min-w-0 items-start gap-2">
        <span className={cn("mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md", tone === "warning" ? "bg-warning/10 text-warning" : "bg-brand/10 text-brand")}><Icon className="size-3.5" /></span>
        <div className="min-w-0"><h3 className="text-xs font-semibold text-foreground">{title}</h3><p className="mt-0.5 text-[11px] leading-4 text-muted-foreground">{description}</p></div>
      </div>
      {action ? <div className="shrink-0">{action}</div> : null}
    </header>
    <div className="space-y-2.5 p-3">{children}</div>
  </section>
}
function NumberField({ id, label, value, suffix, description, help, disabled, onChange }: {
	id: string
	label: string
	value: string
	suffix: string
	description?: string
	help?: string
	disabled: boolean
	onChange: (value: string) => void
}) {
	return <div className="min-w-0 rounded-md border border-border bg-muted/15 p-2.5">
		<div className="flex items-center gap-1.5"><Label htmlFor={id} className="text-xs font-medium">{label}</Label>{help ? <Tooltip delayDuration={150}><TooltipTrigger asChild><button type="button" className="rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`查看${label}说明`}><CircleHelp className="size-3.5" /></button></TooltipTrigger><TooltipContent side="top" className="max-w-96 text-xs leading-5">{help}</TooltipContent></Tooltip> : null}</div>
		<div className="mt-1.5 flex items-center gap-2"><Input id={id} inputMode="numeric" value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} className="h-8 min-w-0 flex-1 text-xs tabular-nums" aria-label={label} /><span className="shrink-0 text-[11px] text-muted-foreground">{suffix}</span></div>
		{description ? <p className="mt-1 text-[10px] leading-4 text-muted-foreground">{description}</p> : null}
	</div>
}

const weightFields: Array<{ key: NumericRiskField; label: string; suffix: string; description: string }> = [
  { key: "registration_ip_weight", label: "同 IP 注册基础分", suffix: "分", description: "达到注册账号阈值时的起始分" },
  { key: "registration_ip_increment_weight", label: "同 IP 注册递增步长", suffix: "分/账号", description: "超过阈值后，每多注册 1 个账号增加的分数" },
  { key: "same_ip_concurrent_weight", label: "同 IP 不同账号并发基础分", suffix: "分", description: "达到同 IP 不同账号并发数阈值时的起始分" },
  { key: "same_ip_concurrent_increment_weight", label: "同 IP 不同账号并发递增步长", suffix: "分/账号", description: "超过阈值后，每增加 1 个同 IP 下同时调用的不同账号增加的分数" },
  { key: "same_user_multi_ip_weight", label: "同账号多 IP 基础分", suffix: "分", description: "达到并发 IP 阈值时的起始分" },
  { key: "same_user_multi_ip_increment_weight", label: "同账号多 IP 递增步长", suffix: "分/IP", description: "超过阈值后，每多 1 个并发 IP 增加的分数" },
  { key: "suspicious_email_weight", label: "疑似机器邮箱权重", suffix: "分", description: "该二元信号命中时一次性加分" },
  { key: "new_account_no_usage_weight", label: "新账号无请求权重", suffix: "分", description: "该二元信号命中时一次性加分" },
]

const thresholdFields: Array<{ key: NumericRiskField; label: string; suffix: string }> = [
  { key: "registration_ip_min_accounts", label: "同 IP 注册命中阈值", suffix: "个账号" },
  { key: "same_ip_concurrent_min_users", label: "同 IP 不同账号并发数阈值", suffix: "个账号" },
  { key: "same_user_multi_ip_min_ips", label: "同账号多 IP 命中阈值", suffix: "个 IP" },
]

function validateConfig(config: UserRiskConfig) {
  if (!Number.isInteger(config.lookback_minutes) || config.lookback_minutes < 1 || config.lookback_minutes > 60) return "风险观察窗口必须是 1 到 60 分钟"
  if (!Number.isInteger(config.concurrency_window_seconds) || config.concurrency_window_seconds < 5 || config.concurrency_window_seconds > 300) return "并发重叠窗口必须是 5 到 300 秒"
  const weights: Array<[string, number]> = [
    ["同 IP 注册基础分", config.registration_ip_weight], ["同 IP 注册递增步长", config.registration_ip_increment_weight],
    ["同 IP 不同账号并发基础分", config.same_ip_concurrent_weight], ["同 IP 不同账号并发递增步长", config.same_ip_concurrent_increment_weight],
    ["同账号多 IP 基础分", config.same_user_multi_ip_weight], ["同账号多 IP 递增步长", config.same_user_multi_ip_increment_weight],
    ["疑似机器邮箱权重", config.suspicious_email_weight], ["新账号无请求权重", config.new_account_no_usage_weight],
  ]
  for (const [label, value] of weights) if (!Number.isInteger(value) || value < 0 || value > 100) return `${label}必须是 0 到 100 的整数`
  const thresholds: Array<[string, number]> = [
    ["同 IP 注册账号数阈值", config.registration_ip_min_accounts], ["同 IP 不同账号并发数阈值", config.same_ip_concurrent_min_users], ["同账号并发 IP 数阈值", config.same_user_multi_ip_min_ips],
  ]
  for (const [label, value] of thresholds) if (!Number.isInteger(value) || value < 2 || value > 20) return `${label}必须是 2 到 20 的整数`
  if (!Number.isInteger(config.auto_disable_threshold) || config.auto_disable_threshold < 1 || config.auto_disable_threshold > 100) return "自动禁用风险阈值必须是 1 到 100 的整数"
  return ""
}

function ExamplesTable({ examples }: { examples: UserRiskExample[] }) {
  if (examples.length === 0) return <p className="border border-dashed border-border px-3 py-5 text-center text-[11px] text-muted-foreground">暂无场景示例</p>
  return <div className="overflow-x-auto border border-border"><Table className="min-w-[680px] text-xs"><TableHeader><TableRow className="bg-muted/30 hover:bg-muted/30"><TableHead className="w-36 text-[11px]">场景</TableHead><TableHead className="min-w-64 text-[11px]">命中条件</TableHead><TableHead className="w-20 text-right text-[11px]">分数</TableHead><TableHead className="min-w-52 text-[11px]">建议</TableHead></TableRow></TableHeader><TableBody>{examples.map((example) => <TableRow key={`${example.name}-${example.scenario}`}><TableCell className="font-medium">{example.name}</TableCell><TableCell className="whitespace-normal leading-5 text-muted-foreground">{example.scenario}</TableCell><TableCell className="text-right font-mono font-semibold tabular-nums">{example.score}</TableCell><TableCell className="whitespace-normal leading-5 text-muted-foreground">{example.recommendation}</TableCell></TableRow>)}</TableBody></Table></div>
}

export function UserRiskConfigDialog({ open, stationID, onOpenChange, onSaved }: UserRiskConfigDialogProps) {
  const [response, setResponse] = useState<RelayUserRiskConfigResponse | null>(null)
  const [draft, setDraft] = useState<UserRiskConfigDraft | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const requestID = useRef(0)

  useEffect(() => {
    if (!open) return
    const request = ++requestID.current
    let cancelled = false
    setLoading(true)
    setError("")
    apiFetch<RelayUserRiskConfigResponse>(`/relay-stations/${stationID}/user-risk-config`).then((data) => {
      if (cancelled || request !== requestID.current) return
      setResponse(data)
      setDraft(toDraft(data.config))
    }).catch((cause: unknown) => {
      if (!cancelled && request === requestID.current) setError(cause instanceof Error ? cause.message : "读取风险配置失败")
    }).finally(() => {
      if (!cancelled && request === requestID.current) setLoading(false)
    })
    return () => { cancelled = true }
  }, [open, stationID])

  function update<K extends keyof UserRiskConfigDraft>(key: K, value: UserRiskConfigDraft[K]) {
    setDraft((current) => current ? { ...current, [key]: value } : current)
    setError("")
  }

  async function save() {
    if (!draft || busy) return
    const config = toConfig(draft)
    const validationError = validateConfig(config)
    if (validationError) {
      setError(validationError)
      return
    }
    setBusy(true)
    setError("")
    try {
      const saved = await apiFetch<RelayUserRiskConfigResponse>(`/relay-stations/${stationID}/user-risk-config`, { method: "PUT", body: JSON.stringify(config) })
      setResponse(saved)
      setDraft(toDraft(saved.config))
      await onSaved()
      toast.success("风险配置已保存，用户列表已刷新")
    } catch (cause: unknown) {
      const message = cause instanceof Error ? cause.message : "保存风险配置失败"
      setError(message)
      toast.error(message)
    } finally {
      setBusy(false)
    }
  }

  const disabled = loading || busy || draft === null
	return (
	  <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onOpenChange(false) }}>
	    <DialogContent className="flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col overflow-hidden p-0 sm:max-w-[900px]">
	      <DialogHeader className="shrink-0 border-b border-border px-4 py-4 sm:px-6">
	        <DialogTitle>风险评估配置</DialogTitle>
	        <DialogDescription>数量型信号按“基础分 + max(观察数量 − 命中阈值, 0) × 递增步长”计算，单项封顶 100。</DialogDescription>
	      </DialogHeader>
	      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:px-6">
	        {loading ? <div className="flex min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground"><RefreshCw className="size-4 animate-spin" />读取风险配置…</div> : null}
	        {error && !draft ? <Alert variant="destructive"><AlertTriangle /><AlertDescription>{error}</AlertDescription></Alert> : null}
		{draft ? <>
		  <Section icon={Power} title="启用风险评估" description="关闭后列表仍可管理用户，但不根据规则计算风险分数。" action={<Switch checked={draft.enabled} onCheckedChange={(value) => update("enabled", value)} disabled={disabled} aria-label="启用风险评估" />}><p className="text-[11px] text-muted-foreground">自动禁用独立控制，风险分数不会因关闭自动禁用而停止计算。</p></Section>
		  <Section icon={Timer} title="观察窗口" description="风险观察窗口决定读取多长时间的注册与调用记录；并发重叠窗口决定两个调用是否算作同时发生。">
		    <div className="grid gap-2.5 sm:grid-cols-2"><NumberField id="risk-lookback" label="风险观察窗口" value={draft.lookback_minutes} suffix="分钟" description="1–60 分钟" help="用于读取注册审计和调用记录的回看范围。例如设置 15 分钟，风险评估只使用最近 15 分钟内的数据；它决定评估覆盖多久，不表示一次调用必须持续这么久。窗口越大，越容易捕获较早的注册/调用行为，也会增加数据量和历史行为影响。" disabled={disabled} onChange={(v) => update("lookback_minutes", v)} /><NumberField id="risk-window" label="并发重叠窗口" value={draft.concurrency_window_seconds} suffix="秒" description="5–300 秒" help="用于判断两个调用是否算“同时发生”。如果两个请求的执行区间重叠，直接视为并发；如果上游没有可靠持续时间，则只要开始时间相差不超过该窗口也视为并发。例如设置 60 秒，两个不同账号通过同一 IP 在 60 秒内先后发起请求，会触发同 IP 不同账号并发判断。它不是限流时长，也不是风险观察总时长。" disabled={disabled} onChange={(v) => update("concurrency_window_seconds", v)} /></div>
		  </Section>
	          <Section icon={Gauge} title="基础分与递增步长" description="达到命中阈值计基础分；之后每增加一个账号或 IP 按步长加分。步长 0 表示不递增。">
	            <div className="grid gap-2.5 sm:grid-cols-2">{weightFields.map((field) => <NumberField key={field.key} id={`risk-${field.key}`} label={field.label} value={draft[field.key]} suffix={field.suffix} description={field.description} disabled={disabled} onChange={(v) => update(field.key, v)} />)}</div>
	          </Section>
	          <Section icon={ListChecks} title="命中阈值" description="数量达到该值时开始计基础分，超过后按递增步长加分。">
	            <div className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-3">{thresholdFields.map((field) => <NumberField key={field.key} id={`risk-${field.key}`} label={field.label} value={draft[field.key]} suffix={field.suffix} description="范围：2–20" disabled={disabled} onChange={(v) => update(field.key, v)} />)}</div>
	          </Section>
	          <Section icon={ShieldCheck} title="自动禁用" tone="warning" description="只在风险数据完整时执行，管理员账号不会被禁用。" action={<Switch checked={draft.auto_disable_enabled} onCheckedChange={(value) => update("auto_disable_enabled", value)} disabled={disabled} aria-label="启用自动禁用" />}>
	            <div className="rounded-md border border-warning/25 bg-warning/5 px-3 py-2.5"><p className="text-xs font-medium text-warning">自动禁用默认关闭 / 数据不完整时跳过</p><p className="mt-1 text-[11px] leading-4 text-muted-foreground">开启后，普通活跃用户分数达到设定值才会被禁用。</p></div>
	            <NumberField id="risk-disable-threshold" label="自动禁用风险阈值" value={draft.auto_disable_threshold} suffix="分" description="范围：1–100" disabled={disabled || !draft.auto_disable_enabled} onChange={(v) => update("auto_disable_threshold", v)} />
	          </Section>
	          <Section icon={ListChecks} title="场景分数示例" description="示例使用当前基础分、递增步长、命中阈值和自动禁用风险阈值计算；达到阈值的数量取最小值。">
	            <ExamplesTable examples={response?.examples ?? []} />
	          </Section>
	        </> : null}
	        {draft && error ? <Alert variant="destructive"><AlertTriangle /><AlertDescription>{error}</AlertDescription></Alert> : null}
	      </div>
	      <DialogFooter className="shrink-0 border-t border-border px-4 py-3 sm:px-6">
	        <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>取消</Button>
	        <Button type="button" disabled={disabled} onClick={() => void save()}>{busy ? <RefreshCw className="size-3.5 animate-spin" /> : null}保存配置</Button>
	      </DialogFooter>
	    </DialogContent>
	  </Dialog>
	)
}
