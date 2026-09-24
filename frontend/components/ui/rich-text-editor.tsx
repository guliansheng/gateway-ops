"use client"

import { useEffect, useState } from "react"
import "@wangeditor-next/editor/dist/css/style.css"
import { Editor, Toolbar } from "@wangeditor-next/editor-for-react"
import type { IDomEditor, IEditorConfig, IToolbarConfig } from "@wangeditor-next/editor"
import { cn } from "@/lib/utils"

interface RichTextEditorProps {
  value: string
  onChange: (html: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
}

/**
 * 正文展示样式：右侧预览和公开页复用。
 * 逐项对齐 wangEditor 编辑区的默认样式（字号、间距、浅底代码块、表格边框等），
 * 两边观感一致；表格统一铺满可用宽度。底色和基础文字色由外层面板提供
 * （预览面板为白底 #333，与编辑区相同），这里只描述元素本身。
 */
export const richTextContentClass = cn(
  "text-sm leading-[1.5] break-words",
  // 段落 / 标题：间距 15px / 20px，em 相对字号，700 加粗
  "[&_p]:my-[15px]",
  "[&_h1]:my-[20px] [&_h1]:text-[2em] [&_h1]:font-bold",
  "[&_h2]:my-[20px] [&_h2]:text-[1.5em] [&_h2]:font-bold",
  "[&_h3]:my-[20px] [&_h3]:text-[1.17em] [&_h3]:font-bold",
  // h4 与 h3 同号 1.17em（wangEditor 默认 h4=1em，按需求改大；编辑区同步覆盖）
  "[&_h4]:my-[20px] [&_h4]:text-[1.17em] [&_h4]:font-bold",
  "[&_h5]:my-[20px] [&_h5]:text-[0.83em] [&_h5]:font-bold",
  "[&_h6]:my-[20px] [&_h6]:text-[0.67em] [&_h6]:font-bold",
  // 列表：浏览器默认序号 / 圆点 + 40px 缩进（编辑器为 list-style:revert，效果等价）
  "[&_ol]:my-[15px] [&_ol]:pl-10 [&_ol]:list-decimal",
  "[&_ul]:my-[15px] [&_ul]:pl-10 [&_ul]:list-disc",
  "[&_li]:my-0 [&_li]:leading-[1.5]",
  // 引用：浅底 + 蓝色粗左边框；浅底上固定深色字，暗色模式也清晰
  "[&_blockquote]:my-[10px] [&_blockquote]:border-l-8 [&_blockquote]:border-[#b4d5ff] [&_blockquote]:bg-[#f5f2f0] [&_blockquote]:p-[10px] [&_blockquote]:text-[#333]",
  // 行内 code：浅底小圆角 + 等宽字体（编辑器为 monospace）
  "[&_code]:rounded-[3px] [&_code]:bg-[#f5f2f0] [&_code]:p-[3px] [&_code]:text-[#333] [&_code]:[font-family:monospace]",
  // 代码块：盒子在 pre>code 上（浅底、细边框、14px 等宽、超宽横向滚动），与编辑器一致
  "[&_pre]:whitespace-pre-wrap [&_pre]:leading-[1.5] [&_pre]:[font-family:monospace]",
  "[&_pre>code]:my-[0.5em] [&_pre>code]:block [&_pre>code]:overflow-auto [&_pre>code]:whitespace-pre [&_pre>code]:break-normal [&_pre>code]:rounded-[4px] [&_pre>code]:border [&_pre>code]:border-[#e8e8e8] [&_pre>code]:bg-[#f5f2f0] [&_pre>code]:p-[1em] [&_pre>code]:text-left [&_pre>code]:text-[14px] [&_pre>code]:text-[#333] [&_pre>code]:leading-[1.5] [&_pre>code]:[font-family:Consolas,Monaco,'Andale_Mono','Ubuntu_Mono',monospace] [&_pre>code]:[tab-size:4] [&_pre>code]:[text-shadow:0_1px_#fff]",
  // 表格：细灰边框、单元格 3px/5px、表头浅底居中加粗；全部铺满可用宽度
  "[&_table]:mt-[10px] [&_table]:w-full [&_table]:border-collapse [&_table]:table-fixed",
  "[&_td]:min-w-[30px] [&_td]:overflow-hidden [&_td]:border [&_td]:border-[#ccc] [&_td]:px-[5px] [&_td]:py-[3px] [&_td]:leading-[1.5] [&_td]:break-words [&_td]:break-all [&_td]:[white-space:pre-wrap]",
  "[&_th]:min-w-[30px] [&_th]:overflow-hidden [&_th]:border [&_th]:border-[#ccc] [&_th]:bg-[#f5f2f0] [&_th]:px-[5px] [&_th]:py-[3px] [&_th]:text-center [&_th]:font-bold [&_th]:leading-[1.5] [&_th]:break-words [&_th]:break-all [&_th]:[white-space:pre-wrap] [&_th]:text-[#333]",
  // 链接：保持品牌色 + 下划线（编辑区同步加了同款样式，两边一致）
  "[&_a]:text-brand [&_a]:underline [&_a]:underline-offset-2",
  // 图片：编辑器中为行内显示、无圆角边距
  "[&_img]:inline [&_img]:max-w-full [&_img]:min-h-[20px] [&_img]:min-w-[20px]",
)

/**
 * 富文本编辑器（wangEditor）：上工具栏固定，只有正文区滚动。
 * 输出为 HTML，与预览 / 公开页渲染同一套样式。
 */
export function RichTextEditor({ value, onChange, placeholder, disabled = false, className }: RichTextEditorProps) {
  const [editor, setEditor] = useState<IDomEditor | null>(null)

  // 项目没有图片 / 视频上传后端，隐藏相关菜单（未知 key 会被忽略，安全）。
  const toolbarConfig: Partial<IToolbarConfig> = {
    excludeKeys: ["group-image", "group-video", "insertImage", "uploadImage", "insertVideo", "uploadVideo"],
  }
  const editorConfig: Partial<IEditorConfig> = {
    placeholder: placeholder ?? "开始撰写帮助文档…",
  }

  useEffect(() => {
    if (!editor) return
    if (disabled) editor.disable()
    else editor.enable()
  }, [editor, disabled])

  // 外部 value 变化（打开弹窗加载、查找替换、删除重置）同步进编辑器；
  // 编辑器自身输入触发的 onChange 与 value 已一致，不会回写。
  useEffect(() => {
    if (!editor) return
    if (value !== editor.getHtml()) editor.setHtml(value || "")
  }, [editor, value])

  return (
    <div className={cn("flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border border-border bg-card", className)}>
      <div className="shrink-0 border-b border-border bg-muted/40 [&_.w-e-toolbar]:flex-wrap [&_.w-e-toolbar]:bg-transparent">
        <Toolbar editor={editor} defaultConfig={toolbarConfig} mode="default" />
      </div>
      <div className="min-h-0 flex-1 overflow-hidden [&_.w-e-text-container]:h-full [&_.w-e-text-container]:text-sm [&_.w-e-text-container_a]:text-brand [&_.w-e-text-container_a]:underline [&_.w-e-text-container_a]:underline-offset-2 [&_.w-e-text-container_[data-slate-editor]_h4]:text-[1.17em] [&_.w-e-text-container_table]:w-full">
        <Editor
          value={value}
          defaultConfig={editorConfig}
          mode="default"
          style={{ height: "100%" }}
          onCreated={(instance) => setEditor(instance)}
          onChange={(instance) => onChange(instance.getHtml())}
        />
      </div>
    </div>
  )
}

/** 预览区 / 公开页渲染富文本 HTML 的容器。 */
export function RichTextBody({ html, className }: { html: string; className?: string }) {
  return (
    <div
      className={cn(richTextContentClass, className)}
      // 正文来自管理员编辑器输出或后端清洗后的 HTML。
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
