"use client"

import { Fragment, useCallback, useLayoutEffect, useMemo, useRef, type CSSProperties, type ReactNode } from "react"
import { cn } from "@/lib/utils"
import { sanitizeHelpHtml } from "@/lib/help-html"
import { buildHelpDocument } from "@/lib/help-document"

interface RichTextEditorProps {
  value: string
  onChange: (html: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
}

/** 正文展示样式：后台安全预览和公开页面共用。 */
export const richTextContentClass = cn(
  "text-sm leading-[1.7] break-words text-[#333]",
  "[&_p]:my-4 [&_h1]:my-5 [&_h1]:text-2xl [&_h1]:font-bold",
  "[&_h2]:mb-4 [&_h2]:mt-8 [&_h2]:border-b [&_h2]:border-[#e5e7eb] [&_h2]:pb-2 [&_h2]:text-xl [&_h2]:font-bold",
  "[&_h3]:mb-3 [&_h3]:mt-6 [&_h3]:text-lg [&_h3]:font-semibold [&_h4]:my-4 [&_h4]:font-semibold",
  "[&_h5]:my-4 [&_h5]:font-semibold [&_h6]:my-4 [&_h6]:font-semibold",
  "[&_ol]:my-4 [&_ol]:list-decimal [&_ol]:pl-6 [&_ul]:my-4 [&_ul]:list-disc [&_ul]:pl-6 [&_li]:my-1",
  "[&_blockquote]:my-4 [&_blockquote]:border-l-4 [&_blockquote]:border-[#65b9a9] [&_blockquote]:bg-[#eaf6f2] [&_blockquote]:px-4 [&_blockquote]:py-2 [&_blockquote]:text-[#315a57]",
  "[&_code]:rounded [&_code]:bg-[#f1f5f9] [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.9em]",
  "[&_pre]:my-4 [&_pre]:max-w-full [&_pre]:overflow-x-auto [&_pre]:rounded-lg [&_pre]:border [&_pre]:border-[#dbe3ee] [&_pre]:bg-[#f8fafc] [&_pre]:p-4",
  "[&_pre>code]:block [&_pre>code]:whitespace-pre [&_pre>code]:break-normal [&_pre>code]:bg-transparent [&_pre>code]:p-0 [&_pre>code]:text-[13px] [&_pre>code]:leading-6",
  "[&_table]:my-4 [&_table]:w-full [&_table]:table-fixed [&_table]:border-collapse [&_table]:text-[13px]",
  "[&_td]:border [&_td]:border-[#dbe3ee] [&_td]:px-3 [&_td]:py-2 [&_td]:align-top [&_td]:[overflow-wrap:anywhere]",
  "[&_th]:border [&_th]:border-[#dbe3ee] [&_th]:bg-[#eef6f7] [&_th]:px-3 [&_th]:py-2 [&_th]:text-left [&_th]:font-semibold [&_th]:[overflow-wrap:anywhere]",
  "[&_a]:text-[#1674b8] [&_a]:underline [&_a]:underline-offset-2 [&_a]:[overflow-wrap:anywhere] [&_img]:h-auto [&_img]:max-w-full",
  "[&_details]:my-4 [&_details]:rounded [&_details]:border [&_details]:border-[#dbe3ee] [&_details]:p-3 [&_summary]:cursor-pointer [&_summary]:font-medium [&_hr]:my-6 [&_hr]:border-[#e5e7eb]",
)

const htmlTokenPattern = /(<!--[\s\S]*?(?:-->|$)|<\/?[a-z](?:"[^"]*"|'[^']*'|[^'">])*>|<![^>]*>)/gi
const htmlTagPattern = /^(<\/?)([\w:-]+)([\s\S]*?)(\/?>)$/
const htmlAttributePattern = /(\s+)([\w:-]+)(\s*=\s*)("[^"]*"|'[^']*'|[^\s>]+)/g

function highlightTag(token: string, keyPrefix: string) {
  const match = htmlTagPattern.exec(token)
  if (!match) return <span key={keyPrefix} className="text-[#b45309]">{token}</span>
  const [, bracket, name, attributes, close] = match
  const pieces: ReactNode[] = [<span key={`${keyPrefix}-open`} className="text-[#64748b]">{bracket}</span>, <span key={`${keyPrefix}-tag`} className="text-[#7c3aed]">{name}</span>]
  let lastIndex = 0
  htmlAttributePattern.lastIndex = 0
  let attribute = htmlAttributePattern.exec(attributes)
  let index = 0
  while (attribute) {
    if (attribute.index > lastIndex) pieces.push(<span key={`${keyPrefix}-space-${index}`}>{attributes.slice(lastIndex, attribute.index)}</span>)
    pieces.push(<span key={`${keyPrefix}-attr-${index}`} className="text-[#2563eb]">{attribute[1]}{attribute[2]}</span>)
    pieces.push(<span key={`${keyPrefix}-equals-${index}`} className="text-[#64748b]">{attribute[3]}</span>)
    pieces.push(<span key={`${keyPrefix}-value-${index}`} className="text-[#15803d]">{attribute[4]}</span>)
    lastIndex = attribute.index + attribute[0].length
    index += 1
    attribute = htmlAttributePattern.exec(attributes)
  }
  if (lastIndex < attributes.length) pieces.push(<span key={`${keyPrefix}-tail`}>{attributes.slice(lastIndex)}</span>)
  pieces.push(<span key={`${keyPrefix}-close`} className="text-[#64748b]">{close}</span>)
  return <Fragment key={keyPrefix}>{pieces}</Fragment>
}

