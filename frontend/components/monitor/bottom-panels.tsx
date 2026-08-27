"use client"

import { useMemo, useState } from "react"
import { toast } from "sonner"
import {
  AlertTriangle,
  ArrowUpRight,
  BellRing,
  CheckCircle2,
  CircleX,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Send,
  ShieldCheck,
  ShieldX,
  TestTube2,
  Trash2,
} from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { useCaptchaConfigs, useDashboardSummary, useNotificationChannels, useNotificationLogs } from "@/lib/queries"
import { apiFetch } from "@/lib/api"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { CaptchaFormDialog } from "@/components/monitor/captcha-form-dialog"
import { NotificationFormDialog } from "@/components/monitor/notification-form-dialog"
import type { LucideIcon } from "lucide-react"
import type {
  CaptchaConfig,
  NotificationChannel,
  NotificationEvent,
  NotificationChannelType,
  NotificationLog,
} from "@/lib/api-types"

type PillTone = "brand" | "blue" | "orange" | "danger" | "warning" | "sky" | "muted"

const eventMeta: Record<NotificationEvent, { icon: LucideIcon; cls: string; tag: PillTone }> = {
  balance_low: { icon: AlertTriangle, cls: "text-warning", tag: "warning" },
  login_failed: { icon: ShieldX, cls: "text-danger", tag: "danger" },
  captcha_failed: { icon: KeyRound, cls: "text-orange-600 dark:text-orange-400", tag: "orange" },
  rate_changed: { icon: ArrowUpRight, cls: "text-brand", tag: "brand" },
  monitor_failed: { icon: ShieldX, cls: "text-sky-600 dark:text-sky-400", tag: "sky" },
}

const eventLabel: Record<string, string> = {
  balance_low: "余额告警",
  rate_changed: "倍率变化",
  login_failed: "登录失败",
  captcha_failed: "验证码失败",
  monitor_failed: "采集失败",
  "": "测试通知",
}

const notificationTypeLabel: Record<NotificationChannelType, string> = {
  telegram: "Telegram",
  webhook: "Webhook",
  email: "邮件",
  wecom: "企业微信",
  dingtalk: "钉钉",
  feishu: "飞书",
  bark: "Bark",
}

const notificationTypeTone: Record<NotificationChannelType, PillTone> = {
  telegram: "sky",
  webhook: "blue",
  email: "orange",
  wecom: "brand",
  dingtalk: "danger",
  feishu: "blue",
  bark: "warning",
}

const captchaTypeTone: Record<string, PillTone> = {
  capsolver: "brand",
  "2captcha": "blue",
  anticaptcha: "orange",
  yescaptcha: "warning",
}

function absoluteTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(new Date(value))
}

function eventMetaFor(event?: string) {
  if (event && event in eventMeta) return eventMeta[event as NotificationEvent]
  return { icon: BellRing, cls: "text-muted-foreground", tag: "muted" as PillTone }
}

const pillToneClass: Record<PillTone, string> = {
  brand: "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/70 dark:bg-emerald-950/30 dark:text-emerald-400",
  blue: "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-800/70 dark:bg-blue-950/30 dark:text-blue-400",
  orange: "border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-800/70 dark:bg-orange-950/30 dark:text-orange-400",
  danger: "border-red-200 bg-red-50 text-red-700 dark:border-red-800/70 dark:bg-red-950/30 dark:text-red-400",
  warning: "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/70 dark:bg-amber-950/30 dark:text-amber-400",
  sky: "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-800/70 dark:bg-sky-950/30 dark:text-sky-400",
  muted: "border-border bg-muted text-muted-foreground",
}

function PillTag({ label, icon: Icon, tone = "brand", title }: { label: string; icon?: LucideIcon; tone?: PillTone; title?: string }) {
  return <span className={cn("inline-flex max-w-full items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium", pillToneClass[tone])} title={title ?? label}>{Icon ? <Icon className="size-3.5 shrink-0" /> : null}<span className="truncate">{label}</span></span>
}

