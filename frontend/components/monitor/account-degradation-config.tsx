"use client"

import { useEffect, useId, useRef, useState } from "react"
import { Loader2, Save, Settings2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { DegradationModelSelect, DegradationProtocolSelect } from "@/components/monitor/degradation-model-select"
import { AccountDegradationTestForm } from "@/components/monitor/account-degradation-test"
import { apiFetch } from "@/lib/api"

export interface AccountDegradationConfig {
  enabled: boolean
  method: "pelican" | "candy" | "both"
  model: string
  judge_model: string
  effort: "none" | "low" | "medium" | "high"
  pelican_interval_minutes: number
  candy_interval_minutes: number
  history_limit: number
  disable_on_degraded: boolean
  protocol: string
}

const defaults: AccountDegradationConfig = {
  enabled: false, method: "pelican", model: "", judge_model: "", effort: "none",
  pelican_interval_minutes: 60, candy_interval_minutes: 10, history_limit: 20,
  disable_on_degraded: false, protocol: "",
}

export function AccountDegradationSettings({ stationID, accountID, accountName }: { stationID: number; accountID: number; accountName: string }) {
  const id = useId()
  const endpoint = `/relay-stations/${stationID}/accounts/${accountID}/degradation/config`
  const [config, setConfig] = useState(defaults)
  const [models, setModels] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [modelsLoading, setModelsLoading] = useState(true)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const controller = useRef<AbortController | null>(null)

  useEffect(() => {
    const abort = new AbortController()
    controller.current = abort
    setLoading(true); setModelsLoading(true); setError(""); setDirty(false)
    void apiFetch<AccountDegradationConfig>(endpoint, { signal: abort.signal }).then((value) => {
      if (!abort.signal.aborted) setConfig({ ...defaults, ...value })
    }).catch((requestError: unknown) => {
      if (!abort.signal.aborted) setError(requestError instanceof Error ? requestError.message : "配置读取失败")
    }).finally(() => { if (!abort.signal.aborted) setLoading(false) })
    void apiFetch<string[]>(`/relay-stations/${stationID}/accounts/${accountID}/models`, { signal: abort.signal }).then((value) => {
      if (!abort.signal.aborted) { setModels(value); setConfig((current) => ({ ...current, model: current.model || value[0] || "" })) }
    }).catch(() => undefined).finally(() => { if (!abort.signal.aborted) setModelsLoading(false) })
    return () => abort.abort()
  }, [endpoint, stationID, accountID])

  const update = (patch: Partial<AccountDegradationConfig>) => { setConfig((current) => ({ ...current, ...patch })); setDirty(true); setMessage("") }
  const methodSelection = { pelican: config.method !== "candy", candy: config.method !== "pelican" }
  function toggleMethod(kind: "pelican" | "candy", checked: boolean) {
    const next = { pelican: kind === "pelican" ? checked : methodSelection.pelican, candy: kind === "candy" ? checked : methodSelection.candy }
    if (!next.pelican && !next.candy) return
    update({ method: next.pelican && next.candy ? "both" : next.pelican ? "pelican" : "candy" })
  }
  const valid = Number.isInteger(config.history_limit) && config.history_limit >= 1 && config.history_limit <= 1000 && [config.pelican_interval_minutes, config.candy_interval_minutes].every((value) => Number.isInteger(value) && value >= 5 && value <= 1440) && (!config.enabled || Boolean(config.model.trim()))
  async function save() {
    const signal = controller.current?.signal
    if (!dirty || saving || !valid || signal?.aborted) return
    setSaving(true); setError(""); setMessage("")
    try {
      const saved = await apiFetch<AccountDegradationConfig>(endpoint, { method: "PUT", body: JSON.stringify(config), signal })
      if (!signal?.aborted) { setConfig(saved); setDirty(false); setMessage("配置已保存") }
    } catch (requestError) {
      if (!signal?.aborted) setError(requestError instanceof Error ? requestError.message : "配置保存失败")
    } finally { if (!signal?.aborted) setSaving(false) }
  }

  if (loading) return <p role="status" className="flex items-center gap-2 py-8 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />正在读取{accountName}配置</p>
  return <div className="space-y-5">
    <div className="flex items-center justify-between gap-3 border-b border-border pb-4"><h3 className="font-semibold">{accountName}</h3><label htmlFor={`${id}-enabled`} className="flex items-center gap-2"><Switch id={`${id}-enabled`} checked={config.enabled} disabled={saving} onCheckedChange={(enabled) => update({ enabled })} />{config.enabled ? "已开启" : "未开启"}</label></div>
    <fieldset disabled={saving} className="space-y-5">
      <section className="space-y-3">
        <h4 className="border-b border-border pb-2 text-xs font-semibold text-foreground">检测项目</h4>
        <div className="flex flex-col gap-3">
          <span className="text-xs font-medium">测试方式（可多选）</span>
          <div className="flex flex-wrap items-center gap-5">
            <label className="flex items-center gap-2 text-xs"><Checkbox checked={methodSelection.pelican} disabled={saving || config.method === "pelican"} onCheckedChange={(checked) => toggleMethod("pelican", checked === true)} aria-label="启用鹈鹕测试" />鹈鹕测试</label>
            <label className="flex items-center gap-2 text-xs"><Checkbox checked={methodSelection.candy} disabled={saving || config.method === "candy"} onCheckedChange={(checked) => toggleMethod("candy", checked === true)} aria-label="启用糖果测试" />糖果测试</label>
          </div>
          <p className="text-[11px] leading-5 text-muted-foreground">至少保留一项；两项都启用时按各自间隔独立计时。</p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="flex flex-col gap-3 text-xs font-medium" htmlFor={`${id}-pelican-interval`}><span>鹈鹕检测间隔（分钟）</span><Input id={`${id}-pelican-interval`} type="number" min={5} max={1440} step={1} value={config.pelican_interval_minutes} onChange={(event) => update({ pelican_interval_minutes: Number(event.target.value) })} /></label>
          <label className="flex flex-col gap-3 text-xs font-medium" htmlFor={`${id}-candy-interval`}><span>糖果检测间隔（分钟）</span><Input id={`${id}-candy-interval`} type="number" min={5} max={1440} step={1} value={config.candy_interval_minutes} onChange={(event) => update({ candy_interval_minutes: Number(event.target.value) })} /></label>
        </div>
      </section>
      <section className="space-y-3">
        <h4 className="border-b border-border pb-2 text-xs font-semibold text-foreground">模型与评审</h4>
        <div className="grid gap-4 sm:grid-cols-2">
          <DegradationModelSelect label="调用模型" models={models} value={config.model} loading={modelsLoading} disabled={saving} onChange={(model) => update({ model })} />
          <DegradationModelSelect label="评审模型" models={models} value={config.judge_model} loading={modelsLoading} disabled={saving} emptyLabel="沿用调用模型" onChange={(judge_model) => update({ judge_model })} />
          <label className="flex flex-col gap-3 text-xs font-medium" htmlFor={`${id}-effort`}><span>思考强度</span><select id={`${id}-effort`} className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={config.effort} onChange={(event) => update({ effort: event.target.value as AccountDegradationConfig["effort"] })}><option value="none">模型默认值</option><option value="low">低</option><option value="medium">中</option><option value="high">高</option></select></label>
        </div>
      </section>
      <section className="space-y-3">
        <h4 className="border-b border-border pb-2 text-xs font-semibold text-foreground">历史与行为</h4>
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="flex flex-col gap-3 text-xs font-medium" htmlFor={`${id}-history-limit`}><span>历史最多保留（条）</span><Input id={`${id}-history-limit`} type="number" min={1} max={1000} step={1} value={config.history_limit} onChange={(event) => update({ history_limit: Number(event.target.value) })} /><span className="block text-[11px] font-normal leading-5 text-muted-foreground">1–1000 条，保存后清理超出上限的旧记录。</span></label>
        </div>
        <div className="flex min-h-11 items-center justify-between gap-3 rounded-md border border-border px-3"><div><p className="text-xs font-medium">发现降智后关闭账号</p><p className="mt-1 text-[11px] text-muted-foreground">仅明确判定为降智时关闭上游调度；失败和待确认不会关闭。</p></div><Switch checked={config.disable_on_degraded} onCheckedChange={(disable_on_degraded) => update({ disable_on_degraded })} aria-label="发现降智后关闭账号" /></div>
      </section>
    </fieldset>
    <p className="border-l-2 border-brand pl-3 text-xs leading-5 text-muted-foreground">两种测试按各自间隔运行；同一分钟同时到期时先运行鹈鹕，糖果顺延，不会并行。</p>
    <p className="rounded-md border border-warning/30 bg-warning/5 px-3 py-2 text-xs leading-5 text-muted-foreground">定时测试会产生真实模型调用和费用；账号最多保留 {config.history_limit} 条历史。</p>
    {error ? <p role="alert" className="text-sm text-danger">{error}</p> : null}{message ? <p role="status" className="text-sm text-success">{message}</p> : null}
    <div className="sticky bottom-0 z-10 border-t border-border bg-background/95 pt-3 pb-1 backdrop-blur"><Button type="button" disabled={saving || !dirty || !valid} className="gap-2" onClick={() => void save()}>{saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}保存配置</Button></div>
    <AccountDegradationTestForm stationID={stationID} accountID={accountID} historyLimit={config.history_limit} disabled={dirty || saving} />
  </div>
}

export function AccountDegradationSettingsButton({ stationID, accountID, accountName, disabled = false }: { stationID: number; accountID: number; accountName: string; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}><Tooltip delayDuration={200}><TooltipTrigger asChild><Button type="button" variant="outline" size="icon" className="size-8 shrink-0" aria-label={`配置${accountName}降智测试`} disabled={disabled} onClick={() => setOpen(true)}><Settings2 className="size-3.5" /></Button></TooltipTrigger><TooltipContent>降智测试配置</TooltipContent></Tooltip>{open ? <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl"><DialogHeader className="shrink-0 border-b border-border px-4 py-4 text-left sm:px-6"><DialogTitle>账号降智测试配置</DialogTitle><DialogDescription>配置“{accountName}”的独立测试计划。</DialogDescription></DialogHeader><DialogBody className="px-4 py-4 sm:px-6"><AccountDegradationSettings key={`${stationID}-${accountID}`} stationID={stationID} accountID={accountID} accountName={accountName} /></DialogBody></DialogContent> : null}</Dialog>
}
