"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { Power, Plus, RefreshCw, Search, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import type { RelayAccountView, RelayDynamicMonitorConfig, RelayDynamicMonitorState, RelayGroupView } from "@/lib/api-types"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/** 与后端 dynamicMonitorMaxGroups 对齐。 */
const MAX_RULE_GROUPS = 20

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
  selectedIDs: number[]
}

let ruleGroupSeq = 0

function makeDraft(index: number, overrides: Partial<RuleGroupDraft> = {}): RuleGroupDraft {
  ruleGroupSeq += 1
  return {
    key: `rule-${ruleGroupSeq}`,
    name: overrides.name ?? `规则组 ${index}`,
    threshold: overrides.threshold ?? "10",
    consecutive: overrides.consecutive ?? "2",
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
    selectedIDs: [...(group.account_external_ids ?? [])],
  }))
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
  const [groupFilter, setGroupFilter] = useState("all")
  const [onlySelected, setOnlySelected] = useState(false)
  const selectedAccountIDs = useMemo(() => new Set(draft.selectedIDs), [draft.selectedIDs])

  const visibleAccounts = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    return accounts.filter((account) => {
      if (onlySelected && !selectedAccountIDs.has(account.external_id)) return false
      if (groupFilter !== "all" && !account.current_groups.some((group) => String(group.external_id) === groupFilter)) return false
      if (needle !== "" && !account.name.toLocaleLowerCase().includes(needle)) return false
      return true
    })
  }, [accounts, groupFilter, query, onlySelected, selectedAccountIDs])

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

  const allVisibleSelected = visibleAccounts.some((account) => !claimed.has(account.external_id))
    && visibleAccounts.filter((account) => !claimed.has(account.external_id)).every((account) => draft.selectedIDs.includes(account.external_id))

  return (
    <section className="rounded-md border border-border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
          <span className="shrink-0 text-[11px] font-medium text-muted-foreground">组 {index + 1}</span>
          <Input value={draft.name} onChange={(event) => onChange({ name: event.target.value })} disabled={busy} placeholder="规则组名称" aria-label={`规则组 ${index + 1} 名称`} className="h-8 w-36 text-xs" />
          <div className="flex items-center gap-1.5">
            <Input inputMode="decimal" value={draft.threshold} onChange={(event) => onChange({ threshold: event.target.value })} disabled={busy} className="h-8 w-20 text-xs tabular-nums" aria-label={`规则组 ${index + 1} 首字延迟阈值（秒）`} />
            <span className="shrink-0 text-xs text-muted-foreground">秒</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Input inputMode="numeric" value={draft.consecutive} onChange={(event) => onChange({ consecutive: event.target.value })} disabled={busy} className="h-8 w-16 text-xs tabular-nums" aria-label={`规则组 ${index + 1} 连续超标次数`} />
            <span className="shrink-0 text-xs text-muted-foreground">次超标即关闭</span>
          </div>
        </div>
        <Button type="button" variant="ghost" size="sm" className="h-8 shrink-0 gap-1 text-xs text-danger hover:text-danger" disabled={busy || !canRemove} onClick={onRemove} aria-label={`删除规则组 ${index + 1}`}>
          <Trash2 className="size-3.5" />删除
        </Button>
      </div>

      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="筛选账号名称" aria-label={`规则组 ${index + 1} 按账号名称筛选`} className="h-8 w-40 pl-8 text-xs" />
        </div>
        <Select value={groupFilter} onValueChange={setGroupFilter}>
          <SelectTrigger className="h-8 w-44 text-xs" aria-label={`规则组 ${index + 1} 按销售分组筛选`}><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部分组</SelectItem>
            {groups.map((group) => <SelectItem key={group.external_id} value={String(group.external_id)}>{group.name}</SelectItem>)}
          </SelectContent>
        </Select>
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
      {total > 1 ? <p className="mt-1.5 text-[11px] text-muted-foreground">共 {total} 个规则组；同一账号只能归属一个规则组。</p> : null}
    </section>
  )
}