export function AlertFeed() {
  const summary = useDashboardSummary()
  const items = summary.data?.recent_notification_logs ?? []

  return (
    <Card className="border border-border shadow-none lg:h-100">
      <CardHeader className="flex shrink-0 flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"告警动态"}</CardTitle>
        <span className="text-xs text-muted-foreground">{items.length > 0 ? `最近 ${items.length} 条` : ""}</span>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 px-0">
        {summary.loading ? (
          <p className="px-6 py-4 text-xs text-muted-foreground">{"加载中…"}</p>
        ) : items.length === 0 ? (
          <p className="px-6 py-4 text-xs text-muted-foreground">{"暂无告警记录"}</p>
        ) : (
          <ScrollArea type="hover" className="h-full">
            <ul className="divide-y divide-border">
              {items.map((a) => {
                const meta = eventMeta[a.event] ?? { icon: AlertTriangle, cls: "text-muted-foreground" }
                return (
                  <li key={a.id} className="flex items-center justify-between gap-3 px-6 py-3">
                    <div className="flex min-w-0 items-center gap-2.5">
                      <meta.icon className={cn("size-4 shrink-0", meta.cls)} />
                      <span className="truncate text-sm text-foreground">{a.subject}</span>
                    </div>
                    <span className="shrink-0 text-xs text-muted-foreground">{relativeTime(a.sent_at)}</span>
                  </li>
                )
              })}
            </ul>
          </ScrollArea>
        )}
      </CardContent>
    </Card>
  )
}

const captchaTypeLabel: Record<string, string> = {
  capsolver: "CapSolver",
  "2captcha": "2Captcha",
  anticaptcha: "AntiCaptcha",
  yescaptcha: "YesCaptcha",
}

export function CaptchaStatus({ fullPage = false }: { fullPage?: boolean }) {
  const { data, loading } = useCaptchaConfigs()
  const refresh = useTriggerRefresh()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [editing, setEditing] = useState<CaptchaConfig | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState<number | null>(null)

  async function handleDelete(c: CaptchaConfig) {
    const ok = await confirm({
      title: `删除打码配置 ${c.name}？`,
      description: "删除后引用此配置的渠道将无法自动过码，需要重新指定打码 provider。",
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    setBusy(c.id)
    try {
      await apiFetch(`/captcha-configs/${c.id}`, { method: "DELETE" })
      toast.success(`已删除 ${c.name}`)
      refresh()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "删除失败")
    } finally {
      setBusy(null)
    }
  }

  return (
    <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
      <CardHeader className="gap-3 px-4 py-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-sm font-semibold"><ShieldCheck className="size-4 text-brand" />{"验证码服务配置"}</CardTitle>
            {fullPage ? <p className="mt-1 text-xs text-muted-foreground">配置 Turnstile 打码 provider，供渠道登录时自动获取验证令牌。</p> : null}
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {fullPage && data ? <span className="text-xs text-muted-foreground">{data.length} 个配置</span> : null}
            {fullPage ? <Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => refresh()} aria-label="刷新验证码配置" title="刷新验证码配置"><RefreshCw className="size-3.5" />刷新</Button> : null}
            <Button
              size="sm"
              variant="outline"
              className="h-9 shrink-0 gap-1.5 text-xs"
              onClick={() => {
                setEditing(null)
                setOpen(true)
              }}
            >
              <Plus className="size-3.5" />
              {"新增"}
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="border-t border-border p-0">
        {fullPage ? (
          <div className="relative isolate h-[min(52vh,520px)]">
            <div className="h-full overflow-auto">
              {loading && !data ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">正在读取验证码配置...</div> : !data?.length ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂未配置打码 provider</div> : <div className="isolate min-w-[860px]">
                <div className="sticky top-0 z-30 grid grid-cols-[minmax(220px,1fr)_150px_minmax(240px,1.2fr)_110px_170px_112px] items-center border-b border-border bg-muted text-[11px] font-medium text-foreground/90 [&>*]:px-4 [&>*]:py-2"><span>配置名称</span><span>服务类型</span><span>接口地址</span><span>状态</span><span>更新时间</span><span className="fixed-column-shadow-right sticky right-0 z-40 flex items-center bg-muted">操作</span></div>
                {data.map((p) => <div key={p.id} className="grid min-h-14 grid-cols-[minmax(220px,1fr)_150px_minmax(240px,1.2fr)_110px_170px_112px] items-center border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-3"><div className="flex min-w-0 flex-col justify-center"><p className="truncate font-medium text-foreground" title={p.name}>{p.name}</p><p className="mt-0.5 truncate text-[11px] text-muted-foreground">ID #{p.id}</p></div><div className="flex items-center"><PillTag label={captchaTypeLabel[p.type] ?? p.type} icon={ShieldCheck} tone="brand" /></div><span className="truncate text-muted-foreground" title={p.endpoint || "未配置"}>{p.endpoint || "未配置"}</span><div className="flex items-center"><PillTag label={p.enabled ? "已启用" : "已禁用"} tone={p.enabled ? "brand" : "muted"} /></div><span className="font-mono tabular-nums text-muted-foreground">{relativeTime(p.updated_at)}</span><div className="fixed-column-shadow-right sticky right-0 z-20 flex items-center gap-1 bg-card"><Button size="icon" variant="ghost" className="size-8" aria-label={`编辑${p.name}`} title="编辑" onClick={() => { setEditing(p); setOpen(true) }}><Pencil className="size-3.5" /></Button><Button size="icon" variant="ghost" className="size-8 text-destructive hover:bg-destructive/10 hover:text-destructive" aria-label={`删除${p.name}`} title="删除" disabled={busy === p.id} onClick={() => handleDelete(p)}><Trash2 className="size-3.5" /></Button></div></div>)}
              </div>}
            </div>
          </div>
        ) : loading ? <p className="px-6 py-4 text-xs text-muted-foreground">{"加载中…"}</p> : !data || data.length === 0 ? <p className="px-6 py-4 text-xs text-muted-foreground">{"暂未配置打码 provider"}</p> : <ul className="divide-y divide-border">{data.map((p) => <li key={p.id} className="flex items-center justify-between gap-3 px-6 py-2.5"><div className="flex min-w-0 items-center gap-2.5"><span className={cn("size-2 shrink-0 rounded-full", p.enabled ? "bg-success" : "bg-muted-foreground/30")} /><div className="min-w-0"><p className="truncate text-sm font-medium text-foreground">{p.name}</p><p className="mt-0.5 truncate text-[11px] text-muted-foreground">{captchaTypeLabel[p.type] ?? p.type}</p></div></div><div className="flex shrink-0 items-center gap-1"><span className={cn("mr-1 text-xs", p.enabled ? "text-success" : "text-muted-foreground")}>{p.enabled ? "已启用" : "已禁用"}</span><Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => { setEditing(p); setOpen(true) }}><Pencil className="size-3.5" /></Button><Button size="icon" variant="ghost" className="h-7 w-7 text-destructive hover:bg-destructive/10 hover:text-destructive" disabled={busy === p.id} onClick={() => handleDelete(p)}><Trash2 className="size-3.5" /></Button></div></li>)}</ul>}
      </CardContent>

      <CaptchaFormDialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v)
          if (!v) setEditing(null)
        }}
        config={editing}
      />

      {confirmDialog}
    </Card>
  )
}

