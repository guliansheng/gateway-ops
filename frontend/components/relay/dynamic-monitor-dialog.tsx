"use client"

import { useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode } from "react"
import { Activity, AlertTriangle, ChevronDown, HelpCircle, Layers3, Power, Plus, RefreshCw, Search, SlidersHorizontal, Trash2, Zap } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Switch } from "@/components/ui/switch"
import type { RelayAccountView, RelayDynamicMonitorConfig, RelayDynamicMonitorErrorRuleGroup, RelayDynamicMonitorState, RelayGroupView } from "@/lib/api-types"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/** 与后端 dynamicMonitorMaxGroups 对齐。 */
const MAX_RULE_GROUPS = 20

/**
 * 弹窗里的一级模块：统一“彩色图标 + 标题 + 右侧操作”的标题栏与卡片外观，
 * 让启用开关、监测规则组、分组最少保留账号数等大模块一眼可区分。
 */
function ModuleSection({
  icon: Icon, title, description, action, tone = "brand", children,
}: {
  icon: ComponentType<{ className?: string }>
  title: string
  description?: ReactNode
  action?: ReactNode
  tone?: "brand" | "warning"
  children?: ReactNode
}) {
  const iconTone = tone === "warning" ? "bg-warning/10 text-warning" : "bg-brand/10 text-brand"
  return (
    <section className="overflow-hidden rounded-lg border border-border bg-card shadow-none">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border bg-muted/40 px-3 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className={cn("flex size-6 shrink-0 items-center justify-center rounded-md", iconTone)}>
            <Icon className="size-3.5" />
          </span>
          <div className="min-w-0">
            <h3 className="truncate text-xs font-semibold text-foreground">{title}</h3>
            {description ? <p className="mt-0.5 text-[11px] leading-4 text-muted-foreground">{description}</p> : null}
          </div>
        </div>
        {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
      </header>
      {children ? <div className="space-y-2.5 px-3 py-3">{children}</div> : null}
    </section>
  )
}

interface DynamicMonitorDialogProps {
  open: boolean
  busy: boolean
  config: RelayDynamicMonitorConfig
  state: RelayDynamicMonitorState
  accounts: RelayAccountView[]
  groups: RelayGroupView[]
  onOpenChange: (open: boolean) => void
  onSave: (config: RelayDynamicMonitorConfig) => void
}

/** 编辑中的规则组：数字字段用字符串承载，便于清空后重填。 */
interface RuleGroupDraft {
  key: string
  name: string
  threshold: string
  consecutive: string
  /** 是否启用“单次首字超时直接关闭”这条规则。 */
  singleTimeoutEnabled: boolean
  /** 单次首字超时阈值（秒）。 */
  singleTimeout: string
  selectedIDs: number[]
}

/** 把编辑中的规则组翻译成一句可核对的话，避免“N 秒 M 次”这类缩写歧义。 */
function describeRule(draft: RuleGroupDraft): string {
  const threshold = draft.threshold.trim() || "?"
  const consecutive = draft.consecutive.trim() || "?"
  const rules = [`连续 ${consecutive} 次首字延迟超过 ${threshold} 秒`]
  const single = draft.singleTimeout.trim()
  if (draft.singleTimeoutEnabled) rules.push(`单次首字延迟超过 ${single || "?"} 秒`)
  const prefix = rules.length > 1 ? "命中任意一条即关闭账号：" : "关闭账号条件："
  return `${prefix}${rules.join("；")}。`
}

/** 关闭原因的中文说明，与后端 dynamicMonitorReason* 常量对应。 */
const CLOSE_REASON_LABELS: Record<string, string> = {
  first_token: "连续超标",
  first_token_single: "单次超时",
  error_rule: "报错次数超阈值",
}

/** 报错组参考：常见状态码的原因与建议，展示在悬浮窗里方便查阅。 */
const ERROR_REFERENCE: { code: string; cause: string; advice: string }[] = [
  { code: "400", cause: "请求参数或请求体格式错误", advice: "不建议关闭账号" },
  { code: "401", cause: "上游凭据无效、Token 过期", advice: "可关闭：建议 10 分钟 3 次" },
  { code: "402", cause: "余额或额度不足", advice: "可临时关闭：1 次即可" },
  { code: "403", cause: "账号或模型权限不足、区域限制", advice: "可关闭：建议 10 分钟 3 次" },
  { code: "404", cause: "模型不存在或已下线", advice: "不建议关闭账号" },
  { code: "408", cause: "请求超时", advice: "改用首字延迟规则" },
  { code: "409", cause: "并发或状态冲突", advice: "降低并发，不关闭账号" },
  { code: "413", cause: "请求体或上下文过大", advice: "不建议关闭账号" },
  { code: "422", cause: "参数语义校验失败", advice: "不建议关闭账号" },
  { code: "429", cause: "频率或并发限流", advice: "可短暂关闭：建议 5 分钟 5 次" },
  { code: "499", cause: "客户端主动断开", advice: "不建议关闭账号" },
  { code: "500", cause: "上游内部错误", advice: "可关闭：建议 10 分钟 3 次" },
  { code: "502", cause: "上游网关错误、连接失败", advice: "可关闭：建议 10 分钟 3 次" },
  { code: "503", cause: "上游服务不可用", advice: "可关闭：建议 10 分钟 3 次" },
  { code: "504", cause: "上游网关超时", advice: "可关闭：建议 10 分钟 3 次" },
]

/** 报错组编辑态：状态码用逗号分隔的字符串承载，便于清空后重填。 */
interface ErrorGroupDraft {
  key: string
  name: string
  codes: string
  threshold: string
  lookback: string
  autoOpenEnabled: boolean
  autoOpen: string
}

let errorGroupSeq = 0

function makeErrorDraft(index: number, overrides: Partial<ErrorGroupDraft> = {}): ErrorGroupDraft {
  errorGroupSeq += 1
  return {
    key: `error-${errorGroupSeq}`,
    name: overrides.name ?? `报错组 ${index}`,
    codes: overrides.codes ?? "502,503",
    threshold: overrides.threshold ?? "3",
    lookback: overrides.lookback ?? "10",
    autoOpenEnabled: overrides.autoOpenEnabled ?? false,
    autoOpen: overrides.autoOpen ?? "30",
  }
}

function toErrorDrafts(config: RelayDynamicMonitorConfig): ErrorGroupDraft[] {
  const source = config.error_groups ?? []
  return source.map((group, index) => makeErrorDraft(index + 1, {
    name: group.name,
    codes: (group.status_codes ?? []).join(","),
    threshold: String(group.threshold ?? 0),
    lookback: String(group.lookback_minutes ?? 0),
    autoOpenEnabled: (group.auto_open_minutes ?? 0) > 0,
    autoOpen: (group.auto_open_minutes ?? 0) > 0 ? String(group.auto_open_minutes) : "30",
  }))
}

/** 解析逗号或空格分隔的状态码，出现非法值返回 null。 */
function parseStatusCodes(raw: string): number[] | null {
  const parts = raw.split(/[\s,，、]+/).map((item) => item.trim()).filter((item) => item !== "")
  if (parts.length === 0) return null
  const codes: number[] = []
  for (const part of parts) {
    if (!/^\d{3}$/.test(part)) return null
    const value = Number(part)
    if (value < 400 || value > 599) return null
    if (!codes.includes(value)) codes.push(value)
  }
  return codes.sort((a, b) => a - b)
}

let ruleGroupSeq = 0

function makeDraft(index: number, overrides: Partial<RuleGroupDraft> = {}): RuleGroupDraft {
  ruleGroupSeq += 1
  return {
    key: `rule-${ruleGroupSeq}`,
    name: overrides.name ?? `规则组 ${index}`,
    threshold: overrides.threshold ?? "10",
    consecutive: overrides.consecutive ?? "2",
    singleTimeoutEnabled: overrides.singleTimeoutEnabled ?? false,
    singleTimeout: overrides.singleTimeout ?? "60",
    selectedIDs: overrides.selectedIDs ?? [],
  }
}

function toDrafts(config: RelayDynamicMonitorConfig): RuleGroupDraft[] {
  const source = config.groups ?? []
  if (source.length === 0) return [makeDraft(1)]
  return source.map((group, index) => makeDraft(index + 1, {
    name: group.name,
    threshold: String(group.first_token_seconds ?? 0),
    consecutive: String(group.consecutive_count ?? 0),
    singleTimeoutEnabled: (group.single_timeout_seconds ?? 0) > 0,
    singleTimeout: (group.single_timeout_seconds ?? 0) > 0 ? String(group.single_timeout_seconds) : "60",
    selectedIDs: [...(group.account_external_ids ?? [])],
  }))
}

/** 把配置里按销售分组设置的下限读成编辑态：只保留明确设置过的分组。 */
function toGroupMinDrafts(config: RelayDynamicMonitorConfig): Record<string, { enabled: boolean; value: string }> {
  const source = config.group_min_accounts ?? {}
  const result: Record<string, { enabled: boolean; value: string }> = {}
  for (const [externalID, value] of Object.entries(source)) {
    result[externalID] = { enabled: true, value: String(value) }
  }
  return result
}

/** 把配置里“销售分组 → 关联规则组下标”读成编辑态（值为规则组下标字符串）。 */
function toGroupRuleDrafts(config: RelayDynamicMonitorConfig): Record<string, string> {
  const source = config.group_rule_groups ?? {}
  const result: Record<string, string> = {}
  for (const [externalID, index] of Object.entries(source)) {
    result[externalID] = String(index)
  }
  return result
}

/** 把配置里“不参与规则判断”的销售分组读成编辑态：值为 true 表示不参与。 */
function toGroupExcludedDrafts(config: RelayDynamicMonitorConfig): Record<string, boolean> {
  const source = config.group_rule_excluded ?? {}
  const result: Record<string, boolean> = {}
  for (const [externalID, excluded] of Object.entries(source)) {
    if (excluded) result[externalID] = true
  }
  return result
}

/** 单个规则组：自己的阈值、超标次数与账号勾选。 */
function RuleGroupCard({
  draft, index, total, accounts, groups, closedByExternalID, claimed, busy, canRemove, onChange, onToggleAccount, onRemove,
}: {
  draft: RuleGroupDraft
  index: number
  total: number
  accounts: RelayAccountView[]
  groups: RelayGroupView[]
  closedByExternalID: Map<number, { account_name: string }>
  claimed: Set<number>
  busy: boolean
  canRemove: boolean
  onChange: (patch: Partial<RuleGroupDraft>) => void
  onToggleAccount: (externalID: number, checked: boolean) => void
  onRemove: () => void
}) {
  const [query, setQuery] = useState("")
  const [groupFilterIDs, setGroupFilterIDs] = useState<number[]>([])
  const [onlySelected, setOnlySelected] = useState(false)
  const selectedAccountIDs = useMemo(() => new Set(draft.selectedIDs), [draft.selectedIDs])
  const selectedGroups = useMemo(() => groups.filter((group) => groupFilterIDs.includes(group.external_id)), [groupFilterIDs, groups])

  const visibleAccounts = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    return accounts.filter((account) => {
      if (onlySelected && !selectedAccountIDs.has(account.external_id)) return false
      if (groupFilterIDs.length > 0 && !account.current_groups.some((group) => groupFilterIDs.includes(group.external_id))) return false
      if (needle !== "" && !account.name.toLocaleLowerCase().includes(needle)) return false
      return true
    })
  }, [accounts, groupFilterIDs, query, onlySelected, selectedAccountIDs])

  function toggleAllVisible() {
    const visibleIDs = visibleAccounts.filter((account) => !claimed.has(account.external_id)).map((account) => account.external_id)
    if (visibleIDs.length === 0) return
    const allSelected = visibleIDs.every((id) => draft.selectedIDs.includes(id))
    onChange({
      selectedIDs: allSelected
        ? draft.selectedIDs.filter((id) => !visibleIDs.includes(id))
        : [...new Set([...draft.selectedIDs, ...visibleIDs])],
    })
  }

  const closedCount = useMemo(
    () => draft.selectedIDs.filter((externalID) => closedByExternalID.has(externalID)).length,
    [draft.selectedIDs, closedByExternalID],
  )

  const allVisibleSelected = visibleAccounts.some((account) => !claimed.has(account.external_id))
    && visibleAccounts.filter((account) => !claimed.has(account.external_id)).every((account) => draft.selectedIDs.includes(account.external_id))

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-card shadow-none">
      {/* 左侧色条 + 序号徽标，让每个规则组的边界一眼可见。 */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-y-2 border-l-4 border-l-brand border-y-transparent bg-muted/50 px-3 py-2">
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <span className="shrink-0 rounded bg-brand/10 px-1.5 py-0.5 text-[11px] font-semibold text-brand">规则组 {index + 1}</span>
          <Input value={draft.name} onChange={(event) => onChange({ name: event.target.value })} disabled={busy} placeholder="规则组名称" aria-label={`规则组 ${index + 1} 名称`} className="h-8 w-44 text-xs" />
        </div>
        <Button type="button" variant="ghost" size="sm" className="h-8 shrink-0 gap-1 text-xs text-danger hover:text-danger" disabled={busy || !canRemove} onClick={onRemove} aria-label={`删除规则组 ${index + 1}`}>
          <Trash2 className="size-3.5" />删除
        </Button>
      </div>

      <div className="space-y-2.5 px-3 py-3">
      <div className="grid gap-2 sm:grid-cols-2">
        <div className="rounded-md border border-border bg-muted/30 px-2.5 py-2">
          <div className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><span className="flex size-4 shrink-0 items-center justify-center rounded bg-foreground/10 text-[10px] font-bold">1</span>连续超时关闭</span>
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <span className="shrink-0 text-[11px] text-muted-foreground">首字延迟超过</span>
            <Input inputMode="decimal" value={draft.threshold} onChange={(event) => onChange({ threshold: event.target.value })} disabled={busy} className="h-8 w-20 text-xs tabular-nums" aria-label={`规则组 ${index + 1} 连续超时阈值（秒）`} />
            <span className="shrink-0 text-[11px] text-muted-foreground">秒，连续</span>
            <Input inputMode="numeric" value={draft.consecutive} onChange={(event) => onChange({ consecutive: event.target.value })} disabled={busy} className="h-8 w-16 text-xs tabular-nums" aria-label={`规则组 ${index + 1} 连续超标次数`} />
            <span className="shrink-0 text-[11px] text-muted-foreground">次后关闭</span>
          </div>
        </div>
        <div className="rounded-md border border-border bg-muted/30 px-2.5 py-2">
          <div className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><span className="flex size-4 shrink-0 items-center justify-center rounded bg-foreground/10 text-[10px] font-bold">2</span>单次超时直接关闭</span>
            <Switch checked={draft.singleTimeoutEnabled} onCheckedChange={(checked) => onChange({ singleTimeoutEnabled: checked })} disabled={busy} aria-label={`规则组 ${index + 1} 启用单次首字超时直接关闭`} />
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <span className="shrink-0 text-[11px] text-muted-foreground">首字延迟超过</span>
            <Input inputMode="decimal" value={draft.singleTimeout} onChange={(event) => onChange({ singleTimeout: event.target.value })} disabled={busy || !draft.singleTimeoutEnabled} className="h-8 w-20 text-xs tabular-nums" aria-label={`规则组 ${index + 1} 单次首字超时阈值（秒）`} />
            <span className="shrink-0 text-[11px] text-muted-foreground">秒立即关闭</span>
          </div>
        </div>
      </div>

      <p className="mt-1.5 text-[11px] text-muted-foreground">{describeRule(draft)}</p>

      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="筛选账号名称" aria-label={`规则组 ${index + 1} 按账号名称筛选`} className="h-8 w-40 pl-8 text-xs" />
        </div>
        {/* modal 让浮层自建滚动锁（与 Select 一致）；否则外层 Dialog 的 RemoveScroll 会吃掉列表里的滚轮事件。 */}
        <Popover modal>
          <PopoverTrigger asChild>
            <Button type="button" variant="outline" size="sm" className="h-8 w-44 justify-between gap-2 px-2.5 text-xs font-normal" aria-label={`规则组 ${index + 1} 按销售分组筛选`}>
              <span className="truncate">{selectedGroups.length === 0 ? "全部分组" : selectedGroups.length === 1 ? selectedGroups[0].name : `已选 ${selectedGroups.length} 个分组`}</span>
              <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-64 p-2" align="start">
            <div className="flex items-center justify-between gap-2 px-1 pb-1.5">
              <span className="text-xs font-medium text-foreground">按销售分组筛选</span>
              {selectedGroups.length > 0 ? <button type="button" className="text-[11px] text-muted-foreground hover:text-foreground" onClick={() => setGroupFilterIDs([])}>清除</button> : null}
            </div>
            <p className="px-1 pb-1.5 text-[11px] leading-4 text-muted-foreground">可多选，账号命中任一分组即可显示</p>
            <div className="max-h-52 space-y-1 overflow-y-auto pr-1">
              {groups.length === 0 ? <p className="px-1 py-4 text-center text-xs text-muted-foreground">暂无销售分组</p> : groups.map((group) => {
                const checked = groupFilterIDs.includes(group.external_id)
                return (
                  <label key={group.external_id} className={cn("flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-xs transition-colors hover:bg-muted/50", checked && "bg-brand/5 text-foreground")}>
                    <Checkbox checked={checked} onCheckedChange={(value) => setGroupFilterIDs((current) => {
                      if (value === true) return current.includes(group.external_id) ? current : [...current, group.external_id]
                      return current.filter((id) => id !== group.external_id)
                    })} />
                    <span className="min-w-0 truncate" title={group.name}>{group.name}</span>
                  </label>
                )
              })}
            </div>
          </PopoverContent>
        </Popover>
        <div className="flex h-8 items-center gap-1.5">
          <Switch id={`${draft.key}-only-selected`} checked={onlySelected} onCheckedChange={setOnlySelected} aria-label={`规则组 ${index + 1} 仅显示本组已选账号`} />
          <Label htmlFor={`${draft.key}-only-selected`} className="cursor-pointer whitespace-nowrap text-xs">仅显示本组已选</Label>
        </div>
        <Button type="button" variant="outline" size="sm" className="h-8 gap-1 text-xs" disabled={busy || visibleAccounts.length === 0} onClick={toggleAllVisible}>
          {allVisibleSelected ? "取消全选" : "全选当前列表"}
        </Button>
        <span className="text-[11px] text-muted-foreground">已选 {draft.selectedIDs.length} 个</span>
      </div>

      <div className="mt-2 max-h-52 space-y-1.5 overflow-y-auto rounded-md border border-border bg-background p-2 pr-1">
        {visibleAccounts.length === 0 ? <p className="py-6 text-center text-xs text-muted-foreground">{onlySelected ? "当前筛选没有本组已选账号" : "当前筛选没有账号"}</p> : visibleAccounts.map((account) => {
          const checked = draft.selectedIDs.includes(account.external_id)
          // 已被其它规则组占用的账号不可再勾，保证一个账号只有一套判定规则。
          const taken = !checked && claimed.has(account.external_id)
          const closed = closedByExternalID.has(account.external_id)
          return (
            <label key={account.external_id} className={cn("flex items-center justify-between gap-3 border px-3 py-2 transition-colors", taken ? "cursor-not-allowed border-transparent opacity-50" : "cursor-pointer border-transparent hover:bg-muted/40", checked && !taken ? "border-brand/50 bg-brand/5" : "")}>
              <span className="flex min-w-0 items-center gap-2.5">
                <Checkbox checked={checked} disabled={busy || taken} onCheckedChange={(value) => onToggleAccount(account.external_id, value === true)} />
                <span className="min-w-0">
                  <span className="flex flex-wrap items-center gap-1.5">
                    <span className="truncate text-xs font-medium">{account.name}</span>
                    {taken ? <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">已在其它规则组</span> : null}
                    {closed ? <span className="rounded bg-warning/10 px-1.5 py-0.5 text-[10px] font-medium text-warning">已被规则关闭</span> : null}
                    {!account.schedulable && !closed ? <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">调度已关闭</span> : null}
                  </span>
                  <span className="mt-0.5 block text-[11px] text-muted-foreground">{account.platform || "-"} · {account.current_groups.map((group) => group.name).join("、") || "未分组"}</span>
                </span>
              </span>
            </label>
          )
        })}
      </div>
      </div>
      {total > 1 ? <p className="px-3 pb-3 text-[11px] text-muted-foreground">共 {total} 个规则组；同一账号只能归属一个规则组。</p> : null}
      {closedCount > 0 ? <p className="px-3 pb-3 text-[11px] text-muted-foreground">本组当前有 {closedCount} 个账号被规则关闭。</p> : null}
    </section>
  )
}

/** 多规则组动态监测：每组独立的“连续超标”与“单次超时”规则，账号跨组互斥；补开规则全局一套。 */
export function DynamicMonitorDialog({ open, busy, config, state, accounts, groups, onOpenChange, onSave }: DynamicMonitorDialogProps) {
  const [enabled, setEnabled] = useState(config.enabled)
  const [ruleGroups, setRuleGroups] = useState<RuleGroupDraft[]>(() => toDrafts(config))
  const [minAccounts, setMinAccounts] = useState(String(config.min_accounts_per_group))
  // 按销售分组单独设置的最少保留账号数：key 为分组 external_id 字符串。
  // 只保留用户明确启用自定义的分组，其余分组保存时不下发，运行时沿用公共值。
  const [groupMinAccounts, setGroupMinAccounts] = useState<Record<string, { enabled: boolean; value: string }>>({})
  // 按销售分组关联的规则组：key 为分组 external_id，值为规则组下标的字符串（空串表示不关联）。
  const [groupRuleGroups, setGroupRuleGroups] = useState<Record<string, string>>({})
  // 不参与规则判断的销售分组：key 为分组 external_id，出现即表示该分组不参与。
  const [groupRuleExcluded, setGroupRuleExcluded] = useState<Record<string, boolean>>({})
  const [allowAutoOpen, setAllowAutoOpen] = useState(config.allow_auto_open)
  const [autoOpenMinutes, setAutoOpenMinutes] = useState(String(config.auto_open_minutes))
  const [errorGroups, setErrorGroups] = useState<ErrorGroupDraft[]>(() => toErrorDrafts(config))
  const [error, setError] = useState("")

  const closedAccounts = state.closed_accounts ?? []

  // 弹窗打开期间页面可能被 30 秒全局轮询或其它刷新带入新的 config/state，
  // 若直接依赖 config 重置表单，会把用户正在编辑的内容覆盖掉。
  // 这里只在“从关闭到打开”这一刻读取最新值，打开期间不响应外部数据变化。
  const latestConfig = useRef(config)
  latestConfig.current = config
  const wasOpen = useRef(false)

  useEffect(() => {
    if (open && !wasOpen.current) {
      const next = latestConfig.current
      setEnabled(next.enabled)
      setRuleGroups(toDrafts(next))
      setMinAccounts(String(next.min_accounts_per_group))
      setGroupMinAccounts(toGroupMinDrafts(next))
      setGroupRuleGroups(toGroupRuleDrafts(next))
      setGroupRuleExcluded(toGroupExcludedDrafts(next))
      setAllowAutoOpen(next.allow_auto_open)
      setAutoOpenMinutes(String(next.auto_open_minutes))
      setErrorGroups(toErrorDrafts(next))
      setError("")
    }
    wasOpen.current = open
  }, [open])

  const closedByExternalID = useMemo(
    () => new Map(closedAccounts.map((closure) => [closure.account_external_id, closure])),
    [closedAccounts],
  )

  // 每个规则组里被其它组占用的账号集合，用于置灰不可选。
  const claimedByGroup = useMemo(() => {
    const result = new Map<string, Set<number>>()
    for (const draft of ruleGroups) {
      const others = new Set<number>()
      for (const other of ruleGroups) {
        if (other.key === draft.key) continue
        for (const id of other.selectedIDs) others.add(id)
      }
      result.set(draft.key, others)
    }
    return result
  }, [ruleGroups])

  const totalSelected = useMemo(
    () => ruleGroups.reduce((sum, draft) => sum + draft.selectedIDs.length, 0),
    [ruleGroups],
  )

  function patchDraft(key: string, patch: Partial<RuleGroupDraft>) {
    setRuleGroups((current) => current.map((draft) => (draft.key === key ? { ...draft, ...patch } : draft)))
  }

  function toggleAccount(key: string, externalID: number, checked: boolean) {
    setRuleGroups((current) => current.map((draft) => {
      if (draft.key !== key) return draft
      return {
        ...draft,
        selectedIDs: checked
          ? [...new Set([...draft.selectedIDs, externalID])]
          : draft.selectedIDs.filter((id) => id !== externalID),
      }
    }))
  }

  function addGroup() {
    setRuleGroups((current) => (current.length >= MAX_RULE_GROUPS ? current : [...current, makeDraft(current.length + 1)]))
  }

  function removeGroup(key: string) {
    if (ruleGroups.length <= 1) return
    const removedIndex = ruleGroups.findIndex((draft) => draft.key === key)
    if (removedIndex < 0) return
    setRuleGroups(ruleGroups.filter((draft) => draft.key !== key))
    // 规则组下标会整体前移，同步修正分组关联；指向被删规则组的关联直接清空。
    setGroupRuleGroups((assignments) => {
      const remapped: Record<string, string> = {}
      for (const [externalID, raw] of Object.entries(assignments)) {
        const index = Number(raw)
        if (!Number.isInteger(index)) continue
        if (index === removedIndex) continue
        remapped[externalID] = String(index > removedIndex ? index - 1 : index)
      }
      return remapped
    })
  }

  function patchErrorDraft(key: string, patch: Partial<ErrorGroupDraft>) {
    setErrorGroups((current) => current.map((draft) => (draft.key === key ? { ...draft, ...patch } : draft)))
  }

  function addErrorGroup() {
    setErrorGroups((current) => (current.length >= MAX_RULE_GROUPS ? current : [...current, makeErrorDraft(current.length + 1)]))
  }

  function removeErrorGroup(key: string) {
    setErrorGroups((current) => current.filter((draft) => draft.key !== key))
  }

  function submit() {
    const minAccountsValue = Number(minAccounts)
    const autoOpenValue = Number(autoOpenMinutes)
    if (!Number.isInteger(minAccountsValue) || minAccountsValue < 0 || minAccountsValue > 1000) return setError("分组最少保留账号数必须是 0 到 1000 的整数")
    if (!Number.isInteger(autoOpenValue) || autoOpenValue < 0 || autoOpenValue > 10080) return setError("自动开启时间必须是 0 到 10080 分钟的整数")

    // 只保留仍存在的销售分组，且用户明确启用自定义的分组。
    const overrides: Record<string, number> = {}
    for (const group of groups) {
      const key = String(group.external_id)
      const draft = groupMinAccounts[key]
      if (!draft?.enabled) continue
      const value = Number(draft.value)
      if (!Number.isInteger(value) || value < 0 || value > 1000) return setError(`分组“${group.name}”的最少保留账号数必须是 0 到 1000 的整数`)
      overrides[key] = value
    }
    // 分组关联规则组：只保留仍存在的分组，且下标必须指向本次提交的规则组。
    const ruleAssignments: Record<string, number> = {}
    for (const group of groups) {
      const key = String(group.external_id)
      const raw = groupRuleGroups[key]
      if (raw == null || raw === "") continue
      const index = Number(raw)
      if (!Number.isInteger(index) || index < 0 || index >= ruleGroups.length) {
        return setError(`分组“${group.name}”关联的规则组无效，请重新选择`)
      }
      ruleAssignments[key] = index
    }
    // 不参与规则判断的分组：只保留仍存在的分组。
    const excludedGroups: Record<string, boolean> = {}
    for (const group of groups) {
      const key = String(group.external_id)
      if (groupRuleExcluded[key]) excludedGroups[key] = true
    }

    for (const [index, draft] of ruleGroups.entries()) {
      const label = draft.name.trim() || `第 ${index + 1} 组`
      const thresholdValue = Number(draft.threshold)
      const consecutiveValue = Number(draft.consecutive)
      const singleTimeoutValue = draft.singleTimeoutEnabled ? Number(draft.singleTimeout) : 0
      if (!Number.isFinite(thresholdValue) || thresholdValue < 0.1 || thresholdValue > 600) return setError(`${label}：连续超时阈值必须在 0.1 到 600 秒之间`)
      if (!Number.isInteger(consecutiveValue) || consecutiveValue < 1 || consecutiveValue > 100) return setError(`${label}：连续超标次数必须是 1 到 100 的整数`)
      if (draft.singleTimeoutEnabled && (!Number.isFinite(singleTimeoutValue) || singleTimeoutValue < 0.1 || singleTimeoutValue > 600)) return setError(`${label}：单次首字超时阈值必须在 0.1 到 600 秒之间`)
      if (draft.selectedIDs.length === 0) return setError(`${label}：请至少勾选一个参与账号`)
    }

    const payloadErrorGroups: RelayDynamicMonitorErrorRuleGroup[] = []
    for (const [index, draft] of errorGroups.entries()) {
      const label = draft.name.trim() || `报错组 ${index + 1}`
      const codes = parseStatusCodes(draft.codes)
      if (!codes) return setError(`${label}：请填写 400 到 599 之间的具体状态码，多个用逗号分隔`)
      const thresholdValue = Number(draft.threshold)
      const lookbackValue = Number(draft.lookback)
      const groupAutoOpenValue = draft.autoOpenEnabled ? Number(draft.autoOpen) : 0
      if (!Number.isInteger(thresholdValue) || thresholdValue < 1 || thresholdValue > 1000) return setError(`${label}：错误次数阈值必须是 1 到 1000 的整数`)
      if (!Number.isInteger(lookbackValue) || lookbackValue < 1 || lookbackValue > 10080) return setError(`${label}：统计时间窗口必须是 1 到 10080 分钟的整数`)
      if (draft.autoOpenEnabled && (!Number.isInteger(groupAutoOpenValue) || groupAutoOpenValue < 1 || groupAutoOpenValue > 10080)) return setError(`${label}：关闭后自动开启时间必须是 1 到 10080 分钟的整数`)
      payloadErrorGroups.push({
        name: draft.name.trim(),
        status_codes: codes,
        threshold: thresholdValue,
        lookback_minutes: lookbackValue,
        allow_auto_open: draft.autoOpenEnabled,
        auto_open_minutes: groupAutoOpenValue,
      })
    }

    const hasAnyFloor = minAccountsValue > 0 || Object.values(overrides).some((value) => value > 0)
    if (enabled && ruleGroups.length === 0 && payloadErrorGroups.length === 0) return setError("启用监测时请至少配置一个规则组或报错规则组")
    if (enabled && allowAutoOpen && autoOpenValue <= 0 && !hasAnyFloor) return setError("允许动态开启时，请设置自动开启时间或分组最少保留账号数")

    setError("")
    onSave({
      enabled,
      groups: ruleGroups.map((draft) => ({
        name: draft.name.trim(),
        first_token_seconds: Number(draft.threshold),
        consecutive_count: Number(draft.consecutive),
        single_timeout_seconds: draft.singleTimeoutEnabled ? Number(draft.singleTimeout) : 0,
        account_external_ids: draft.selectedIDs,
      })),
      error_groups: payloadErrorGroups.length > 0 ? payloadErrorGroups : undefined,
      min_accounts_per_group: minAccountsValue,
      group_min_accounts: Object.keys(overrides).length > 0 ? overrides : undefined,
      group_rule_groups: Object.keys(ruleAssignments).length > 0 ? ruleAssignments : undefined,
      group_rule_excluded: Object.keys(excludedGroups).length > 0 ? excludedGroups : undefined,
      allow_auto_open: allowAutoOpen,
      auto_open_minutes: autoOpenValue,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onOpenChange(false) }}>
      <DialogContent className="flex w-[calc(100vw-2rem)] max-h-[calc(100dvh-2rem)] sm:max-w-[868px] flex-col overflow-hidden p-0">
        <DialogHeader className="shrink-0 border-b border-border px-4 py-4 sm:px-6">
          <DialogTitle>账号动态监测</DialogTitle>
          <DialogDescription>
            规则只保存在当前中转站的这个功能里，不会写入分组配置，也不影响自动调组策略：监测规则组按首字延迟关闭勾选的账号，报错规则组按具体 HTTP 状态码统计所有账号；两者都支持各自的超时开启，分组最少保留账号数始终生效。
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:px-6">
          <ModuleSection
            icon={Power}
            title="启用账号动态监测"
            description="关闭判断在每次快照同步时执行，超时开启由后台每分钟任务处理。"
            action={<Switch checked={enabled} onCheckedChange={setEnabled} disabled={busy} aria-label="启用账号动态监测" />}
          />

          <ModuleSection
            icon={Layers3}
            title="监测规则组"
            description="每组两条互相独立的关闭规则，命中任意一条就关闭该组勾选的账号：规则 1 看连续调用，规则 2 只看单次调用。"
            action={<>
              <span className="hidden text-[11px] text-muted-foreground sm:inline">共 {ruleGroups.length} 组 · 已选 {totalSelected} / {accounts.length} 个账号</span>
              <Button type="button" variant="outline" size="sm" className="h-8 gap-1 text-xs" disabled={busy || ruleGroups.length >= MAX_RULE_GROUPS} onClick={addGroup}>
                <Plus className="size-3.5" />添加规则组
              </Button>
            </>}
          >
              {ruleGroups.map((draft, index) => (
                <RuleGroupCard
                  key={draft.key}
                  draft={draft}
                  index={index}
                  total={ruleGroups.length}
                  accounts={accounts}
                  groups={groups}
                  closedByExternalID={closedByExternalID}
                  claimed={claimedByGroup.get(draft.key) ?? new Set()}
                  busy={busy}
                  canRemove={ruleGroups.length > 1}
                  onChange={(patch) => patchDraft(draft.key, patch)}
                  onToggleAccount={(externalID, checked) => toggleAccount(draft.key, externalID, checked)}
                  onRemove={() => removeGroup(draft.key)}
                />
              ))}
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-background px-3 py-2.5">
              <div className="flex min-w-0 items-center gap-2">
                <Zap className="size-4 shrink-0 text-brand" />
                <div className="min-w-0">
                  <p className="text-xs font-semibold text-foreground">允许动态开启</p>
                  <p className="text-[11px] leading-4 text-muted-foreground">作用于上面所有监测规则组关闭的账号；报错规则组在各自组内单独配置。</p>
                </div>
              </div>
              <div className="flex shrink-0 flex-wrap items-center gap-2">
                <Switch checked={allowAutoOpen} onCheckedChange={setAllowAutoOpen} disabled={busy} aria-label="允许动态开启" />
                <span className="text-[11px] text-muted-foreground">关闭</span>
                <Input inputMode="numeric" value={autoOpenMinutes} onChange={(event) => setAutoOpenMinutes(event.target.value)} disabled={busy || !allowAutoOpen} className="h-8 w-20 text-xs tabular-nums" aria-label="关闭多久后自动开启（分钟）" />
                <span className="text-[11px] text-muted-foreground">分钟后开启</span>
              </div>
            </div>
          </ModuleSection>

          <ModuleSection
            icon={AlertTriangle}
            title="报错规则组"
            description="不选择账号，按具体 HTTP 状态码统计所有账号：命中任意一组即关闭账号，关闭后按该组自己的时间自动开启。"
            action={<>
              {/* modal 让浮层自建滚动锁（与分组筛选、Select 一致）；否则外层 Dialog 的 RemoveScroll 会吃掉列表里的滚轮事件。 */}
              <Popover modal>
                <PopoverTrigger asChild>
                  <Button type="button" variant="outline" size="sm" className="h-8 gap-1 text-xs">
                    <HelpCircle className="size-3.5" />常见状态码参考
                  </Button>
                </PopoverTrigger>
                <PopoverContent className="w-[min(36rem,92vw)] p-0" align="end">
                  <div className="border-b border-border px-3 py-2">
                    <p className="text-xs font-semibold text-foreground">常见状态码、原因与建议</p>
                    <p className="mt-0.5 text-[11px] leading-4 text-muted-foreground">4xx 多为请求或模型问题，只有上游凭据与权限类错误适合作为关闭标准。</p>
                  </div>
                  <div className="max-h-72 overflow-y-auto overscroll-contain p-2">
                    {ERROR_REFERENCE.map((item) => (
                      <div key={item.code} className="flex items-start gap-2 rounded px-2 py-1.5 text-[11px] leading-4 hover:bg-muted/50">
                        <span className="w-10 shrink-0 font-mono font-semibold tabular-nums text-foreground">{item.code}</span>
                        <span className="min-w-0 flex-1 text-muted-foreground">{item.cause}</span>
                        <span className="w-40 shrink-0 text-right text-muted-foreground">{item.advice}</span>
                      </div>
                    ))}
                  </div>
                </PopoverContent>
              </Popover>
              <Button type="button" variant="outline" size="sm" className="h-8 gap-1 text-xs" disabled={busy || errorGroups.length >= MAX_RULE_GROUPS} onClick={addErrorGroup}>
                <Plus className="size-3.5" />添加报错组
              </Button>
            </>}
          >
            {errorGroups.length === 0 ? (
              <p className="rounded-md border border-dashed border-border px-3 py-4 text-center text-[11px] text-muted-foreground">暂无报错规则组；添加后对所有账号生效</p>
            ) : errorGroups.map((draft, index) => (
              <div key={draft.key} className="space-y-2 rounded-md border border-border bg-muted/20 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 flex-1 items-center gap-2">
                    <span className="shrink-0 rounded bg-danger/10 px-1.5 py-0.5 text-[11px] font-semibold text-danger">报错组 {index + 1}</span>
                    <Input value={draft.name} onChange={(event) => patchErrorDraft(draft.key, { name: event.target.value })} disabled={busy} placeholder="报错组名称" aria-label={`报错组 ${index + 1} 名称`} className="h-8 w-44 text-xs" />
                  </div>
                  <Button type="button" variant="ghost" size="sm" className="h-8 shrink-0 gap-1 text-xs text-danger hover:text-danger" disabled={busy} onClick={() => removeErrorGroup(draft.key)} aria-label={`删除报错组 ${index + 1}`}>
                    <Trash2 className="size-3.5" />删除
                  </Button>
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  <div className="rounded-md border border-border bg-background px-2.5 py-2">
                    <span className="text-[11px] text-muted-foreground">状态码</span>
                    <Input value={draft.codes} onChange={(event) => patchErrorDraft(draft.key, { codes: event.target.value })} disabled={busy} placeholder="502,503" aria-label={`报错组 ${index + 1} 状态码`} className="mt-1 h-8 text-xs tabular-nums" />
                  </div>
                  <div className="rounded-md border border-border bg-background px-2.5 py-2">
                    <span className="text-[11px] text-muted-foreground">窗口内达到</span>
                    <div className="mt-1 flex items-center gap-1.5">
                      <Input inputMode="numeric" value={draft.threshold} onChange={(event) => patchErrorDraft(draft.key, { threshold: event.target.value })} disabled={busy} aria-label={`报错组 ${index + 1} 错误次数阈值`} className="h-8 w-16 text-xs tabular-nums" />
                      <span className="text-[11px] text-muted-foreground">次关闭</span>
                    </div>
                  </div>
                  <div className="rounded-md border border-border bg-background px-2.5 py-2">
                    <span className="text-[11px] text-muted-foreground">统计窗口</span>
                    <div className="mt-1 flex items-center gap-1.5">
                      <Input inputMode="numeric" value={draft.lookback} onChange={(event) => patchErrorDraft(draft.key, { lookback: event.target.value })} disabled={busy} aria-label={`报错组 ${index + 1} 统计窗口（分钟）`} className="h-8 w-20 text-xs tabular-nums" />
                      <span className="text-[11px] text-muted-foreground">分钟</span>
                    </div>
                  </div>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-background px-2.5 py-2">
                  <div className="flex items-center gap-1.5">
                    <Switch checked={draft.autoOpenEnabled} onCheckedChange={(checked) => patchErrorDraft(draft.key, { autoOpenEnabled: checked })} disabled={busy} aria-label={`报错组 ${index + 1} 启用动态开启`} />
                    <span className="text-xs">关闭后动态开启</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="text-[11px] text-muted-foreground">关闭</span>
                    <Input inputMode="numeric" value={draft.autoOpen} onChange={(event) => patchErrorDraft(draft.key, { autoOpen: event.target.value })} disabled={busy || !draft.autoOpenEnabled} aria-label={`报错组 ${index + 1} 自动开启时间（分钟）`} className="h-8 w-20 text-xs tabular-nums" />
                    <span className="text-[11px] text-muted-foreground">分钟后开启</span>
                  </div>
                </div>
              </div>
            ))}
          </ModuleSection>
          <ModuleSection
            icon={SlidersHorizontal}
            title="分组规则"
            description="关联规则组、最少保留账号数与是否参与规则判断：关联后分组新增账号自动加入该规则组；参与判断且可用账号低于下限时优先补开，设为不参与则该分组不占下限、也不会被补开。"
            action={<>
              <Label htmlFor="dm-min-accounts" className="text-[11px] text-muted-foreground">公共值</Label>
              <Input id="dm-min-accounts" inputMode="numeric" value={minAccounts} onChange={(event) => setMinAccounts(event.target.value)} disabled={busy} className="h-8 w-20 text-xs tabular-nums" />
              <span className="text-[11px] text-muted-foreground">个</span>
            </>}
          >
            <div className="space-y-2 rounded-md border border-border bg-muted/20 p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-xs font-semibold text-foreground">按分组单独设置</p>
                <span className="text-[11px] text-muted-foreground">每个分组可关联一个规则组（新增账号自动加入）、单独设置最少保留账号数，并选择是否参与规则判断</span>
              </div>
              {groups.length === 0 ? (
                <p className="rounded-md border border-dashed border-border px-3 py-4 text-center text-[11px] text-muted-foreground">当前中转站暂无销售分组</p>
              ) : (
                <div className="max-h-56 space-y-1.5 overflow-y-auto pr-1">
                  {groups.map((group) => {
                    const key = String(group.external_id)
                    const draft = groupMinAccounts[key]
                    const enabled = draft?.enabled ?? false
                    // 不参与规则判断的分组：不计入“最少保留账号数”，也不会被补开。
                    const participates = !groupRuleExcluded[key]
                    const normalAccountCount = accounts.filter((account) => account.current_groups.some((currentGroup) => currentGroup.external_id === group.external_id) && account.schedulable && account.status?.trim().toLowerCase() === "active").length
                    return (
                      <div key={group.external_id} className="flex flex-wrap items-center justify-between gap-2 border border-transparent px-2 py-1.5">
                        <span className="flex min-w-0 items-center gap-1.5">
                          <span className="truncate text-xs font-medium" title={group.name}>{group.name}</span>
                          <span className="font-mono text-[10px] font-normal text-muted-foreground">#{group.external_id}</span>
                          <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] font-medium tabular-nums text-muted-foreground" title="该分组当前关联的账号总数">账号 {group.account_count}</span>
                          <span className="shrink-0 rounded bg-success/10 px-1.5 py-0.5 font-mono text-[10px] font-medium tabular-nums text-success" title="账号已启用且调度已开启">正常 {normalAccountCount}</span>
                        </span>
                        <span className="flex shrink-0 flex-wrap items-center gap-2">
                          <span className="flex items-center gap-1.5">
                            <Label htmlFor={`${key}-participates`} className={`cursor-pointer whitespace-nowrap text-[11px] ${participates ? "text-muted-foreground" : "text-warning"}`}>
                              {participates ? "参与判断" : "不参与判断"}
                            </Label>
                            <Switch
                              id={`${key}-participates`}
                              checked={participates}
                              disabled={busy}
                              aria-label={`分组 ${group.name} 参与规则判断`}
                              onCheckedChange={(checked) => setGroupRuleExcluded((current) => {
                                const next = { ...current }
                                if (checked) delete next[key]
                                else next[key] = true
                                return next
                              })}
                            />
                          </span>
                          <span className="flex items-center gap-1.5">
                            <span className="text-[11px] text-muted-foreground">关联规则组</span>
                            <Select
                              value={groupRuleGroups[key] ?? "__none"}
                              onValueChange={(value) => setGroupRuleGroups((current) => {
                                const next = { ...current }
                                if (value === "__none") delete next[key]
                                else next[key] = value
                                return next
                              })}
                              disabled={busy}
                            >
                              <SelectTrigger className="h-8 w-32 text-xs" aria-label={`分组 ${group.name} 关联规则组`}><SelectValue /></SelectTrigger>
                              <SelectContent>
                                <SelectItem value="__none">不关联</SelectItem>
                                {ruleGroups.map((draft, index) => (
                                  <SelectItem key={draft.key} value={String(index)}>
                                    规则组 {index + 1}{draft.name.trim() ? ` · ${draft.name.trim()}` : ""}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </span>
                          <span className="flex items-center gap-1.5">
                            <span className="text-[11px] text-muted-foreground">最少保留</span>
                            <Switch
                              checked={enabled}
                              disabled={busy || !participates}
                              aria-label={`分组 ${group.name} 自定义最少保留账号数`}
                              onCheckedChange={(checked) => {
                                setGroupMinAccounts((current) => ({
                                  ...current,
                                  [key]: { enabled: checked, value: current[key]?.value ?? String(minAccounts || "0") },
                                }))
                              }}
                            />
                            {enabled ? (
                              <>
                                <Input
                                  inputMode="numeric"
                                  value={draft?.value ?? ""}
                                  disabled={busy || !participates}
                                  aria-label={`分组 ${group.name} 最少保留账号数`}
                                  onChange={(event) => setGroupMinAccounts((current) => ({ ...current, [key]: { enabled: true, value: event.target.value } }))}
                                  className="h-8 w-16 text-xs tabular-nums"
                                />
                                <span className="text-[11px] text-muted-foreground">个</span>
                              </>
                            ) : (
                              <span className="w-20 text-right text-[11px] text-muted-foreground">{participates ? `公共值 ${minAccounts.trim() || "0"}` : "不参与判断"}</span>
                            )}
                          </span>
                        </span>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </ModuleSection>


          <ModuleSection
            icon={Activity}
            tone="warning"
            title="当前被规则关闭的账号"
            description="这些账号由动态监测自动关闭，将按上面的规则自动开启。"
            action={<span className="rounded bg-warning/10 px-2 py-0.5 text-[11px] font-medium tabular-nums text-warning">{closedAccounts.length} 个</span>}
          >
            {closedAccounts.length === 0 ? (
              <p className="rounded-md border border-dashed border-border px-3 py-4 text-center text-[11px] text-muted-foreground">暂无被规则关闭的账号</p>
            ) : (
              <div className="max-h-40 space-y-1.5 overflow-y-auto rounded-md border border-border p-2 pr-1">
                {closedAccounts.map((closure) => (
                  <div key={closure.account_external_id} className="flex flex-wrap items-start justify-between gap-2 border border-transparent px-2 py-1.5">
                    <span className="flex min-w-0 flex-col gap-1">
                      <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                        <span className="truncate text-xs font-medium">{closure.account_name}</span>
                        <span className="font-mono text-[11px] font-normal text-muted-foreground">#{closure.account_external_id}</span>
                        <span className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium", closure.rule_category === "error" ? "bg-danger/10 text-danger" : "bg-warning/10 text-warning")}>
                          {closure.rule_category === "error" ? "报错规则组" : "监测规则组"}
                        </span>
                        <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">{CLOSE_REASON_LABELS[closure.reason] ?? "规则命中"}</span>
                      </span>
                      <span className="text-[11px] leading-4 text-muted-foreground">
                        命中规则：{closure.rule_group_name?.trim() || "未记录"}
                        {closure.error_status_codes && closure.error_status_codes.length > 0
                          ? ` · 状态码 ${closure.error_status_codes.join("、")} · ${closure.error_window_minutes ?? 0} 分钟内 ${closure.error_count ?? 0} 次`
                          : ""}
                      </span>
                    </span>
                    <span className="shrink-0 text-right text-[11px] text-muted-foreground">
                      <span className="block">关闭于 {relativeTime(closure.closed_at)}</span>
                      <span className="block">{closure.auto_open_at ? "到点自动开启" : "仅分组保底开启"}</span>
                    </span>
                  </div>
                ))}
              </div>
            )}
          </ModuleSection>
        </div>

        <DialogFooter className="shrink-0 items-center justify-between gap-3 border-t border-border px-4 py-3 sm:px-6">
          <p className="min-w-0 text-xs text-danger" role="alert">{error}</p>
          <div className="flex shrink-0 gap-2">
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>关闭</Button>
            <Button type="button" className="gap-1.5" disabled={busy} onClick={submit}>
              {busy ? <RefreshCw className="size-3.5 animate-spin" /> : null}
              保存规则
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
