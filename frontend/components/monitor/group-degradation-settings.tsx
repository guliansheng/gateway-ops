"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { Download, Eye, Loader2, Play, Save, Settings2, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import { degradationRunKey, startDegradationRun, useDegradationRun } from "@/lib/degradation-runs"
import { defaultDegradationConfig, degradationStatusMeta, mergeDegradationConfig, previewHTML, type GroupDegradationConfig, type GroupDegradationResult, type GroupDegradationView } from "@/lib/group-degradation"
import { cn } from "@/lib/utils"

function formatTime(raw: string) {
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? "未知时间" : date.toLocaleString("zh-CN", { hour12: false })
}

export function ResultFrame({ result, large = false, compact = false }: { result: GroupDegradationResult; large?: boolean; compact?: boolean }) {
  const html = useMemo(() => result.html ? previewHTML(result.html) : "", [result.html])
  if (html && compact) return <div className="relative flex h-24 w-[160px] max-w-full items-center justify-center overflow-hidden rounded border border-border bg-white"><iframe title={`鹈鹕 HTML 测试结果 ${formatTime(result.created_at)}`} srcDoc={html} sandbox="allow-scripts" referrerPolicy="no-referrer" tabIndex={-1} loading="lazy" className="pointer-events-none h-[384px] w-[640px] shrink-0 origin-center scale-25 border-0 bg-white" /></div>
  return html ? <iframe title={`鹈鹕 HTML 测试结果 ${formatTime(result.created_at)}`} srcDoc={html} sandbox="allow-scripts" referrerPolicy="no-referrer" loading="lazy" className={cn("block w-full rounded-md border border-border bg-white", large ? "h-[min(65dvh,620px)]" : "h-72")} /> : <pre className={cn("overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted p-3 text-xs leading-6", compact ? "max-h-24 w-[160px] max-w-full" : "max-h-60")}>{result.answer || "此记录没有生成内容"}</pre>
}

