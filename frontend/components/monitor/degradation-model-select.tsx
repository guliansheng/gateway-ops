"use client"

import { useId, useState } from "react"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

export function DegradationModelSelect({ label, models, value, onChange, disabled = false, loading = false, emptyLabel }: { label: string; models: string[]; value: string; onChange: (value: string) => void; disabled?: boolean; loading?: boolean; emptyLabel?: string }) {
  const id = useId()
  const [manual, setManual] = useState(false)
  const custom = manual || Boolean(value && !models.includes(value))
  return <div className="min-w-0 space-y-2"><label htmlFor={id} className="text-xs font-medium">{label}</label><Select disabled={disabled || loading} value={custom ? "__custom" : value || (emptyLabel ? "__empty" : "")} onValueChange={(next) => {
    if (next === "__custom") { setManual(true); return }
    setManual(false); onChange(next === "__empty" ? "" : next)
  }}><SelectTrigger id={id} className="h-10 w-full" aria-label={custom ? `${label}选择方式` : label}><SelectValue placeholder={loading ? "正在读取模型…" : "选择模型"} /></SelectTrigger><SelectContent>{emptyLabel ? <SelectItem value="__empty">{emptyLabel}</SelectItem> : null}{models.map((model) => <SelectItem key={model} value={model}>{model}</SelectItem>)}<SelectItem value="__custom">手动输入模型</SelectItem></SelectContent></Select>{custom ? <Input aria-label={label} value={value} maxLength={256} disabled={disabled} placeholder="输入完整模型名称" onChange={(event) => onChange(event.target.value)} /> : null}</div>
}

export function DegradationProtocolSelect({ value, onChange, disabled }: { value: string; onChange: (value: string) => void; disabled?: boolean }) {
  const id = useId()
  return <div className="min-w-0 space-y-2"><label htmlFor={id} className="text-xs font-medium">请求协议</label><Select value={value || "auto"} onValueChange={(protocol) => onChange(protocol === "auto" ? "" : protocol)} disabled={disabled}><SelectTrigger id={id} className="h-10 w-full" aria-label="请求协议"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="auto">沿用账号配置</SelectItem><SelectItem value="chat">Chat Completions</SelectItem><SelectItem value="responses">Responses</SelectItem><SelectItem value="anthropic">Anthropic Messages</SelectItem><SelectItem value="gemini">Gemini</SelectItem></SelectContent></Select></div>
}