const notifyTypeIcon: Partial<Record<NotificationChannelType, LucideIcon>> = {
  telegram: Send,
  webhook: Send,
  email: Send,
  wecom: Send,
  dingtalk: Send,
  feishu: Send,
}

export function NotificationStatus({ fullPage = false }: { fullPage?: boolean }) {
  const { data, loading } = useNotificationChannels()
  const refresh = useTriggerRefresh()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [editing, setEditing] = useState<NotificationChannel | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState<number | null>(null)

  async function handleDelete(c: NotificationChannel) {
    const ok = await confirm({
      title: `删除通知渠道 ${c.name}？`,
      description: "删除后系统将不再向该渠道推送告警，历史发送记录会保留以便审计。",
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    setBusy(c.id)
    try {
      await apiFetch(`/notifications/channels/${c.id}`, { method: "DELETE" })
      toast.success(`已删除 ${c.name}`)
      refresh()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "删除失败")
    } finally {
      setBusy(null)
    }
  }

  async function handleTest(c: NotificationChannel) {
    setBusy(c.id)
    try {
      const res = await apiFetch<{ ok: boolean; error?: string }>(
        `/notifications/channels/${c.id}/test`,
        { method: "POST" },
      )
      if (res.ok) {
        toast.success(`已发送测试消息到 ${c.name}`)
      } else {
        toast.error(`测试失败：${res.error ?? "未知错误"}`)
      }
      refresh()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "测试失败")
    } finally {
      setBusy(null)
    }
  }

  async function handleToggle(c: NotificationChannel, enabled: boolean) {
    setBusy(c.id)
    try {
      await apiFetch(`/notifications/channels/${c.id}`, {
        method: "PUT",
        body: JSON.stringify({
          name: c.name,
          type: c.type,
          enabled,
          subscriptions: c.subscriptions || "[]",
        }),
      })
      toast.success(`${c.name} 已${enabled ? "启用" : "停用"}`)
      refresh()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "切换状态失败")
    } finally {
      setBusy(null)
    }
  }

  return (
    <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
      <CardHeader className="gap-3 px-4 py-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-sm font-semibold"><BellRing className="size-4 text-brand" />{"通知渠道管理"}</CardTitle>
            {fullPage ? <p className="mt-1 text-xs text-muted-foreground">管理推送目标、订阅规则和测试发送。</p> : null}
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {fullPage && data ? <span className="text-xs text-muted-foreground">{data.length} 个渠道</span> : null}
            {fullPage ? <Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => refresh()} aria-label="刷新通知渠道" title="刷新通知渠道"><RefreshCw className="size-3.5" />刷新</Button> : null}
            <Button
              size="sm"
              variant="outline"
              className="h-9 shrink-0 gap-1.5 text-xs"
              onClick={() => {
                setEditing(null)
                setOpen(true)
              }}
            >
              <Plus className="size-3.5" />
              {"新增"}
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="border-t border-border p-0">
        {fullPage ? (
          <div className="relative isolate h-[min(44vh,460px)]">
            <div className="h-full overflow-auto">
              {loading && !data ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">正在读取通知渠道...</div> : !data?.length ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂未配置通知渠道</div> : <div className="isolate min-w-[1000px]">
                <div className="sticky top-0 z-30 grid grid-cols-[minmax(230px,1fr)_130px_minmax(170px,0.8fr)_144px_160px_140px] items-center border-b border-border bg-muted text-[11px] font-medium text-foreground/90 [&>*]:px-4 [&>*]:py-2"><span>渠道名称</span><span>类型</span><span>订阅范围</span><span>状态</span><span>更新时间</span><span className="fixed-column-shadow-right sticky right-0 z-40 flex items-center bg-muted">操作</span></div>
                {data.map((c) => { const Icon = notifyTypeIcon[c.type] ?? Send; const subCount = parseSubCount(c.subscriptions); return <div key={c.id} className="grid min-h-14 grid-cols-[minmax(230px,1fr)_130px_minmax(170px,0.8fr)_144px_160px_140px] items-center border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-3"><div className="flex min-w-0 items-center gap-2.5"><Icon className={cn("size-4 shrink-0", c.enabled ? "text-brand" : "text-muted-foreground")} /><div className="min-w-0"><p className="truncate font-medium text-foreground" title={c.name}>{c.name}</p><p className="mt-0.5 text-[11px] text-muted-foreground">ID #{c.id}</p></div></div><div className="flex items-center"><PillTag label={notificationTypeLabel[c.type] ?? c.type} icon={Icon} tone={notificationTypeTone[c.type] ?? "muted"} /></div><span className="truncate text-muted-foreground" title={subCount === 0 ? "订阅全部事件" : `${subCount} 条订阅`}>{subCount === 0 ? "订阅全部事件" : `${subCount} 条订阅`}</span><div className="flex items-center gap-2"><Switch checked={c.enabled} onCheckedChange={(checked) => void handleToggle(c, checked)} disabled={busy === c.id} aria-label={`${c.name}${c.enabled ? "停用" : "启用"}`} /><span className={cn("text-[11px] font-medium", c.enabled ? "text-success" : "text-muted-foreground")}>{c.enabled ? "已启用" : "已停用"}</span></div><span className="font-mono tabular-nums text-muted-foreground">{relativeTime(c.updated_at)}</span><div className="fixed-column-shadow-right sticky right-0 z-20 flex items-center gap-1 bg-card"><Button size="icon" variant="ghost" className="size-8" title="测试发送" aria-label={`测试发送到${c.name}`} disabled={busy === c.id} onClick={() => handleTest(c)}><TestTube2 className="size-3.5" /></Button><Button size="icon" variant="ghost" className="size-8" title="编辑" aria-label={`编辑${c.name}`} onClick={() => { setEditing(c); setOpen(true) }}><Pencil className="size-3.5" /></Button><Button size="icon" variant="ghost" className="size-8 text-destructive hover:bg-destructive/10 hover:text-destructive" title="删除" disabled={busy === c.id} onClick={() => handleDelete(c)}><Trash2 className="size-3.5" /></Button></div></div> })}
              </div>}
            </div>
          </div>
        ) : loading ? <p className="px-6 py-4 text-xs text-muted-foreground">{"加载中…"}</p> : !data || data.length === 0 ? <p className="px-6 py-4 text-xs text-muted-foreground">{"暂未配置通知渠道"}</p> : <ul className="divide-y divide-border">{data.map((c) => { const Icon = notifyTypeIcon[c.type] ?? Send; const subCount = parseSubCount(c.subscriptions); return <li key={c.id} className="flex min-h-14 items-center justify-between gap-3 px-3 py-2.5"><div className="flex min-w-0 items-center gap-2.5"><Icon className={cn("size-4 shrink-0", c.enabled ? "text-brand" : "text-muted-foreground")} /><div className="min-w-0"><p className="truncate text-sm font-medium text-foreground">{c.name}</p><p className="text-[11px] text-muted-foreground">{notificationTypeLabel[c.type] ?? c.type}{" · "}{subCount === 0 ? "订阅全部" : `${subCount} 条订阅`}{!c.enabled ? " · 已禁用" : ""}</p></div></div><div className="flex shrink-0 items-center gap-0.5"><Button size="icon" variant="ghost" className="h-7 w-7" title="测试发送" disabled={busy === c.id} onClick={() => handleTest(c)}><TestTube2 className="size-3.5" /></Button><Button size="icon" variant="ghost" className="h-7 w-7" title="编辑" onClick={() => { setEditing(c); setOpen(true) }}><Pencil className="size-3.5" /></Button><Button size="icon" variant="ghost" className="h-7 w-7 text-destructive hover:bg-destructive/10 hover:text-destructive" title="删除" disabled={busy === c.id} onClick={() => handleDelete(c)}><Trash2 className="size-3.5" /></Button></div></li> })}</ul>}
      </CardContent>

      <NotificationFormDialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v)
          if (!v) setEditing(null)
        }}
        channel={editing}
      />

      {confirmDialog}
    </Card>
  )
}