function downloadHTML(result: GroupDegradationResult) {
  if (!result.html) return
  const url = URL.createObjectURL(new Blob([result.html], { type: "text/html;charset=utf-8" }))
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = `pelican-test-${result.id}.html`
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function ResultDialog({ result, onClose, minimal = false }: { result: GroupDegradationResult | null; onClose: () => void; minimal?: boolean }) {
  return <Dialog open={Boolean(result)} onOpenChange={(open) => { if (!open) onClose() }}>
    <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-auto sm:max-w-4xl">
      <DialogHeader><DialogTitle>{result?.method === "candy" ? "糖果测试结果" : "鹈鹕 HTML 测试结果"}</DialogTitle><DialogDescription>{result ? `${formatTime(result.created_at)} · ${result.model}` : ""}</DialogDescription></DialogHeader>
      {result ? <>{result.method === "candy" ? result.answer ? <ResultFrame result={result} large /> : null : result.html ? <ResultFrame result={result} large /> : null}{minimal ? null : <><p className="text-sm leading-6 text-muted-foreground">{result.reason}</p>{result.html ? <Button type="button" variant="outline" className="w-fit gap-2" onClick={() => downloadHTML(result)}><Download className="size-4" />下载原始 HTML</Button> : null}</>}</> : null}
    </DialogContent>
  </Dialog>
} 

/**
 * Group counterpart of the account list's latest test cell: method, model,
 * status, time and the compact preview, opening the group's detection history.
 */
export function GroupDegradationLatest({ result, groupName, onOpen, disabled = false }: { result?: GroupDegradationResult; groupName: string; onOpen: () => void; disabled?: boolean }) {
  const meta = result ? degradationStatusMeta[result.status] ?? degradationStatusMeta.inconclusive : null
  const method = result?.method === "candy" ? "糖果" : "鹈鹕"
  return <div className="min-w-0 space-y-1">
    <button type="button" disabled={disabled} onClick={onOpen} className="w-full space-y-1 text-left text-xs hover:underline" aria-label={result ? `查看${groupName}最近降智检测：${method} ${result.model || "未记录模型"}` : `查看${groupName}降智检测`}>
      <span className={cn("block truncate font-medium", meta ? meta.tone : "text-muted-foreground")}>{result ? `${method} · ${result.model || "未记录模型"}` : "暂无检测"}</span>
      {result && meta ? <><span className={cn("block text-[10px]", meta.tone)}>{meta.label}</span><time className="block text-[10px] text-muted-foreground">{formatTime(result.created_at)}</time></> : null}
    </button>
    {result ? <div className="relative w-[160px] max-w-full"><ResultFrame result={result} compact /><button type="button" disabled={disabled} onClick={onOpen} aria-label={`查看${groupName}${method}检测记录`} className="absolute inset-0 rounded border border-transparent hover:border-brand focus-visible:outline-2 focus-visible:outline-ring" /></div> : null}
  </div>
}

export function GroupDegradationStatus({ stationID, groupID }: { stationID: number; groupID: number }) {
  const [view, setView] = useState<GroupDegradationView | null>(null)
  const [selected, setSelected] = useState<GroupDegradationResult | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    const load = async () => {
      try {
        const data = await apiFetch<GroupDegradationView>(`/public/relay-stations/${stationID}/groups/${groupID}/degradation`, { skipAuthErrorHandler: true, signal: controller.signal })
        if (!controller.signal.aborted) {
          setView(data)
          if (!data.config.enabled || data.config.result_visibility === "hide") setSelected(null)
          setError("")
        }
      } catch (requestError) {
        if (!controller.signal.aborted) {
          setError(requestError instanceof Error ? requestError.message : "检测结果读取失败")
        }
      }
    }
    void load()
    const timer = window.setInterval(() => void load(), 30_000)
    return () => { controller.abort(); window.clearInterval(timer) }
  }, [stationID, groupID])
  if (!view?.config.enabled) return null
  // A failed model call says nothing about quality: the public bars only keep
  // completed tests (normal, degraded or inconclusive).
  const rawResults = view.results ?? []
  const results = rawResults.filter((result) => result.status !== "error")
  const showResults = view.config.result_visibility !== "hide"
  return <section className="mt-4 border-t border-border/70 pt-3" aria-label="降智检测">
    <h3 className="text-xs font-semibold">降智检测</h3>
    <div className="mt-2 flex gap-1" aria-label="最近检测记录，从早到晚">
      {[...results].slice(0, 18).reverse().map((result) => {
        const state = degradationStatusMeta[result.status] ?? degradationStatusMeta.inconclusive
        const barClass = cn("h-5 min-w-0 flex-1 rounded-[3px] outline-offset-2 focus-visible:outline-2 focus-visible:outline-ring", state.bar)
        const item = result.method === "candy" ? "糖果测试" : "鹈鹕测试"
        const accessibleLabel = `${formatTime(result.created_at)}，${item} ${result.model || "未记录模型"}`
        if (!showResults) return <Tooltip key={result.id} delayDuration={120}>
          <TooltipTrigger asChild><button type="button" className={cn(barClass, "cursor-default")} aria-label={`${accessibleLabel}，${state.label}：${state.explanation}`} onClick={(event) => event.preventDefault()} /></TooltipTrigger>
          <TooltipContent side="top" className="max-w-xs text-xs"><time>{formatTime(result.created_at)}</time><p className="mt-1 truncate">{result.model}</p><p className={cn("mt-1 font-medium", state.tone)}>{state.label}</p><p className="mt-0.5 leading-5">{state.explanation}</p></TooltipContent>
        </Tooltip>
        return <HoverCard key={result.id} openDelay={180} closeDelay={150}>
          <HoverCardTrigger asChild><button type="button" className={cn(barClass, "cursor-pointer hover:brightness-110")} aria-label={`${accessibleLabel}，${state.label}`} onClick={() => setSelected(result)} /></HoverCardTrigger>
          <HoverCardContent side="top" className="w-[min(38rem,calc(100vw-2rem))] p-3">
            <div className="mb-1 flex items-center justify-between gap-2 text-xs"><time>{formatTime(result.created_at)}</time><span className={cn("shrink-0 font-medium", state.tone)}>{state.label}</span></div>
            <p className="mb-2 truncate text-xs text-muted-foreground" title={result.model}>测试项目：{item} · {result.model || "未记录模型"}</p>
            {result.method === "candy" ? result.answer ? <ResultFrame result={result} /> : null : result.html ? <ResultFrame result={result} /> : null}
          </HoverCardContent>
        </HoverCard>
      })}
    </div>
    {!results.length ? <p className="py-2 text-[11px] text-muted-foreground">{rawResults.length ? "最近只有调用失败的检测，暂不展示" : "等待首次检测"}</p> : null}
    {showResults ? <ResultDialog result={selected} onClose={() => setSelected(null)} minimal /> : null}
  </section>

  }