function highlightHtml(source: string) {
  return source.split(htmlTokenPattern).filter(Boolean).map((token, index) => {
    if (token.startsWith("<!--")) return <span key={index} className="text-[#64748b]">{token}</span>
    if (token.startsWith("<")) return highlightTag(token, `tag-${index}`)
    return <span key={index} className="text-[#334155]">{token}</span>
  })
}

// 两层必须使用完全相同的字体、间距和不折行规则，避免光标与高亮错位。
const sourceTextStyle: CSSProperties = {
  fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace",
  fontSize: 13,
  fontWeight: 400,
  fontStyle: "normal",
  fontVariantLigatures: "none",
  lineHeight: "24px",
  letterSpacing: 0,
  tabSize: 2,
  padding: 16,
  margin: 0,
  border: 0,
  boxSizing: "border-box",
  whiteSpace: "pre",
  overflowWrap: "normal",
  wordBreak: "normal",
}

/** 原生 textarea 保留选区、撤销和输入法行为；高亮层仅显示转义后的 React 文本。 */
export function RichTextEditor({ value, onChange, placeholder, disabled = false, className }: RichTextEditorProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const highlightRef = useRef<HTMLPreElement>(null)
  const highlighted = useMemo(() => highlightHtml(value), [value])

  const syncHighlightScroll = useCallback(() => {
    const textarea = textareaRef.current
    const highlight = highlightRef.current
    if (!textarea || !highlight) return
    highlight.scrollTop = textarea.scrollTop
    highlight.scrollLeft = textarea.scrollLeft
  }, [])

  useLayoutEffect(syncHighlightScroll, [syncHighlightScroll, value])

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-lg border border-border bg-card focus-within:ring-2 focus-within:ring-ring/40", disabled && "opacity-60", className)}>
      <div className="flex shrink-0 items-center justify-between border-b border-border bg-muted/40 px-3 py-2">
        <span className="text-xs font-medium text-foreground">HTML 源码</span>
        <span className="font-mono text-[11px] text-muted-foreground">HTML</span>
      </div>
      <div className="relative min-h-0 flex-1 overflow-hidden bg-[#fbfcfe]">
        <pre ref={highlightRef} aria-hidden="true" style={sourceTextStyle} className="pointer-events-none absolute inset-0 overflow-hidden forced-colors:hidden"><code className="block w-max min-w-full [font:inherit]">{highlighted}{"\n"}</code></pre>
        <textarea
          ref={textareaRef}
          aria-label="帮助文档 HTML 源码"
          wrap="off"
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
          autoCorrect="off"
          disabled={disabled}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onScroll={syncHighlightScroll}
          placeholder={placeholder ?? "输入帮助文档 HTML…"}
          style={sourceTextStyle}
          className="absolute inset-0 z-10 size-full resize-none overflow-auto bg-transparent text-transparent caret-slate-900 outline-none placeholder:text-slate-400 selection:bg-blue-200/70 disabled:cursor-not-allowed forced-colors:text-[CanvasText]"
        />
      </div>
    </div>
  )
}

/** 预览区与公开页都只渲染允许列表清洗后的 HTML。 */
export function RichTextBody({ html, className }: { html: string; className?: string }) {
  const safeHtml = useMemo(() => sanitizeHelpHtml(html), [html])
  return <div className={cn(richTextContentClass, className)} dangerouslySetInnerHTML={{ __html: safeHtml }} />
}

/** 完整文档保留自身 CSS 和目录；不共享父页面 origin 或权限。 */
export function HelpDocumentFrame({ html, className, title = "帮助文档", transparentBackground = false, hideDocumentHeader = false }: { html: string; className?: string; title?: string; transparentBackground?: boolean; hideDocumentHeader?: boolean }) {
  const source = useMemo(() => buildHelpDocument(html, transparentBackground, hideDocumentHeader), [html, transparentBackground, hideDocumentHeader])
  return <iframe title={title} srcDoc={source} sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox" referrerPolicy="no-referrer" className={cn("block size-full min-h-0 border-0", transparentBackground ? "bg-transparent" : "bg-[#f4f7f9]", className)} />
}