export function NotificationLogs() {
  const logs = useNotificationLogs(100)
  const channels = useNotificationChannels()
  const refresh = useTriggerRefresh()
  const [statusFilter, setStatusFilter] = useState<"all" | "success" | "failed">("all")
  const [channelFilter, setChannelFilter] = useState("all")
  const [eventFilter, setEventFilter] = useState("all")
  const [query, setQuery] = useState("")
  const channelNames = useMemo(() => new Map((channels.data ?? []).map((channel) => [channel.id, channel])), [channels.data])
  const allLogs = logs.data ?? []
  const filteredLogs = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    return allLogs.filter((log) => {
      const channel = channelNames.get(log.channel_id)
      const event = log.event || "test"
      return (statusFilter === "all" || (statusFilter === "success" ? log.success : !log.success)) &&
        (channelFilter === "all" || String(log.channel_id) === channelFilter) &&
        (eventFilter === "all" || event === eventFilter) &&
        (!keyword || `${channel?.name ?? ""} ${log.subject} ${log.body} ${log.error_message ?? ""}`.toLowerCase().includes(keyword))
    })
  }, [allLogs, channelFilter, channelNames, eventFilter, query, statusFilter])
  const failedCount = filteredLogs.filter((log) => !log.success).length
  const resetFilters = () => { setStatusFilter("all"); setChannelFilter("all"); setEventFilter("all"); setQuery("") }

  return (
    <Card className="gap-0 overflow-hidden border border-border py-0 shadow-none">
      <CardHeader className="gap-3 px-4 py-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-sm font-semibold"><Send className="size-4 text-brand" />通知发送记录</CardTitle>
            <p className="mt-1 text-xs text-muted-foreground">最近 100 条通知的发送结果和完整内容。</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <span className="text-xs text-muted-foreground">{filteredLogs.length} / {allLogs.length}</span>
            <span className={cn("text-xs font-medium", failedCount ? "text-danger" : "text-success")}>{failedCount ? `${failedCount} 条失败` : "当前筛选全部成功"}</span>
          </div>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Input value={query} onChange={(event) => setQuery(event.target.value)} className="h-9 w-full text-xs sm:w-52" placeholder="搜索渠道、标题或内容" aria-label="搜索通知记录" />
          <Select value={statusFilter} onValueChange={(value) => setStatusFilter(value as typeof statusFilter)}><SelectTrigger className="h-9 w-[calc(var(--spacing)*26)] text-xs" aria-label="筛选发送状态"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部状态</SelectItem><SelectItem value="success">成功</SelectItem><SelectItem value="failed">失败</SelectItem></SelectContent></Select>
          <Select value={channelFilter} onValueChange={setChannelFilter}><SelectTrigger className="h-9 w-36 text-xs" aria-label="筛选通知渠道"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部渠道</SelectItem>{(channels.data ?? []).map((channel) => <SelectItem key={channel.id} value={String(channel.id)}>{channel.name}</SelectItem>)}</SelectContent></Select>
          <Select value={eventFilter} onValueChange={setEventFilter}><SelectTrigger className="h-9 w-32 text-xs" aria-label="筛选通知事件"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部事件</SelectItem><SelectItem value="balance_low">余额告警</SelectItem><SelectItem value="rate_changed">倍率变化</SelectItem><SelectItem value="login_failed">登录失败</SelectItem><SelectItem value="captcha_failed">验证码失败</SelectItem><SelectItem value="monitor_failed">采集失败</SelectItem><SelectItem value="test">测试通知</SelectItem></SelectContent></Select>
          <Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={resetFilters}><RotateCcw className="size-3.5" />重置</Button>
          <Button type="button" variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => void logs.refetch().then(() => refresh())} disabled={logs.refreshing}><RefreshCw className={cn("size-3.5", logs.refreshing && "animate-spin")} />刷新</Button>
        </div>
      </CardHeader>
      <CardContent className="border-t border-border p-0">
        <div className="relative isolate h-[min(58vh,600px)]">
          <div className="h-full overflow-auto">
            {logs.loading && !logs.data ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">正在读取通知记录...</div> : !filteredLogs.length ? <div className="flex h-full items-center justify-center text-sm text-muted-foreground">{allLogs.length ? "当前筛选没有通知记录" : "暂无通知发送记录"}</div> : <div className="isolate min-w-[1180px]"><div className="sticky top-0 z-30 grid grid-cols-[176px_160px_132px_220px_minmax(350px,1fr)_128px] items-center border-b border-border bg-muted text-[11px] font-medium text-foreground/90 [&>*]:px-4 [&>*]:py-2"><span>发送时间</span><span>通知渠道</span><span>事件</span><span>标题</span><span>通知内容</span><span>状态</span></div>{filteredLogs.map((log) => <NotificationLogRow key={log.id} log={log} channelName={channelNames.get(log.channel_id)?.name ?? `渠道 #${log.channel_id}`} />)}</div>}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function NotificationLogRow({ log, channelName }: { log: NotificationLog; channelName: string }) {
  const meta = eventMetaFor(log.event)
  const Icon = meta.icon
  return (
    <div className="grid grid-cols-[176px_160px_132px_220px_minmax(350px,1fr)_128px] items-center border-b border-border text-xs last:border-0 [&>*]:px-4 [&>*]:py-3">
      <span className="font-mono tabular-nums text-muted-foreground" title={absoluteTime(log.sent_at)}>{absoluteTime(log.sent_at)}</span>
      <span className="min-w-0 truncate font-medium text-foreground" title={channelName}>{channelName}</span>
      <div className="flex items-center"><PillTag label={eventLabel[log.event] || "测试通知"} icon={Icon} tone={meta.tag} /></div>
      <span className="min-w-0 break-words font-medium text-foreground">{log.subject}</span>
      <div className="flex min-w-0 items-center"><div className="min-w-0"><p className="whitespace-pre-wrap break-words leading-5 text-muted-foreground">{log.body || "-"}</p>{log.error_message ? <p className="mt-1 whitespace-pre-wrap break-words text-danger">错误：{log.error_message}</p> : null}</div></div>
      <div className="flex items-center"><PillTag label={log.success ? "成功" : "失败"} icon={log.success ? CheckCircle2 : CircleX} tone={log.success ? "brand" : "danger"} /></div>
    </div>
  )
}

function parseSubCount(raw?: string): number {
  if (!raw) return 0
  try {
    const arr = JSON.parse(raw)
    return Array.isArray(arr) ? arr.length : 0
  } catch {
    return 0
  }
}

export function BottomPanels() {
  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
      <AlertFeed />
      <CaptchaStatus />
      <NotificationStatus />
    </div>
  )
}