export function GroupDegradationSettings({ stationID, groupID, groupName, onSaved, onResults }: { stationID: number; groupID: number; groupName: string; onSaved?: (config: GroupDegradationConfig) => void; onResults?: (results: GroupDegradationResult[]) => void }) {
  const id = useId()
  const [config, setConfig] = useState<GroupDegradationConfig>(defaultDegradationConfig)
  const [results, setResults] = useState<GroupDegradationResult[]>([])
  const [models, setModels] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
	const [loaded,setLoaded]=useState(false)
  const [loadVersion, setLoadVersion] = useState(0)
  const [runMethod,setRunMethod]=useState<"pelican"|"candy">("pelican")
  const [saving, setSaving] = useState(false)
  const runKey = degradationRunKey("group", stationID, groupID)
  const backgroundRun = useDegradationRun(runKey)
  const running = backgroundRun.running
  const handledRun = useRef(backgroundRun.finishedAt)
  const [clearing, setClearing] = useState(false)
  const [clearConfirm, setClearConfirm] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const [selected, setSelected] = useState<GroupDegradationResult | null>(null)
  const controller = useRef<AbortController | null>(null)
  const mutation = useRef(false)
  const endpoint = `/relay-stations/${stationID}/groups/${groupID}/degradation`
  useEffect(() => {
    const abort = new AbortController()
    controller.current = abort
    mutation.current = false
    setLoading(true); setLoaded(false); setError(""); setMessage(""); setDirty(false)
    setSaving(false); setClearing(false); setClearConfirm(false); setSelected(null)
    setResults([]); setModels([])
    void apiFetch<GroupDegradationView>(endpoint, { signal: abort.signal })
      .then((data) => {
        if (!abort.signal.aborted) {
          const next = mergeDegradationConfig(data.config)
          const history = (data.results ?? []).slice(0, next.history_limit)
          setConfig(next); setResults(history); setLoaded(true); onResults?.(history)
        }
      })
      .catch((requestError: unknown) => { if (!abort.signal.aborted) setError(requestError instanceof Error ? requestError.message : "配置读取失败") })
      .finally(() => { if (!abort.signal.aborted) setLoading(false) })
    void apiFetch<string[]>(`/relay-stations/${stationID}/groups/${groupID}/models`, { signal: abort.signal }).then((data) => { if (!abort.signal.aborted) setModels(data) }).catch(() => undefined)
    return () => abort.abort()
  }, [endpoint, stationID, groupID, loadVersion, onResults])
  const update = (patch: Partial<GroupDegradationConfig>) => { setConfig((current) => ({ ...current, ...patch })); setDirty(true); setMessage("") }
  const validHistoryLimit = Number.isInteger(config.history_limit) && config.history_limit >= 1 && config.history_limit <= 1000
  const validIntervals = [config.pelican_interval_minutes, config.candy_interval_minutes].every((value) => Number.isInteger(value) && value >= 5 && value <= 1440)
  async function save() {
    const signal = controller.current?.signal
    if (!loaded || !dirty || mutation.current || signal?.aborted) return
    if (!validHistoryLimit) { setError("历史最多保留条数必须为 1–1000 的整数"); return }
    if (!validIntervals) { setError("两种检测间隔必须为 5–1440 分钟的整数"); return }
    mutation.current = true; setSaving(true); setError(""); setMessage("")
    try {
      const data = await apiFetch<GroupDegradationView>(endpoint, { method: "PUT", body: JSON.stringify(config), signal })
      if (signal?.aborted) return
      const next = mergeDegradationConfig(data.config)
      const history = (data.results ?? []).slice(0, next.history_limit)
      setConfig(next); setResults(history); setSelected(null); setDirty(false); setMessage("配置已保存"); onSaved?.(next); onResults?.(history)
    } catch (requestError) {
      if (!signal?.aborted) setError(requestError instanceof Error ? requestError.message : "配置保存失败")
    } finally { if (!signal?.aborted) { mutation.current = false; setSaving(false) } }
  }
  function run() {
    if (!loaded || dirty || running || !config.enabled || !config.model.trim()) return
    setError(""); setMessage("检测已开始，关闭弹窗会在后台继续运行，完成后自动写入历史")
    // No request signal: closing the dialog must not cancel a paid detection.
    startDegradationRun(runKey, () => apiFetch<GroupDegradationResult>(`${endpoint}/run?method=${runMethod}`, { method: "POST" }))
  }
  async function clearHistory() {
    const signal = controller.current?.signal
    if (!loaded || mutation.current || dirty || !results.length || !clearConfirm || signal?.aborted) return
    mutation.current = true; setClearing(true); setError(""); setMessage("")
    try {
      const data = await apiFetch<GroupDegradationView>(`${endpoint}/history`, { method: "DELETE", signal })
      if (signal?.aborted) return
      const next = mergeDegradationConfig(data.config)
      const history = (data.results ?? []).slice(0, next.history_limit)
      setConfig(next); setResults(history); setSelected(null); setClearConfirm(false); setMessage("检测历史已清空"); onSaved?.(next); onResults?.(history)
    } catch (requestError) {
      if (!signal?.aborted) setError(requestError instanceof Error ? requestError.message : "清空历史失败")
    } finally { if (!signal?.aborted) { mutation.current = false; setClearing(false) } }
  }
  useEffect(() => {
    // A run started from this dialog keeps going after it closes; adopt the
    // finished result exactly once so the local history matches the server.
    if (backgroundRun.running || !backgroundRun.finishedAt || backgroundRun.finishedAt === handledRun.current) return
    handledRun.current = backgroundRun.finishedAt
    const result = backgroundRun.result
    if (!result) { if (backgroundRun.error) setError(backgroundRun.error); return }
    const history = [result, ...results.filter((entry) => entry.id !== result.id)].slice(0, config.history_limit)
    setResults(history); onResults?.(history); setMessage("检测完成，结果已保存")
    if (result.status === "error") setError(result.reason)
  }, [backgroundRun, config.history_limit, onResults, results])
  if (loading) return <p className="flex items-center gap-2 py-8 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />正在读取{groupName}配置</p>
  if (!loaded) return <div className="space-y-3 py-4"><p role="alert" className="text-sm text-danger">{error || "配置读取失败"}</p><Button type="button" variant="outline" onClick={() => setLoadVersion((value) => value + 1)}>重新读取配置</Button></div>
  const busy = saving || running || clearing
  return <div className="space-y-5">
    <div className="flex items-center justify-between gap-3 border-b border-border pb-4"><h3 className="font-semibold">{groupName}</h3><label htmlFor={`${id}-enabled`} className="flex items-center gap-2"><Switch id={`${id}-enabled`} checked={config.enabled} disabled={busy} onCheckedChange={(enabled) => update({ enabled })} />{config.enabled ? "已开启" : "未开启"}</label></div>
    <fieldset disabled={busy} className="grid gap-4 sm:grid-cols-2">
      <label className="space-y-2 text-xs font-medium"><span>测试方式</span><select aria-label="分组降智测试方式" className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.method} onChange={e=>update({method:e.target.value as GroupDegradationConfig["method"]})}><option value="pelican">仅鹈鹕</option><option value="candy">仅糖果</option><option value="both">鹈鹕和糖果</option></select></label>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-pelican-interval`}><span>鹈鹕检测间隔（分钟）</span><Input id={`${id}-pelican-interval`} type="number" min={5} max={1440} step={1} value={config.pelican_interval_minutes} onChange={e=>update({pelican_interval_minutes:Number(e.target.value)})}/></label>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-model`}><span>调用模型</span><Input id={`${id}-model`} list={`${id}-models`} value={config.model} onChange={(e) => update({ model: e.target.value })} placeholder="选择或输入模型名称" maxLength={256} /></label>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-judge`}><span>评审模型</span><Input id={`${id}-judge`} list={`${id}-models`} value={config.judge_model ?? ""} onChange={(e) => update({ judge_model: e.target.value })} placeholder="留空沿用调用模型" maxLength={256} /></label>
      <datalist id={`${id}-models`}>{models.map((model) => <option key={model} value={model} />)}</datalist>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-effort`}><span>思考强度</span><select id={`${id}-effort`} className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.effort} onChange={(e) => update({ effort: e.target.value as GroupDegradationConfig["effort"] })}><option value="none">模型默认值</option><option value="low">低</option><option value="medium">中</option><option value="high">高</option></select></label>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-candy-interval`}><span>糖果检测间隔（分钟）</span><Input id={`${id}-candy-interval`} type="number" min={5} max={1440} step={1} value={config.candy_interval_minutes} onChange={e=>update({candy_interval_minutes:Number(e.target.value)})}/></label>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-history-limit`}><span>历史最多保留（条）</span><Input id={`${id}-history-limit`} aria-label="历史最多保留（条）" type="number" min={1} max={1000} step={1} value={config.history_limit} onChange={(e) => update({ history_limit: Number(e.target.value) })} /><span className="block text-[11px] font-normal leading-5 text-muted-foreground">1–1000 条。调小并保存后会移除超出上限的旧记录。</span></label>
      <label className="space-y-2 text-xs font-medium" htmlFor={`${id}-visibility`}><span>公开测试结果</span><select id={`${id}-visibility`} className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.result_visibility} onChange={(e) => update({ result_visibility: e.target.value as GroupDegradationConfig["result_visibility"], preview_mode: "hover" })}><option value="hide">不展示 · 仅悬浮说明通过情况</option><option value="show">展示 · 悬浮预览，点击展开详情</option></select></label>
    </fieldset>
    <p className="border-l-2 border-brand pl-3 text-xs leading-5 text-muted-foreground">两种测试独立计时，同一分组不会并行测试。同轮到期优先运行鹈鹕，糖果跳过本轮并等待下个周期。</p>
    <p className="text-xs leading-5 text-muted-foreground">{config.result_visibility === "hide" ? "公开监控页仅显示通过情况，不提供答案或 HTML；管理员仍可查看保留范围内的完整历史。" : "公开监控页可悬浮预览生成内容，点击检测记录展开完整结果。"}</p>
    <p className="rounded-md border border-warning/30 bg-warning/5 px-3 py-2 text-xs leading-5 text-muted-foreground">开启后将按间隔产生真实模型调用和费用。生成结果由评审模型判断；最多保留 {config.history_limit || 20} 条检测历史。请求健康监控仍为被动统计。</p>
    {error && !clearConfirm ? <p role="alert" className="text-sm text-danger">{error}</p> : null}{message ? <p role="status" className="text-sm text-success">{message}</p> : null}
	<div className="flex flex-wrap items-center gap-2"><Select value={runMethod} onValueChange={(value)=>setRunMethod(value as "pelican"|"candy")} disabled={busy||dirty||!config.enabled}><SelectTrigger className="h-9 w-32 text-xs" aria-label="立即检测方式"><SelectValue /></SelectTrigger><SelectContent>{config.method !== "candy" ? <SelectItem value="pelican">鹈鹕测试</SelectItem> : null}{config.method !== "pelican" ? <SelectItem value="candy">糖果测试</SelectItem> : null}</SelectContent></Select><Button type="button" disabled={busy || !dirty || !validHistoryLimit || !validIntervals} className="gap-2" onClick={() => void save()}>{saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}保存配置</Button><Button type="button" variant="outline" disabled={busy || dirty || !config.enabled || !config.model.trim()} className="gap-2" title={running ? "检测在后台运行，关闭弹窗不会中断" : undefined} onClick={() => void run()}>{running ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}{running ? "检测中" : "立即检测"}</Button></div>
    <div data-degradation-history className="space-y-2 border-t border-border pt-4"><div className="flex items-center justify-between gap-2"><h4 className="text-sm font-medium">检测历史</h4><Button type="button" variant="outline" size="sm" className="gap-1.5 text-danger" disabled={busy || dirty || !results.length} title={dirty ? "请先保存配置" : undefined} onClick={() => { setError(""); setClearConfirm(true) }}><Trash2 className="size-3.5" />清空历史</Button></div>{!results.length ? <p className="text-xs text-muted-foreground">暂无检测记录</p> : results.map((result) => { const meta = degradationStatusMeta[result.status] ?? degradationStatusMeta.inconclusive; return <button key={result.id} type="button" disabled={busy} onClick={() => setSelected(result)} className="flex w-full items-center justify-between gap-3 rounded-md border border-border px-3 py-2 text-left text-xs hover:bg-muted"><span className="min-w-0 break-words"><time>{formatTime(result.created_at)}</time><span className="ml-2 text-muted-foreground">{result.method === "pelican" ? "鹈鹕" : "糖果"} · {result.model}</span>{result.used_account_name ? <span className="ml-2 text-muted-foreground">调用账号 {result.used_account_name}</span> : null}</span><span className={cn("shrink-0", meta.tone)}>{meta.label}</span></button> })}</div>
    <AlertDialog open={clearConfirm} onOpenChange={(open) => { if (!clearing) setClearConfirm(open) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>清空检测历史？</AlertDialogTitle><AlertDialogDescription>仅清空“{groupName}”的检测记录，无法恢复。不影响其他分组、检测配置或定时计划。</AlertDialogDescription></AlertDialogHeader>{error ? <p role="alert" className="text-sm text-danger">{error}</p> : null}<AlertDialogFooter><AlertDialogCancel disabled={clearing}>取消</AlertDialogCancel><Button type="button" variant="destructive" disabled={clearing} onClick={() => void clearHistory()}>{clearing ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}确认清空</Button></AlertDialogFooter></AlertDialogContent></AlertDialog>
    <ResultDialog result={selected} onClose={() => setSelected(null)} />
  </div>
}

export function GroupDegradationToggle({ stationID, groupID, groupName, config, onConfigChange, onResults }: {
  stationID: number
  groupID: number
  groupName: string
  config: GroupDegradationConfig | null
  onConfigChange: (config: GroupDegradationConfig | null) => void
  onResults?: (results: GroupDegradationResult[]) => void
}) {
  const endpoint = `/relay-stations/${stationID}/groups/${groupID}/degradation`
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const controller = useRef<AbortController | null>(null)
  const savingRef = useRef(false)

  useEffect(() => {
    const abort = new AbortController()
    controller.current = abort
    setLoading(true)
    setError("")
    setSaving(false)
    savingRef.current = false
    onConfigChange(null)
    onResults?.([])
    void apiFetch<GroupDegradationView>(endpoint, { signal: abort.signal })
      .then((view) => {
        if (!abort.signal.aborted) { onConfigChange(mergeDegradationConfig(view.config)); onResults?.(view.results ?? []) }
      })
      .catch((requestError: unknown) => {
        if (!abort.signal.aborted) setError(requestError instanceof Error ? requestError.message : "配置读取失败")
      })
      .finally(() => { if (!abort.signal.aborted) setLoading(false) })
    return () => abort.abort()
  }, [endpoint, onConfigChange, onResults])

  async function toggle(enabled: boolean) {
    const signal = controller.current?.signal
    if (loading || savingRef.current || !config || signal?.aborted || (enabled && !config.model.trim())) return
    savingRef.current = true
    setSaving(true)
    setError("")
    try {
      const view = await apiFetch<GroupDegradationView>(endpoint, {
        method: "PUT",
        body: JSON.stringify({ ...config, enabled }),
        signal,
      })
      if (!signal?.aborted) { onConfigChange(mergeDegradationConfig(view.config)); onResults?.(view.results ?? []) }
    } catch (requestError) {
      if (!signal?.aborted) setError(requestError instanceof Error ? requestError.message : "降智监控开关更新失败")
    } finally {
      if (!signal?.aborted) {
        savingRef.current = false
        setSaving(false)
      }
    }
  }

  const canEnable = Boolean(config?.model.trim())
  const status = loading ? "读取中" : saving ? "保存中" : error ? "操作失败" : config?.enabled ? "已开启" : canEnable ? "未开启" : "先配置"
  return <div className="flex min-w-0 items-center gap-1.5">
    <Switch
      checked={Boolean(config?.enabled)}
      disabled={loading || saving || !config || (!config.enabled && !canEnable)}
      onCheckedChange={(enabled) => void toggle(enabled)}
      aria-label={`${groupName}${config?.enabled ? "关闭" : "开启"}降智监控`}
      title={error || (!canEnable && !config?.enabled ? "请先在操作列配置调用模型" : "开启后会产生真实模型调用和费用")}
    />
    <span role={error ? "alert" : undefined} title={error || undefined} className={cn("text-[11px] font-medium", error ? "text-danger" : config?.enabled ? "text-brand" : "text-muted-foreground")}>
      {status}
    </span>
  </div>
}

export function GroupDegradationSettingsButton({ stationID, groupID, groupName, onSaved, onResults, disabled = false, open: controlledOpen, onOpenChange }: {
  stationID: number
  groupID: number
  groupName: string
  onSaved: (config: GroupDegradationConfig) => void
  onResults?: (results: GroupDegradationResult[]) => void
  disabled?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false)
  const open = controlledOpen ?? uncontrolledOpen
  const setOpen = onOpenChange ?? setUncontrolledOpen
  return <Dialog open={open} onOpenChange={setOpen}>
    <Tooltip delayDuration={250}>
      <TooltipTrigger asChild>
        <Button type="button" variant="outline" size="icon" className="size-8 shrink-0" disabled={disabled} aria-label={`配置${groupName}降智监控`} onClick={() => setOpen(true)}>
          <Settings2 className="size-3.5" />
        </Button>
      </TooltipTrigger>
      <TooltipContent side="top">降智监测配置</TooltipContent>
    </Tooltip>
    {open ? <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-auto sm:max-w-3xl">
      <DialogHeader><DialogTitle>分组降智监测配置</DialogTitle><DialogDescription>仅修改“{groupName}”分组的检测与公开结果设置。</DialogDescription></DialogHeader>
      <GroupDegradationSettings key={`${stationID}-${groupID}`} stationID={stationID} groupID={groupID} groupName={groupName} onSaved={onSaved} onResults={onResults} />
    </DialogContent> : null}
  </Dialog>
}
