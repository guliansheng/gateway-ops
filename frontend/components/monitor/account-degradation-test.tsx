"use client"

import { useEffect, useId, useRef, useState } from "react"
import { BrainCircuit, Loader2, Play, Share2, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { DegradationRunningBadge, ResultDialog, ResultFrame } from "@/components/monitor/group-degradation-settings"
import { DegradationModelSelect, DegradationProtocolSelect } from "@/components/monitor/degradation-model-select"
import { apiFetch } from "@/lib/api"
import { defaultDegradationConfig, degradationStatusMeta, type GroupDegradationResult, type GroupDegradationView } from "@/lib/group-degradation"
import type { RelayGroupView, RelayOverview } from "@/lib/api-types"
import { degradationRunKey, startDegradationRun, useDegradationRun } from "@/lib/degradation-runs"
import { cn } from "@/lib/utils"

function AccountTestForm({ stationID, accountID, onResult, onClear, onDelete, historyLimit = 20, disabled = false }: { stationID: number; accountID: number; onResult?: (result: GroupDegradationResult) => void; onClear?: () => void; onDelete?: (results: GroupDegradationResult[]) => void; historyLimit?: number; disabled?: boolean }) {
  const id = useId()
  const [config, setConfig] = useState(defaultDegradationConfig)
  const [models, setModels] = useState<string[]>([])
  const [results, setResults] = useState<GroupDegradationResult[]>([])
  const [modelsLoading, setModelsLoading] = useState(true)
  const [modelsError, setModelsError] = useState("")
  const [fallbackModel, setFallbackModel] = useState("")
  const [protocol, setProtocol] = useState("")
  const [loading, setLoading] = useState(true)
  const [loaded, setLoaded] = useState(false)
  const [version, setVersion] = useState(0)
  const runKey = degradationRunKey("account", stationID, accountID)
  const backgroundRun = useDegradationRun(runKey)
  const running = backgroundRun.running
  const handledRun = useRef(backgroundRun.finishedAt)
  const [clearing, setClearing] = useState(false)
  const [clearConfirm, setClearConfirm] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const [selected, setSelected] = useState<GroupDegradationResult | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<GroupDegradationResult | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [shareTarget, setShareTarget] = useState<GroupDegradationResult | null>(null)
  const [groups, setGroups] = useState<RelayGroupView[]>([])
  const [groupsLoading, setGroupsLoading] = useState(false)
  const [shareGroup, setShareGroup] = useState("")
  const [sharing, setSharing] = useState(false)
  const [shareError, setShareError] = useState("")
  const controller = useRef<AbortController | null>(null)
  const mutation = useRef(false)
  const endpoint = `/relay-stations/${stationID}/accounts/${accountID}/degradation`

  useEffect(() => {
    const abort = new AbortController()
    controller.current = abort
    mutation.current = false
    setLoading(true); setLoaded(false); setError(""); setMessage(""); setSelected(null); setResults([]); setModels([]); setDeleting(false); setDeleteTarget(null)
    setShareTarget(null); setSharing(false); setShareError(""); setShareGroup("")
    setModelsLoading(true); setModelsError("")
    void apiFetch<{ results: GroupDegradationResult[] }>(endpoint, { signal: abort.signal }).then((data) => {
      if (!abort.signal.aborted) { setResults(data.results ?? []); setLoaded(true) }
    }).catch((requestError: unknown) => { if (!abort.signal.aborted) setError(requestError instanceof Error ? requestError.message : "测试记录读取失败") }).finally(() => { if (!abort.signal.aborted) setLoading(false) })
    void apiFetch<string[]>(`/relay-stations/${stationID}/accounts/${accountID}/models`, { signal: abort.signal }).then((data) => {
      if (!abort.signal.aborted) { setModels(data); setConfig((current) => ({ ...current, model: current.model || data[0] || "" })) }
    }).catch((requestError: unknown) => { if (!abort.signal.aborted) setModelsError(requestError instanceof Error ? requestError.message : "模型读取失败") }).finally(() => { if (!abort.signal.aborted) setModelsLoading(false) })
    return () => abort.abort()
  }, [endpoint, stationID, accountID, version])
  useEffect(() => {
    // The run outlives this dialog; adopt its result once when it finishes.
    if (backgroundRun.running || !backgroundRun.finishedAt || backgroundRun.finishedAt === handledRun.current) return
    handledRun.current = backgroundRun.finishedAt
    const result = backgroundRun.result
    if (!result) { if (backgroundRun.error) setError(backgroundRun.error); return }
    setResults((current) => [result, ...current.filter((entry) => entry.id !== result.id)].slice(0, historyLimit))
    onResult?.(result)
    setMessage("测试完成，记录已保存")
    if (result.status === "error") setError(result.reason)
  }, [backgroundRun, historyLimit, onResult])

  function run() {
    if (!loaded || running || !config.model.trim()) return
    setError(""); setMessage("测试已开始，关闭弹窗会在后台继续运行，完成后自动写入记录")
    // No request signal: closing the dialog must not cancel a paid test.
    startDegradationRun(runKey, () => apiFetch<GroupDegradationResult>(`${endpoint}/run`, { method: "POST", body: JSON.stringify({ ...config, fallback_model: fallbackModel, protocol }) }))
  }

  async function clearHistory() {
    if (clearing) return
    setClearing(true); setError(""); setMessage("")
    try {
      await apiFetch<{ results: GroupDegradationResult[] }>(`${endpoint}/history`, { method: "DELETE" })
      setResults([]); setSelected(null); setClearConfirm(false); setMessage("测试记录已清空"); onClear?.()
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "清空记录失败")
    } finally { setClearing(false) }
  }

  async function deleteResult() {
    const target = deleteTarget
    if (!target || deleting) return
    setDeleting(true); setError(""); setMessage("")
    try {
      const data = await apiFetch<{ results: GroupDegradationResult[] }>(`${endpoint}/history/${target.id}`, { method: "DELETE" })
      const remaining = data.results ?? []
      setResults(remaining); setSelected((current) => (current?.id === target.id ? null : current)); setDeleteTarget(null); setMessage("测试记录已删除"); onDelete?.(remaining)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "删除记录失败")
    } finally { setDeleting(false) }
  }
  /**
   * Publishing one account test into a group history: the copy keeps the verdict
   * and the original test time, so the group monitor page shows the same evidence.
   * Only groups of this station can be chosen, and the server rejects an account
   * result that does not belong to the account.
   */
  async function openShare(result: GroupDegradationResult) {
    setError(""); setShareError(""); setShareGroup(""); setShareTarget(result); setGroupsLoading(true)
    try {
      const data = await apiFetch<RelayOverview>(`/relay-stations/${stationID}`)
      setGroups(data.groups ?? [])
    } catch (requestError) {
      setShareError(requestError instanceof Error ? requestError.message : "分组读取失败")
    } finally { setGroupsLoading(false) }
  }

  async function shareResult() {
    const target = shareTarget
    const group = groups.find((entry) => String(entry.external_id) === shareGroup)
    if (!target || !group || sharing) return
    setSharing(true); setShareError("")
    try {
      const data = await apiFetch<{ imported: boolean; view: GroupDegradationView }>(`/relay-stations/${stationID}/groups/${group.external_id}/degradation/import`, { method: "POST", body: JSON.stringify({ account_external_id: accountID, account_result_id: target.id }) })
      const listed = (data.view?.results ?? []).some((row) => row.source_account_id === accountID && row.created_at === target.created_at)
      const notes = [data.imported ? `已加入“${group.name}”的分组历史` : `该测试已在“${group.name}”的分组历史中`]
      if (data.imported && !listed) notes.push("记录早于分组保留的最新条数，暂时不在列表中")
      if (!group.monitor_enabled) notes.push("该分组未开启监控，暂不显示在渠道状态页")
      if (!data.view?.config?.enabled) notes.push("该分组未开启降智检测，暂不显示在渠道状态页")
      setShareTarget(null); setMessage(notes.join("；"))
    } catch (requestError) {
      setShareError(requestError instanceof Error ? requestError.message : "加入分组历史失败")
    } finally { setSharing(false) }
  }

  if (loading) return <p role="status" className="flex items-center gap-2 py-6 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />读取测试记录</p>
  if (!loaded) return <div className="space-y-3"><p role="alert" className="text-sm text-danger">{error}</p><Button variant="outline" onClick={() => setVersion((current) => current + 1)}>重新读取记录</Button></div>
  return <div className="space-y-5">
    <fieldset disabled={running} className="grid gap-4 sm:grid-cols-2">
      <label htmlFor={`${id}-method`} className="space-y-2 text-xs font-medium"><span>测试题目</span><select id={`${id}-method`} aria-label="测试题目" className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.method} onChange={(event) => setConfig({ ...config, method: event.target.value as typeof config.method })}><option value="pelican">鹈鹕测试</option><option value="candy">糖果测试</option></select></label>
      <DegradationModelSelect label="调用模型" models={models} value={config.model} loading={modelsLoading} disabled={running} onChange={(model) => setConfig({ ...config, model })} />
      <DegradationModelSelect label="备选模型" models={models} value={fallbackModel} loading={modelsLoading} disabled={running} emptyLabel="不使用备选模型" onChange={setFallbackModel} />
      <DegradationModelSelect label="评审模型" models={models} value={config.judge_model} loading={modelsLoading} disabled={running} emptyLabel="沿用实际调用模型" onChange={(judge_model) => setConfig({ ...config, judge_model })} />
      <label htmlFor={`${id}-effort`} className="space-y-2 text-xs font-medium"><span>思考强度</span><select id={`${id}-effort`} className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.effort} onChange={(event) => setConfig({ ...config, effort: event.target.value as typeof config.effort })}><option value="none">模型默认值</option><option value="low">低</option><option value="medium">中</option><option value="high">高</option></select></label>
      <DegradationProtocolSelect value={protocol} onChange={setProtocol} disabled={running} />
    </fieldset>
    {modelsError ? <div role="alert" className="flex flex-wrap items-center gap-2 text-xs text-danger">模型读取失败：{modelsError}<Button type="button" variant="ghost" size="sm" disabled={running} onClick={() => setVersion((value) => value + 1)}>重试读取</Button></div> : null}
	<div className="flex flex-wrap items-center justify-between gap-3 border-y border-border py-3"><p className="max-w-lg text-xs leading-5 text-muted-foreground">仅手动执行，产生真实上游费用；保留最近 {historyLimit} 条账号记录。</p><Button disabled={running || disabled || !config.model.trim()} className="gap-1.5" onClick={() => void run()}>{running ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}{running ? "测试中" : "立即测试"}</Button></div>
    {error ? <p role="alert" className="whitespace-pre-wrap break-words text-sm text-danger">{error}</p> : null}
    {message ? <p role="status" className="text-sm text-success">{message}</p> : null}
    <section aria-label="账号降智测试历史" className="space-y-2"><div className="flex items-center justify-between gap-2"><h3 className="text-sm font-medium">测试记录</h3><div className="flex items-center gap-1"><Button variant="ghost" size="sm" className="gap-1.5 text-danger" disabled={running || clearing || !results.length} onClick={() => { setError(""); setClearConfirm(true) }}><Trash2 className="size-3.5" />清空记录</Button><Button variant="ghost" size="sm" disabled={running} onClick={() => setVersion((current) => current + 1)}>刷新记录</Button></div></div><div className="space-y-3">{results.length ? results.map((result) => {
      const meta = degradationStatusMeta[result.status] ?? degradationStatusMeta.inconclusive
      return <article key={result.id} className="grid min-w-0 gap-3 border-b border-border py-3 sm:grid-cols-[minmax(0,1fr)_160px]"><div className="min-w-0 space-y-2 text-xs"><button type="button" onClick={() => setSelected(result)} className="min-w-0 space-y-2 break-words text-left"><span className="block truncate font-medium">{result.method === "pelican" ? "鹈鹕测试" : "糖果测试"} · {result.model || "未记录模型"}</span><time className="block text-muted-foreground">{new Date(result.created_at).toLocaleString("zh-CN")}</time><span className={cn("block", meta.tone)}>{meta.label}</span><span className="block text-brand">查看详情</span></button><div className="flex flex-wrap items-center gap-1"><Button type="button" variant="ghost" size="sm" className="h-7 gap-1 px-2 text-[11px]" disabled={running || deleting || sharing} aria-label={`把 ${new Date(result.created_at).toLocaleString("zh-CN")} 的测试加入分组历史`} onClick={() => void openShare(result)}><Share2 className="size-3" />加入分组历史</Button><Button type="button" variant="ghost" size="sm" className="h-7 gap-1 px-2 text-[11px] text-danger" disabled={running || deleting} aria-label={`删除 ${new Date(result.created_at).toLocaleString("zh-CN")} 的测试记录`} onClick={() => { setError(""); setDeleteTarget(result) }}><Trash2 className="size-3" />删除记录</Button></div></div><div className="w-[160px] max-w-full"><ResultFrame result={result} compact /></div></article>
    }) : <p className="py-4 text-xs text-muted-foreground">暂无测试记录</p>}</div></section>
    <AlertDialog open={clearConfirm} onOpenChange={(open) => { if (!clearing) setClearConfirm(open) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>清空测试记录？</AlertDialogTitle><AlertDialogDescription>仅清空该账号的降智测试历史，无法恢复。不影响其他账号、测试标准或分组检测。</AlertDialogDescription></AlertDialogHeader>{error ? <p role="alert" className="text-sm text-danger">{error}</p> : null}<AlertDialogFooter><AlertDialogCancel disabled={clearing}>取消</AlertDialogCancel><Button type="button" variant="destructive" disabled={clearing} onClick={() => void clearHistory()}>{clearing ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}确认清空</Button></AlertDialogFooter></AlertDialogContent></AlertDialog>
    <AlertDialog open={Boolean(deleteTarget)} onOpenChange={(open) => { if (!open && !deleting) setDeleteTarget(null) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>删除这条测试记录？</AlertDialogTitle><AlertDialogDescription>{deleteTarget ? `${new Date(deleteTarget.created_at).toLocaleString("zh-CN")} · ${deleteTarget.method === "pelican" ? "鹈鹕测试" : "糖果测试"}，删除后无法恢复。不影响其他记录、测试标准或分组检测。` : ""}</AlertDialogDescription></AlertDialogHeader>{error ? <p role="alert" className="text-sm text-danger">{error}</p> : null}<AlertDialogFooter><AlertDialogCancel disabled={deleting}>取消</AlertDialogCancel><Button type="button" variant="destructive" disabled={deleting} onClick={() => void deleteResult()}>{deleting ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}确认删除</Button></AlertDialogFooter></AlertDialogContent></AlertDialog>
    <Dialog open={Boolean(shareTarget)} onOpenChange={(open) => { if (!open && !sharing) setShareTarget(null) }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader><DialogTitle>加入分组降智历史</DialogTitle><DialogDescription>{shareTarget ? `${shareTarget.method === "pelican" ? "鹈鹕测试" : "糖果测试"} · ${shareTarget.model || "未记录模型"} · ${new Date(shareTarget.created_at).toLocaleString("zh-CN")}` : ""}</DialogDescription></DialogHeader>
        <DialogBody className="space-y-3">
          <p className="text-xs leading-5 text-muted-foreground">把这条账号测试复制到所选分组的降智历史，判定和生成内容保持原样，时间沿用原测试时间；重复导入同一条记录不会产生副本。</p>
          <label htmlFor={`${id}-share-group`} className="block space-y-2 text-xs font-medium"><span>目标分组</span><select id={`${id}-share-group`} aria-label="目标分组" className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" disabled={groupsLoading || sharing} value={shareGroup} onChange={(event) => setShareGroup(event.target.value)}><option value="">{groupsLoading ? "读取分组…" : groups.length ? "请选择分组" : "暂无分组"}</option>{groups.map((group) => <option key={group.external_id} value={String(group.external_id)}>{group.name}{group.monitor_enabled ? "" : "（未开启监控）"}</option>)}</select></label>
          {shareError ? <p role="alert" className="text-sm text-danger">{shareError}</p> : null}
        </DialogBody>
        <DialogFooter className="gap-2 px-4 pb-4 sm:px-6"><Button type="button" variant="outline" disabled={sharing} onClick={() => setShareTarget(null)}>取消</Button><Button type="button" className="gap-2" disabled={sharing || !shareGroup} onClick={() => void shareResult()}>{sharing ? <Loader2 className="size-4 animate-spin" /> : <Share2 className="size-4" />}加入分组历史</Button></DialogFooter>
      </DialogContent>
    </Dialog>
    <ResultDialog result={selected} onClose={() => setSelected(null)} />
  </div>
}

export function AccountDegradationTestButton({ stationID, accountID, accountName, disabled = false, latest, onResult, onClear, onDelete, showLatest = false }: { stationID: number; accountID: number; accountName: string; disabled?: boolean; latest?: GroupDegradationResult; onResult?: (result: GroupDegradationResult) => void; onClear?: () => void; onDelete?: (results: GroupDegradationResult[]) => void; showLatest?: boolean }) {
  const [open, setOpen] = useState(false)
  const run = useDegradationRun(degradationRunKey("account", stationID, accountID))
  const published = useRef(run.finishedAt)
  useEffect(() => {
    // 弹窗关闭后仍由这一层把结果回写到「最近降智检测」列：关闭弹窗、切去测别的
    // 账号都不会中断运行，完成后这一列直接换成新结果，不用重开弹窗。
    if (run.running || !run.finishedAt || run.finishedAt === published.current) return
    published.current = run.finishedAt
    if (!open && run.result) onResult?.(run.result)
  }, [run, open, onResult])
  return <Dialog open={open} onOpenChange={setOpen}>
    {showLatest ? <div className="min-w-0 space-y-1">
      <button type="button" disabled={disabled} onClick={() => setOpen(true)} className="w-full space-y-1 text-left text-xs hover:underline" aria-label={`查看${accountName}降智测试历史`}>
        {run.running ? <DegradationRunningBadge /> : null}
        {latest || !run.running ? <span className={cn("block truncate font-medium", latest ? degradationStatusMeta[latest.status]?.tone : "text-muted-foreground")}>{latest ? `${latest.method === "candy" ? "糖果" : "鹈鹕"} · ${latest.model || "未记录模型"}` : "暂无测试"}</span> : null}
        {latest ? <><span className={cn("block text-[10px]", degradationStatusMeta[latest.status]?.tone)}>{degradationStatusMeta[latest.status]?.label ?? "待确认"}</span><time className="block text-[10px] text-muted-foreground">{new Date(latest.created_at).toLocaleString("zh-CN")}</time></> : null}
      </button>
      {latest ? <div className="relative w-[160px] max-w-full"><ResultFrame result={latest} compact /><button type="button" disabled={disabled} onClick={() => setOpen(true)} aria-label={`查看${accountName}${latest.method === "candy" ? "糖果" : "鹈鹕"}测试历史`} className="absolute inset-0 rounded border border-transparent hover:border-brand focus-visible:outline-2 focus-visible:outline-ring" /></div> : null}
    </div> : <Tooltip delayDuration={200}><TooltipTrigger asChild><Button type="button" variant="outline" size="icon" className="size-8 shrink-0" aria-label={`测试${accountName}降智能力`} disabled={disabled} onClick={() => setOpen(true)}><BrainCircuit className="size-3.5" /></Button></TooltipTrigger><TooltipContent>降智测试与记录</TooltipContent></Tooltip>}
    {open ? <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl"><DialogHeader className="shrink-0 border-b border-border px-4 py-4 text-left sm:px-6"><DialogTitle>账号降智测试</DialogTitle><DialogDescription className="break-all">{accountName}</DialogDescription></DialogHeader><DialogBody className="px-4 py-4 sm:px-6"><AccountTestForm key={`${stationID}-${accountID}`} stationID={stationID} accountID={accountID} onResult={onResult} onClear={onClear} onDelete={onDelete} /></DialogBody></DialogContent> : null}
  </Dialog>
}

export function AccountDegradationTestForm({ stationID, accountID, onResult, onClear, onDelete, historyLimit = 20, disabled = false }: { stationID: number; accountID: number; onResult?: (result: GroupDegradationResult) => void; onClear?: () => void; onDelete?: (results: GroupDegradationResult[]) => void; historyLimit?: number; disabled?: boolean }) {
  return <AccountTestForm stationID={stationID} accountID={accountID} onResult={onResult} onClear={onClear} onDelete={onDelete} historyLimit={historyLimit} disabled={disabled} />
}