/** 多规则组动态监测：每组独立的首字延迟阈值（秒）与超标次数，账号跨组互斥；补开规则全局一套。 */
export function DynamicMonitorDialog({ open, busy, config, state, accounts, groups, onOpenChange, onSave }: DynamicMonitorDialogProps) {
  const [enabled, setEnabled] = useState(config.enabled)
  const [ruleGroups, setRuleGroups] = useState<RuleGroupDraft[]>(() => toDrafts(config))
  const [minAccounts, setMinAccounts] = useState(String(config.min_accounts_per_group))
  const [allowAutoOpen, setAllowAutoOpen] = useState(config.allow_auto_open)
  const [autoOpenMinutes, setAutoOpenMinutes] = useState(String(config.auto_open_minutes))
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
      setAllowAutoOpen(next.allow_auto_open)
      setAutoOpenMinutes(String(next.auto_open_minutes))
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
    setRuleGroups((current) => (current.length <= 1 ? current : current.filter((draft) => draft.key !== key)))
  }

  function submit() {
    const minAccountsValue = Number(minAccounts)
    const autoOpenValue = Number(autoOpenMinutes)
    if (!Number.isInteger(minAccountsValue) || minAccountsValue < 0 || minAccountsValue > 1000) return setError("分组最少保留账号数必须是 0 到 1000 的整数")
    if (!Number.isInteger(autoOpenValue) || autoOpenValue < 0 || autoOpenValue > 10080) return setError("自动开启时间必须是 0 到 10080 分钟的整数")

    for (const [index, draft] of ruleGroups.entries()) {
      const label = draft.name.trim() || `第 ${index + 1} 组`
      const thresholdValue = Number(draft.threshold)
      const consecutiveValue = Number(draft.consecutive)
      if (!Number.isFinite(thresholdValue) || thresholdValue < 0.1 || thresholdValue > 600) return setError(`${label}：首字延迟阈值必须在 0.1 到 600 秒之间`)
      if (!Number.isInteger(consecutiveValue) || consecutiveValue < 1 || consecutiveValue > 100) return setError(`${label}：连续超标次数必须是 1 到 100 的整数`)
      if (draft.selectedIDs.length === 0) return setError(`${label}：请至少勾选一个参与账号`)
    }

    if (enabled && ruleGroups.length === 0) return setError("启用监测时请至少配置一个规则组")
    if (enabled && allowAutoOpen && autoOpenValue <= 0 && minAccountsValue <= 0) return setError("允许动态开启时，请设置自动开启时间或分组最少保留账号数")

    setError("")
    onSave({
      enabled,
      groups: ruleGroups.map((draft) => ({
        name: draft.name.trim(),
        first_token_seconds: Number(draft.threshold),
        consecutive_count: Number(draft.consecutive),
        account_external_ids: draft.selectedIDs,
      })),
      min_accounts_per_group: minAccountsValue,
      allow_auto_open: allowAutoOpen,
      auto_open_minutes: autoOpenValue,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onOpenChange(false) }}>
      <DialogContent className="flex w-[calc(100vw-2rem)] max-h-[calc(100dvh-2rem)] sm:max-w-3xl flex-col overflow-hidden p-0">
        <DialogHeader className="shrink-0 border-b border-border px-4 py-4 sm:px-6">
          <DialogTitle>账号动态监测</DialogTitle>
          <DialogDescription>
            规则只保存在当前中转站的这个功能里，不会写入分组配置，也不影响自动调组策略：每个规则组按自己的首字延迟阈值与超标次数关闭账号，随后按“分组最少保留账号数”和“超时自动开启”开回来。
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:px-6">
          <div className="flex min-h-11 items-center justify-between gap-6 rounded-md border border-border bg-muted/30 px-3 py-3">
            <div className="min-w-0">
              <p className="text-xs font-medium">启用账号动态监测</p>
              <p className="mt-0.5 text-[11px] text-muted-foreground">关闭判断在每次快照同步时执行，超时开启由后台每分钟任务处理。</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} disabled={busy} aria-label="启用账号动态监测" />
          </div>

          <section className="space-y-2.5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <Label className="text-xs font-medium">监测规则组</Label>
              <div className="flex items-center gap-3">
                <span className="text-[11px] text-muted-foreground">共 {ruleGroups.length} 组 · 已选 {totalSelected} / {accounts.length} 个账号</span>
                <Button type="button" variant="outline" size="sm" className="h-8 gap-1 text-xs" disabled={busy || ruleGroups.length >= MAX_RULE_GROUPS} onClick={addGroup}>
                  <Plus className="size-3.5" />添加规则组
                </Button>
              </div>
            </div>
            <p className="text-[11px] text-muted-foreground">每组独立设置首字延迟阈值（秒）与连续超标次数；同一账号只能归属一个规则组，已在别组选中的账号这里不可再选。</p>
            <div className="space-y-3">
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
            </div>
          </section>

          <section className="space-y-1.5">
            <div className="flex items-center gap-1.5">
              <Label htmlFor="dm-min-accounts" className="text-xs">分组最少保留账号数</Label>
              <Input id="dm-min-accounts" inputMode="numeric" value={minAccounts} onChange={(event) => setMinAccounts(event.target.value)} disabled={busy} className="h-9 w-24 text-xs tabular-nums" />
              <span className="text-[11px] text-muted-foreground">个</span>
            </div>
            <p className="text-[11px] text-muted-foreground">对所有规则组通用：任一分组可用账号低于该数时，会优先补开之前被规则关闭的账号。0 表示不设置，只在本弹窗生效。</p>
          </section>

          <section className="rounded-md border border-border bg-muted/30 px-3 py-3">
            <div className="flex min-h-9 items-center justify-between gap-6">
              <div className="min-w-0">
                <p className="text-xs font-medium">允许动态开启</p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">被规则关闭的账号，按超时时间或分组最少保留账号数自动开启。</p>
              </div>
              <Switch checked={allowAutoOpen} onCheckedChange={setAllowAutoOpen} disabled={busy} aria-label="允许动态开启" />
            </div>
            <div className="mt-3 flex items-center gap-3">
              <Label htmlFor="dm-auto-open" className="shrink-0 text-xs">关闭多久后自动开启</Label>
              <div className="flex items-center gap-1.5">
                <Input id="dm-auto-open" inputMode="numeric" value={autoOpenMinutes} onChange={(event) => setAutoOpenMinutes(event.target.value)} disabled={busy || !allowAutoOpen} className="h-9 w-24 text-xs tabular-nums" aria-label="关闭多久后自动开启（分钟）" />
                <span className="text-xs text-muted-foreground">分钟</span>
              </div>
              <span className="text-[11px] text-muted-foreground">0 表示不启用超时开启</span>
            </div>
          </section>

          <section className="space-y-2">
            <div className="flex items-center justify-between gap-2">
              <Label className="flex items-center gap-1.5 text-xs font-medium"><Power className="size-3.5 text-warning" />当前被规则关闭的账号</Label>
              <span className="text-[11px] text-muted-foreground">{closedAccounts.length} 个</span>
            </div>
            {closedAccounts.length === 0 ? (
              <p className="rounded-md border border-dashed border-border px-3 py-4 text-center text-[11px] text-muted-foreground">暂无被规则关闭的账号</p>
            ) : (
              <div className="max-h-40 space-y-1.5 overflow-y-auto rounded-md border border-border p-2 pr-1">
                {closedAccounts.map((closure) => (
                  <div key={closure.account_external_id} className="flex flex-wrap items-center justify-between gap-2 border border-transparent px-2 py-1.5">
                    <span className="min-w-0 truncate text-xs font-medium">{closure.account_name} <span className="font-mono text-[11px] font-normal text-muted-foreground">#{closure.account_external_id}</span></span>
                    <span className="text-[11px] text-muted-foreground">关闭于 {relativeTime(closure.closed_at)}</span>
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>

        <DialogFooter className="shrink-0 items-center justify-between gap-3 border-t border-border px-4 py-3 sm:px-6">
          <p className="min-w-0 text-xs text-danger" role="alert">{error}</p>
          <div className="flex shrink-0 gap-2">
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>取消</Button>
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
