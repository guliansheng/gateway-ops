import { useEffect, useState } from "react"
import { Save, ShieldCheck } from "lucide-react"
import { toast } from "sonner"
import { apiFetch } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Textarea } from "@/components/ui/textarea"

interface SecuritySettings { public_frame_origins: string[] }

export function SecuritySettingsCard() {
  const [origins, setOrigins] = useState("")
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [version, setVersion] = useState(0)

  useEffect(() => {
    const abort = new AbortController()
    setLoaded(false)
    setError("")
    apiFetch<SecuritySettings>("/security-settings", { signal: abort.signal })
      .then((data) => { if (!abort.signal.aborted) { setOrigins(data.public_frame_origins.join("\n")); setLoaded(true) } })
      .catch((err: unknown) => { if (!abort.signal.aborted) setError(err instanceof Error ? err.message : "读取安全策略失败") })
    return () => abort.abort()
  }, [version])

  async function save() {
    setSaving(true)
    setError("")
    try {
      const public_frame_origins = origins.split(/\r?\n/).map((value) => value.trim()).filter(Boolean)
      const data = await apiFetch<SecuritySettings>("/security-settings", { method: "PUT", body: JSON.stringify({ public_frame_origins }) })
      setOrigins(data.public_frame_origins.join("\n"))
      toast.success("公开页面嵌入策略已保存，新加载的页面立即生效")
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败")
    } finally { setSaving(false) }
  }

  return <Card className="border border-border shadow-none md:col-span-2">
    <CardHeader className="pb-2"><CardTitle className="flex items-center gap-2 text-base font-semibold"><ShieldCheck className="size-4" />公开页面 iframe 放行策略</CardTitle></CardHeader>
    <CardContent className="space-y-3 text-sm">
      <p className="text-xs leading-5 text-muted-foreground">仅放行公开监控、模型价格、服务状态及帮助页；后台、登录页和管理 API 始终禁止嵌入。这不会授予跨域读取 API 的权限。</p>
      <label htmlFor="public-frame-origins" className="block space-y-2"><span className="text-xs font-medium">允许嵌入的来源（每行一个完整 HTTPS 域名，最多 20 个）</span><Textarea id="public-frame-origins" value={origins} onChange={(event) => setOrigins(event.target.value)} disabled={!loaded || saving} placeholder="https://qidian7.top" className="min-h-28 font-mono text-xs" maxLength={5120} /><span className="block text-[11px] leading-5 text-muted-foreground">不允许通配符、路径、查询参数或 HTTP。子域名和非默认端口需要单独添加；清空后保存将禁止所有嵌入。多层 iframe 的每一层来源都必须在白名单中。</span></label>
      {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
      <div className="flex flex-wrap items-center justify-between gap-2"><span className="text-[11px] text-muted-foreground">保存到数据库，无需重启；已打开的 iframe 需要重新加载。</span>{!loaded && error ? <Button variant="outline" size="sm" onClick={() => setVersion((value) => value + 1)}>重新读取</Button> : <Button size="sm" className="gap-1.5" disabled={!loaded || saving} onClick={() => void save()}><Save className="size-3.5" />{saving ? "保存中…" : "保存放行策略"}</Button>}</div>
    </CardContent>
  </Card>
}
