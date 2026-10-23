"use client"

import { useEffect, useRef, useState } from "react"
import { ListChecks, Loader2, Play, Square } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { ResultDialog, ResultFrame } from "@/components/monitor/group-degradation-settings"
import { DegradationModelSelect, DegradationProtocolSelect } from "@/components/monitor/degradation-model-select"
import { apiFetch } from "@/lib/api"
import { defaultDegradationConfig, degradationStatusMeta, type GroupDegradationResult } from "@/lib/group-degradation"
import { cn } from "@/lib/utils"

type Account = { external_id: number; name: string }
type Entry = { account: Account; result?: GroupDegradationResult; error?: string }

function BatchForm({ stationID, accounts, onResult }: { stationID: number; accounts: Account[]; onResult: (accountID: number, result: GroupDegradationResult) => void }) {
  const [config, setConfig] = useState(defaultDegradationConfig)
  const [fallback, setFallback] = useState("")
  const [protocol, setProtocol] = useState("")
  const [models, setModels] = useState<string[]>([])
  const [entries, setEntries] = useState<Entry[]>([])
  const [running, setRunning] = useState(false)
  const [current, setCurrent] = useState("")
  const [modelError, setModelError] = useState("")
  const [selected, setSelected] = useState<GroupDegradationResult | null>(null)
  const [modelsLoading, setModelsLoading] = useState(true)
  const mutation = useRef(false)
  const stop = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    const controller = new AbortController()
    const load = async () => {
      const available = new Set<string>()
      let failures = 0
      for (const account of accounts) {
        if (controller.signal.aborted) return
        try {
          const data = await apiFetch<string[]>(`/relay-stations/${stationID}/accounts/${account.external_id}/models`, { signal: controller.signal })
          data.forEach((model) => available.add(model))
        } catch { failures++ }
      }
      if (!controller.signal.aborted) {
        setModels([...available].sort())
        setModelsLoading(false)
        if (failures) setModelError(`${failures} 个账号的模型列表读取失败，可手动输入模型；执行前会重新校验。`)
      }
    }
    void load()
    return () => { controller.abort(); stop.current = true; mounted.current = false }
    // The parent freezes the selected accounts for the lifetime of this form.
  }, [stationID, accounts])

  async function run() {
    if (mutation.current || !config.model.trim()) return
    mutation.current = true; stop.current = false
    setRunning(true); setEntries([])
    try {
      for (const account of accounts) {
        if (stop.current) break
        setCurrent(account.name)
        try {
          const result = await apiFetch<GroupDegradationResult>(`/relay-stations/${stationID}/accounts/${account.external_id}/degradation/run`, { method: "POST", body: JSON.stringify({ ...config, model: config.model.trim(), fallback_model: fallback.trim(), protocol }) })
          if (mounted.current) { setEntries((items) => [...items, { account, result }]); onResult(account.external_id, result) }
        } catch (error) {
          if (mounted.current) setEntries((items) => [...items, { account, error: error instanceof Error ? error.message : "测试失败" }])
        }
      }
    } finally {
      mutation.current = false
      if (mounted.current) { setRunning(false); setCurrent("") }
    }
  }

  return <div className="min-w-0 space-y-4">
    <p className="text-xs text-warning">将对 {accounts.length} 个账号逐个发起真实上游调用，产生费用。首选模型不存在时使用备选模型；失败或待确认不会判为降智。</p>
    <fieldset disabled={running} className="grid gap-3 sm:grid-cols-2">
      <label className="space-y-1 text-xs font-medium"><span>测试题目</span><select aria-label="批量测试题目" className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.method} onChange={(event) => setConfig({ ...config, method: event.target.value as typeof config.method })}><option value="pelican">鹈鹕测试</option><option value="candy">糖果测试</option></select></label>
      <label className="space-y-1 text-xs font-medium"><span>思考强度</span><select aria-label="批量测试思考强度" className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.effort} onChange={(event) => setConfig({ ...config, effort: event.target.value as typeof config.effort })}><option value="none">模型默认值</option><option value="low">低</option><option value="medium">中</option><option value="high">高</option></select></label>
      <DegradationModelSelect label="首选模型" models={models} value={config.model} loading={modelsLoading} disabled={running} onChange={(model) => setConfig({ ...config, model })} />
      <DegradationModelSelect label="备选模型" models={models} value={fallback} loading={modelsLoading} disabled={running} emptyLabel="不使用备选模型" onChange={setFallback} />
      <div className="sm:col-span-2"><DegradationModelSelect label="批量评审模型" models={models} value={config.judge_model} loading={modelsLoading} disabled={running} emptyLabel="沿用实际调用模型" onChange={(judge_model) => setConfig({ ...config, judge_model })} /></div>
      <DegradationProtocolSelect value={protocol} onChange={setProtocol} disabled={running} />
    </fieldset>
    {modelError ? <p className="text-xs text-warning">{modelError}</p> : null}
    <div className="flex flex-wrap items-center justify-between gap-2 border-y border-border py-3"><p role="status" className="min-w-0 break-words text-xs text-muted-foreground">已完成 {entries.length} / {accounts.length}{running ? ` · 正在测试 ${current}` : ""}</p>{running ? <Button variant="outline" onClick={() => { stop.current = true }} className="gap-1.5"><Square className="size-4" />停止后续测试</Button> : <Button disabled={!config.model.trim()} onClick={() => void run()} className="gap-1.5"><Play className="size-4" />开始批量测试</Button>}</div>
    <div className="max-h-[45dvh] space-y-3 overflow-auto" aria-label="批量测试结果">{entries.map(({ account, result, error }) => <article key={account.external_id} className="grid min-w-0 gap-3 border-b border-border pb-3 sm:grid-cols-[minmax(0,1fr)_160px]"><div className="min-w-0 space-y-1 break-words text-xs"><p className="font-medium">{account.name}</p>{result ? <button type="button" onClick={() => setSelected(result)} className="block text-left"><time className="block text-[10px] text-muted-foreground">{new Date(result.created_at).toLocaleString("zh-CN")}</time><p className={cn(degradationStatusMeta[result.status]?.tone)}>{degradationStatusMeta[result.status]?.label} · {result.model || "未记录模型"}</p><p className="text-brand">查看详情</p></button> : <p role="alert" className="text-danger">{error}</p>}</div>{result ? <div className="w-[160px] max-w-full"><ResultFrame result={result} compact /></div> : null}</article>)}{running ? <Loader2 className="mx-auto size-5 animate-spin text-muted-foreground" /> : null}</div><ResultDialog result={selected} onClose={() => setSelected(null)} />
  </div>
}

export function BatchAccountDegradationTestButton({ stationID, accounts, disabled, onResult }: { stationID: number; accounts: Account[]; disabled?: boolean; onResult: (accountID: number, result: GroupDegradationResult) => void }) {
  const [snapshot, setSnapshot] = useState<Account[] | null>(null)
  return <Dialog open={snapshot !== null} onOpenChange={(open) => { if (!open) setSnapshot(null) }}><Button type="button" variant="outline" size="sm" disabled={disabled || !accounts.length} className="h-9 gap-1.5" onClick={() => setSnapshot([...accounts])}><ListChecks className="size-3.5" />批量降智测试</Button>{snapshot ? <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-4xl"><DialogHeader><DialogTitle>批量账号降智测试</DialogTitle><DialogDescription>已选 {snapshot.length} 个账号，关闭弹窗将停止后续测试；当前已发出的测试仍会保存记录。</DialogDescription></DialogHeader><BatchForm stationID={stationID} accounts={snapshot} onResult={onResult} /></DialogContent> : null}</Dialog>
}
