"use client"

import { useEffect, useId, useRef, useState } from "react"
import { Loader2, Save, Settings2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Textarea } from "@/components/ui/textarea"
import { apiFetch } from "@/lib/api"
import type { DegradationPrompts } from "@/lib/group-degradation"

function normalizePrompts(value: DegradationPrompts): DegradationPrompts {
  const fields = [["candy_prompt", "糖果题提示词", 32 * 1024], ["candy_expected_answer", "糖果题标准答案", 4 * 1024], ["pelican_prompt", "鹈鹕测试提示词", 32 * 1024]] as const
  const normalized = { ...value }
  for (const [key, label, limit] of fields) {
    if (typeof value[key] !== "string" || /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(value[key])) throw new Error(`${label}包含不允许的控制字符`)
    normalized[key] = value[key].trim()
    if (!normalized[key]) throw new Error(`${label}不能为空`)
    if (new TextEncoder().encode(normalized[key]).length > limit) throw new Error(`${label}最多 ${limit / 1024}KB`)
  }
  return normalized
}

function PromptSettingsForm({ stationID }: { stationID: number }) {
  const id = useId()
  const [settings, setSettings] = useState<DegradationPrompts | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const [loadVersion, setLoadVersion] = useState(0)
  const controller = useRef<AbortController | null>(null)
  const savingRef = useRef(false)
  const endpoint = `/relay-stations/${stationID}/degradation-prompts`

  useEffect(() => {
    const abort = new AbortController()
    controller.current = abort
    savingRef.current = false
    setLoading(true); setSaving(false); setSettings(null); setDirty(false); setError(""); setMessage("")
    void apiFetch<DegradationPrompts>(endpoint, { signal: abort.signal })
      .then((data) => { if (!abort.signal.aborted) setSettings(normalizePrompts(data)) })
      .catch((requestError: unknown) => { if (!abort.signal.aborted) setError(requestError instanceof Error ? requestError.message : "测试设置读取失败") })
      .finally(() => { if (!abort.signal.aborted) setLoading(false) })
    return () => abort.abort()
  }, [endpoint, loadVersion])

  function update(key: keyof DegradationPrompts, value: string) {
    setSettings((current) => current ? { ...current, [key]: value } : current)
    setDirty(true); setMessage("")
  }

  async function save() {
    const signal = controller.current?.signal
    if (!settings || !dirty || savingRef.current || signal?.aborted) return
    setError(""); setMessage("")
    let normalized: DegradationPrompts
    try { normalized = normalizePrompts(settings) } catch (validationError) { setError((validationError as Error).message); return }
    savingRef.current = true; setSaving(true)
    try {
      const data = await apiFetch<DegradationPrompts>(endpoint, { method: "PUT", body: JSON.stringify(normalized), signal })
      if (!signal?.aborted) { setSettings(normalizePrompts(data)); setDirty(false); setMessage("测试设置已保存") }
    } catch (requestError) {
      if (!signal?.aborted) setError(requestError instanceof Error ? requestError.message : "测试设置保存失败")
    } finally {
      if (!signal?.aborted) { savingRef.current = false; setSaving(false) }
    }
  }

  if (loading) return <p role="status" className="flex items-center gap-2 py-6 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />正在读取测试设置…</p>
  if (!settings) return <div className="space-y-3 py-3"><p role="alert" className="text-sm text-danger">{error || "测试设置读取失败"}</p><Button type="button" variant="outline" onClick={() => setLoadVersion((value) => value + 1)}>重新读取设置</Button></div>
  return <div className="space-y-4">
    <fieldset disabled={saving} className="grid gap-6 md:grid-cols-2">
      <section className="min-w-0 space-y-4 md:border-r md:border-border md:pr-6">
        <h3 className="border-b border-border pb-2 text-sm font-semibold">鹈鹕测试</h3>
        <label htmlFor={`${id}-pelican-prompt`} className="block space-y-2"><span className="text-xs font-medium">鹈鹕测试提示词</span><Textarea id={`${id}-pelican-prompt`} aria-label="鹈鹕测试提示词" value={settings.pelican_prompt} onChange={(event) => update("pelican_prompt", event.target.value)} className="min-h-64 resize-y text-sm leading-6 md:min-h-96" maxLength={32768} /></label>
        <p className="text-[11px] leading-5 text-muted-foreground">无标准答案，按提示词评审生成源码。</p>
      </section>
      <section className="min-w-0 space-y-4">
        <h3 className="border-b border-border pb-2 text-sm font-semibold">糖果测试</h3>
        <label htmlFor={`${id}-candy-prompt`} className="block space-y-2"><span className="text-xs font-medium">糖果题提示词</span><Textarea id={`${id}-candy-prompt`} aria-label="糖果题提示词" value={settings.candy_prompt} onChange={(event) => update("candy_prompt", event.target.value)} className="min-h-52 resize-y text-sm leading-6 md:min-h-60" maxLength={32768} /></label>
        <label htmlFor={`${id}-candy-answer`} className="block space-y-2"><span className="text-xs font-medium">糖果题标准答案</span><Textarea id={`${id}-candy-answer`} aria-label="糖果题标准答案" value={settings.candy_expected_answer} onChange={(event) => update("candy_expected_answer", event.target.value)} className="min-h-20 resize-y text-sm" maxLength={4096} /><span className="block text-[11px] leading-5 text-muted-foreground">仅供评审，不发送给作答模型，也不公开。</span></label>
      </section>
    </fieldset>
    {error ? <p role="alert" className="text-sm text-danger">{error}</p> : null}
    {message ? <p role="status" className="text-sm text-success">{message}</p> : null}
    <div className="flex items-center justify-between gap-3 border-t border-border pt-3"><p className="text-[11px] leading-5 text-muted-foreground">保存仅更新设置，不会立即执行检测。</p><Button type="button" disabled={saving || !dirty} className="shrink-0 gap-1.5" onClick={() => void save()}>{saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}保存设置</Button></div>
  </div>
}

export function DegradationPromptSettingsButton({ stationID, disabled = false }: { stationID: number; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  useEffect(() => setOpen(false), [stationID])
  return <Dialog open={open} onOpenChange={setOpen}>
    <Button type="button" variant="outline" size="sm" className="h-11 gap-1.5 sm:h-9" disabled={disabled} aria-label="降智测试设置" onClick={() => setOpen(true)}><Settings2 className="size-3.5" />降智测试标准</Button>
    {open ? <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-5xl"><DialogHeader className="border-b border-border pb-4"><DialogTitle>降智测试设置</DialogTitle><DialogDescription>当前中转站的分组监测与账号手动测试共用此标准。</DialogDescription></DialogHeader><PromptSettingsForm key={stationID} stationID={stationID} /></DialogContent> : null}
  </Dialog>
}
