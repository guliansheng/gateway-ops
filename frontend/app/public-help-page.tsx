"use client"

import { useEffect, useMemo, useState } from "react"
import { BookOpen, MessageSquare, Search } from "lucide-react"
import { useParams } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { RichTextBody } from "@/components/ui/rich-text-editor"
import { apiFetch } from "@/lib/api"
import { helpHeadings, parseHelpSections } from "@/lib/help-html"
import type { PublicHelpDocument } from "@/lib/api-types"

export default function PublicHelpPage() {
  const { stationID } = useParams()
  const [data, setData] = useState<PublicHelpDocument | null>(null)
  const [error, setError] = useState("")
  const [search, setSearch] = useState("")
  const [author, setAuthor] = useState("游客")
  const [body, setBody] = useState("")
  const [sending, setSending] = useState(false)

  useEffect(() => {
    if (!stationID) return
    setError("")
    void apiFetch<PublicHelpDocument>(`/public/relay-stations/${stationID}/help`, { skipAuthErrorHandler: true })
      .then(setData)
      .catch((e) => {
        setData(null)
        setError((e as Error).message || "帮助文档读取失败")
      })
  }, [stationID])

  // 富文本 HTML 按标题切章节：目录锚点 + 按章节搜索都基于它。
  const sections = useMemo(() => (data ? parseHelpSections(data.content) : []), [data])
  const headings = useMemo(() => helpHeadings(sections), [sections])
  const query = search.trim().toLocaleLowerCase()
  const visible = useMemo(
    () => (query ? sections.filter((section) => section.text.toLocaleLowerCase().includes(query)) : sections),
    [sections, query],
  )

  async function comment() {
    if (!stationID || !body.trim()) return
    setSending(true)
    try {
      await apiFetch(`/public/relay-stations/${stationID}/help/comments`, {
        method: "POST",
        skipAuthErrorHandler: true,
        body: JSON.stringify({ author_name: author, body }),
      })
      const next = await apiFetch<PublicHelpDocument>(`/public/relay-stations/${stationID}/help`, { skipAuthErrorHandler: true })
      setData(next)
      setBody("")
      setError("")
    } catch (e) {
      setError((e as Error).message || "留言失败")
    } finally {
      setSending(false)
    }
  }

  if (error && !data) return <main className="flex min-h-screen items-center justify-center p-6 text-sm text-danger">{error}</main>

  return (
    <main className="min-h-screen bg-muted/20 text-foreground">
      <div className="mx-auto max-w-[1500px] px-4 py-6 sm:px-6 lg:px-8">
        <header className="border-b border-border pb-5">
          <h1 className="flex items-center gap-2 text-2xl font-bold">
            <BookOpen className="size-6 text-brand" />
            {data?.title || "帮助文档"}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {data?.station_name} · 公开帮助页面 · 更新于 {data ? new Date(data.updated_at).toLocaleString("zh-CN", { hour12: false }) : "加载中"}
          </p>
        </header>
        <div className="mt-5 grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)_18rem]">
          <aside className="hidden lg:block">
            <div className="sticky top-5 max-h-[calc(100dvh-2rem)] overflow-y-auto rounded-lg border border-border bg-card p-3 shadow-sm">
              <p className="mb-2 text-xs font-semibold">目录</p>
              <nav className="space-y-1">
                {headings.map((heading) => (
                  <a
                    key={heading.id}
                    href={`#${heading.id}`}
                    className="block truncate rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                    style={{ paddingLeft: `${Math.min(heading.level, 5) * 8 + 8}px` }}
                  >
                    {heading.text}
                  </a>
                ))}
              </nav>
            </div>
          </aside>
          <section className="min-w-0 rounded-lg border border-border bg-card p-5 sm:p-8">
            <div className="mb-5 flex items-center gap-2">
              <Search className="size-4 shrink-0 text-muted-foreground" />
              <Input placeholder="查询标题和章节内容" value={search} onChange={(event) => setSearch(event.target.value)} />
            </div>
            {visible.length > 0 ? (
              visible.map((section, index) => (
                <article key={section.id || `section-${index}`}>
                  <RichTextBody html={section.html} className="[&_h1]:scroll-mt-6 [&_h2]:scroll-mt-6 [&_h3]:scroll-mt-6" />
                </article>
              ))
            ) : (
              <p className="py-8 text-center text-sm text-muted-foreground">没有匹配的内容</p>
            )}
          </section>
          <aside>
            {/* 留言区与左侧目录同规格：粘顶 +占满一屏（dvh 自适应），留言列表在区域内内部滚动 */}
            <div className="sticky top-5 flex h-[calc(100dvh-2rem)] flex-col gap-4">
              <div className="shrink-0 rounded-lg border border-border bg-card p-4">
                <div className="mb-3 flex items-center gap-2 text-sm font-semibold">
                  <MessageSquare className="size-4 text-brand" />
                  留言
                </div>
                <Input className="mb-2" value={author} onChange={(event) => setAuthor(event.target.value)} placeholder="名称（默认游客）" />
                <Textarea className="min-h-24" value={body} onChange={(event) => setBody(event.target.value)} placeholder="留下你的问题或建议" />
                <Button className="mt-2 w-full" disabled={sending || !body.trim()} onClick={() => void comment()}>
                  提交留言
                </Button>
                {error && data ? <p className="mt-2 text-xs text-danger">{error}</p> : null}
              </div>
              <div className="min-h-0 flex-1 space-y-2 overflow-y-auto">
                {data?.comments.map((item) => (
                  <div key={item.id} className="rounded-lg border border-border bg-card p-3 text-xs">
                    <div className="flex justify-between gap-2 font-medium">
                      <span>{item.author_name}</span>
                      <time className="text-muted-foreground">{new Date(item.created_at).toLocaleString("zh-CN", { hour12: false })}</time>
                    </div>
                    <p className="mt-2 whitespace-pre-wrap leading-5 text-muted-foreground">{item.body}</p>
                  </div>
                ))}
              </div>
            </div>
          </aside>
        </div>
      </div>
    </main>
  )
}
