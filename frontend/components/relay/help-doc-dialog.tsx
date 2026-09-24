"use client"

import { useCallback, useEffect, useState } from "react"
import { ExternalLink, Loader2, MessageSquare, Save, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Checkbox } from "@/components/ui/checkbox"
import { cn } from "@/lib/utils"
import { HelpFindReplaceBar } from "@/components/relay/help-find-replace"
import { RichTextBody, RichTextEditor } from "@/components/ui/rich-text-editor"
import { apiFetch } from "@/lib/api"
import type { HelpComment, HelpDocument } from "@/lib/api-types"

interface HelpDocDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  station: { id: number; name: string }
}

/**
 * 中转站帮助文档编辑弹窗：所有功能都在这个弹窗里完成。
 * 左侧富文本编辑、右侧实时预览，可切换是否发布到公共页面、
 * 查看 / 删除（支持多选批量删除）访客留言、打开公共文档页面、保存和删除。
 */
export function HelpDocDialog({ open, onOpenChange, station }: HelpDocDialogProps) {
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [documentID, setDocumentID] = useState<number | null>(null)
  const [title, setTitle] = useState("")
  const [content, setContent] = useState("")
  const [enabled, setEnabled] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [comments, setComments] = useState<HelpComment[]>([])
  const [commentsOpen, setCommentsOpen] = useState(false)
  const [selected, setSelected] = useState<number[]>([])

  // 留言计数与管理列表：打开弹窗时随文档一起读，打开「留言」面板时再刷新一次。
  async function loadComments(documentId: number | null) {
    if (documentId == null) {
      setComments([])
      setSelected([])
      return
    }
    try {
      setComments(await apiFetch<HelpComment[]>(`/help/documents/${documentId}/comments`))
      setSelected([])
    } catch (error) {
      toast.error((error as Error).message || "读取留言失败")
    }
  }

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const doc = await apiFetch<HelpDocument | null>(`/help/stations/${station.id}/document`)
      setDocumentID(doc?.id ?? null)
      setTitle(doc?.title ?? "")
      setContent(doc?.content ?? "")
      setEnabled(doc?.enabled ?? false)
      setDirty(false)
      void loadComments(doc?.id ?? null)
    } catch (error) {
      toast.error((error as Error).message || "读取帮助文档失败")
    } finally {
      setLoading(false)
    }
  }, [station.id])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  function handleOpenChange(next: boolean) {
    if (!next && dirty) {
      void confirm({
        title: "放弃未保存的修改？",
        description: "当前帮助文档有未保存的修改，关闭后将丢失。",
        confirmLabel: "放弃修改",
        destructive: true,
      }).then((ok) => {
        if (ok) onOpenChange(false)
      })
      return
    }
    onOpenChange(next)
  }

  async function save() {
    const trimmed = title.trim()
    if (!trimmed) {
      toast.error("请填写文档标题")
      return
    }
    setSaving(true)
    try {
      const doc = await apiFetch<HelpDocument>(`/help/stations/${station.id}/document`, {
        method: "PUT",
        body: JSON.stringify({ title: trimmed, content, enabled }),
      })
      setDocumentID(doc.id)
      setTitle(doc.title)
      setDirty(false)
      toast.success(enabled ? "帮助文档已保存并发布" : "帮助文档已保存")
    } catch (error) {
      toast.error((error as Error).message || "保存失败")
    } finally {
      setSaving(false)
    }
  }

  async function remove() {
    if (documentID == null) return
    const ok = await confirm({
      title: "删除帮助文档？",
      description: `将删除「${station.name}」的帮助文档及全部留言，无法恢复。`,
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    try {
      await apiFetch(`/help/documents/${documentID}`, { method: "DELETE" })
      setDocumentID(null)
      setTitle("")
      setContent("")
      setEnabled(false)
      setDirty(false)
      setComments([])
      toast.success("帮助文档已删除")
    } catch (error) {
      toast.error((error as Error).message || "删除失败")
    }
  }

  function toggleSelected(id: number, checked: boolean) {
    setSelected((current) => (checked ? Array.from(new Set([...current, id])) : current.filter((itemID) => itemID !== id)))
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? comments.map((item) => item.id) : [])
  }

  // 批量删除：一次确认、一次请求，按服务端实际删除条数提示。
  async function removeSelected() {
    if (!selected.length) return
    const ok = await confirm({
      title: "删除所选留言？",
      description: `将删除选中的 ${selected.length} 条留言，公开页面立即不再显示，无法恢复。`,
      confirmLabel: `删除 ${selected.length} 条`,
      destructive: true,
    })
    if (!ok) return
    try {
      const result = await apiFetch<{ deleted: number }>("/help/comments/batch-delete", {
        method: "POST",
        body: JSON.stringify({ comment_ids: selected }),
      })
      setComments((current) => current.filter((item) => !selected.includes(item.id)))
      setSelected([])
      toast.success(`已删除 ${result.deleted} 条留言`)
    } catch (error) {
      toast.error((error as Error).message || "批量删除失败")
    }
  }

  // 删除单条留言：确认后调用管理接口，成功即从列表移除。
  async function removeComment(item: HelpComment) {
    const preview = item.body.length > 50 ? `${item.body.slice(0, 50)}…` : item.body
    const ok = await confirm({
      title: "删除留言？",
      description: `将删除 ${item.author_name} 的留言「${preview}」，无法恢复。`,
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    try {
      await apiFetch(`/help/comments/${item.id}`, { method: "DELETE" })
      setComments((prev) => prev.filter((comment) => comment.id !== item.id))
      setSelected([])
      toast.success("留言已删除")
    } catch (error) {
      toast.error((error as Error).message || "删除失败")
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={handleOpenChange}>
        <DialogContent className="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-[min(1680px,calc(100vw-2rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-[1680px]">
          <DialogHeader className="shrink-0 border-b border-border px-5 py-4 text-left sm:px-6">
            <DialogTitle className="flex min-w-0 items-center gap-2">
              <span className="truncate">帮助文档 · {station.name}</span>
              {dirty ? <span className="shrink-0 rounded bg-warning/10 px-1.5 py-0.5 text-[11px] font-medium text-warning">未保存</span> : null}
            </DialogTitle>
            <DialogDescription>左侧编辑富文本，右侧实时预览；开启发布后访客可通过公共文档页面查看。</DialogDescription>
          </DialogHeader>

          <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border px-5 py-3 sm:px-6">
            <div className="flex min-w-56 flex-1 items-center gap-2">
              <Label htmlFor="help-doc-title" className="shrink-0 text-xs text-muted-foreground">标题</Label>
              <Input
                id="help-doc-title"
                value={title}
                onChange={(event) => {
                  setTitle(event.target.value)
                  setDirty(true)
                }}
                placeholder="文档标题"
                className="h-9 flex-1"
                disabled={loading}
              />
            </div>
            <div className="flex items-center gap-2">
              <Label htmlFor="help-doc-enabled" className="text-xs text-muted-foreground">发布到公共页面</Label>
              <Switch
                id="help-doc-enabled"
                checked={enabled}
                onCheckedChange={(value) => {
                  setEnabled(value)
                  setDirty(true)
                }}
                disabled={loading}
              />
            </div>
            <div className="flex items-center gap-2">
              {documentID != null ? (
                <Button type="button" variant="ghost" size="sm" className="gap-1.5 text-danger hover:text-danger" onClick={() => void remove()} disabled={loading || saving}>
                  <Trash2 className="size-3.5" />
                  删除
                </Button>
              ) : null}
              {documentID != null ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="gap-1.5"
                  disabled={loading}
                  onClick={() => {
                    setCommentsOpen(true)
                    void loadComments(documentID)
                  }}
                >
                  <MessageSquare className="size-3.5" />
                  留言 {comments.length}
                </Button>
              ) : null}
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="gap-1.5"
                asChild
                title={enabled ? undefined : "当前未发布，访客打开后将看不到内容"}
              >
                <a href={`/public/help/${station.id}`} target="_blank" rel="noopener noreferrer">
                  <ExternalLink className="size-3.5" />
                  打开公共文档页面
                </a>
              </Button>
              <Button type="button" size="sm" className="gap-1.5" onClick={() => void save()} disabled={loading || saving}>
                {saving ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
                保存
              </Button>
            </div>
          </div>

          <HelpFindReplaceBar
            html={content}
            onApply={(next, count) => {
              setContent(next)
              setDirty(true)
              toast.success(`已替换 ${count} 处`)
            }}
          />
          <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-2 divide-y divide-border lg:grid-cols-2 lg:grid-rows-1 lg:divide-x lg:divide-y-0">
            <div className="flex min-h-0 flex-col p-4 sm:p-5">
              {loading ? (
                <p className="py-10 text-center text-sm text-muted-foreground">正在读取帮助文档…</p>
              ) : (
                <RichTextEditor
                  key={station.id}
                  value={content}
                  onChange={(html) => {
                    setContent(html)
                    setDirty(true)
                  }}
                  placeholder="开始撰写帮助文档…"
                  className="min-h-0 flex-1"
                />
              )}
            </div>
            {/* 白底 #333，与左侧 wangEditor 编辑区底色/文字色一致，预览即所见 */}
            <div className="min-h-0 overflow-y-auto bg-white p-4 text-[#333] sm:p-6">
              <p className="mb-3 text-xs font-medium text-[#6b7280]">预览（与公共文档页面一致）</p>
              {content ? (
                <RichTextBody html={content} />
              ) : (
                <p className="text-sm text-[#6b7280]">暂无内容，开始编辑后这里会实时预览。</p>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
      {/* 留言管理：与主弹窗同规格——占满一屏（dvh 自适应），留言列表在面板内滚动 */}
      <Dialog open={commentsOpen} onOpenChange={setCommentsOpen}>
        <DialogContent className="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-[min(760px,calc(100vw-2rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-[760px]">
          <DialogHeader className="shrink-0 border-b border-border px-5 py-4 text-left sm:px-6">
            <DialogTitle>留言管理 · {station.name}</DialogTitle>
            <DialogDescription>勾选多条可批量删除；删除后公开页面立即不再显示，且无法恢复。</DialogDescription>
          </DialogHeader>
          <div className={cn("flex shrink-0 items-center gap-3 border-b px-5 py-2.5 sm:px-6", selected.length > 0 ? "border-brand/20 bg-brand/5" : "border-border")}>
            <Checkbox
              checked={comments.length > 0 && selected.length >= comments.length ? true : selected.length > 0 ? "indeterminate" : false}
              onCheckedChange={(value) => toggleAll(value === true)}
              disabled={comments.length === 0}
              aria-label="全选留言"
            />
            <span className={selected.length > 0 ? "text-xs font-medium text-brand" : "text-xs text-muted-foreground"}>
              {selected.length > 0 ? `已选 ${selected.length} 条 / 共 ${comments.length} 条` : `共 ${comments.length} 条`}
            </span>
            {selected.length > 0 ? (
              <Button type="button" variant="destructive" size="sm" className="ml-auto h-8 gap-1.5" onClick={() => void removeSelected()}>
                <Trash2 className="size-3.5" />
                删除所选（{selected.length}）
              </Button>
            ) : null}
          </div>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-5 sm:p-6">
            {comments.length > 0 ? (
              comments.map((item) => (
                <div key={item.id} className="flex items-start gap-3 rounded-lg border border-border bg-card p-3 text-sm">
                  <Checkbox
                    className="mt-0.5"
                    checked={selected.includes(item.id)}
                    onCheckedChange={(value) => toggleSelected(item.id, value === true)}
                    aria-label={`选择 ${item.author_name} 的留言`}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-start justify-between gap-3">
                      <span className="min-w-0 font-medium">{item.author_name}</span>
                      <div className="flex shrink-0 items-center gap-2">
                        <time className="text-xs text-muted-foreground">{new Date(item.created_at).toLocaleString("zh-CN", { hour12: false })}</time>
                        <Button type="button" variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs text-danger hover:text-danger" onClick={() => void removeComment(item)}>
                          <Trash2 className="size-3.5" />
                          删除
                        </Button>
                      </div>
                    </div>
                    <p className="mt-2 whitespace-pre-wrap text-xs leading-5 text-muted-foreground">{item.body}</p>
                  </div>
                </div>
              ))
            ) : (
              <p className="py-16 text-center text-sm text-muted-foreground">暂无留言</p>
            )}
          </div>
        </DialogContent>
      </Dialog>
      {confirmDialog}
    </>
  )
}
