"use client"

import { useMemo, useState } from "react"
import { Search } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { countHtmlText, replaceHtmlText } from "@/lib/help-html"

/**
 * 全局查找替换条（wangEditor 自带菜单不含查找替换，这里自己实现）：
 * 匹配统计与替换都基于文本节点，不会破坏 HTML 结构或链接属性。
 * 替换只改编辑区内容，仍需点「保存」落库。
 */
export function HelpFindReplaceBar({ html, onApply }: { html: string; onApply: (nextHtml: string, count: number) => void }) {
  const [find, setFind] = useState("")
  const [replace, setReplace] = useState("")
  const count = useMemo(() => countHtmlText(html, find), [html, find])

  function apply() {
    if (!find) return
    const result = replaceHtmlText(html, find, replace)
    if (result.count > 0) onApply(result.html, result.count)
  }

  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-muted/20 px-5 py-2 sm:px-6">
      <Search className="size-4 shrink-0 text-muted-foreground" />
      <Input value={find} onChange={(event) => setFind(event.target.value)} placeholder="查找内容" className="h-8 w-40" aria-label="查找内容" />
      <Input value={replace} onChange={(event) => setReplace(event.target.value)} placeholder="替换为" className="h-8 w-40" aria-label="替换为" />
      <span className="min-w-16 text-xs text-muted-foreground">{find ? `共 ${count} 处` : "输入后显示匹配数"}</span>
      <Button type="button" size="sm" className="h-8 gap-1.5" disabled={count === 0} onClick={apply}>
        全部替换
      </Button>
      <span className="text-[11px] text-muted-foreground">全局匹配正文（含代码块），替换后记得保存</span>
    </div>
  )
}
