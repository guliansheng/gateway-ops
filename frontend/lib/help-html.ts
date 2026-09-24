// 帮助文档（富文本 HTML）的章节解析：为公开页生成目录锚点并支持按章节搜索。

export interface HelpSection {
  /** 章节锚点 id，与旧版目录的 slug 规则一致，level 0 的前言块没有锚点。 */
  id: string
  /** 标题级别 1-6；0 表示第一个标题之前的前言内容。 */
  level: number
  /** 标题文字，level 0 时为空字符串。 */
  heading: string
  /** 整个章节的纯文本，用于搜索过滤。 */
  text: string
  /** 章节 HTML（标题克隆并注入了 id，可直接渲染）。 */
  html: string
}

export interface HelpHeading {
  id: string
  level: number
  text: string
}

function slugify(text: string, used: Map<string, number>): string {
  const base = text.trim().toLowerCase().replace(/[^\w\u4e00-\u9fff]+/g, "-").replace(/^-|-$/g, "") || "section"
  const count = used.get(base) ?? 0
  used.set(base, count + 1)
  return count ? `${base}-${count + 1}` : base
}

/** 按 h1-h6 把富文本 HTML 切成章节；纯文本环境下返回空数组。 */
export function parseHelpSections(html: string): HelpSection[] {
  if (typeof document === "undefined") return []
  const doc = new DOMParser().parseFromString(html, "text/html")
  const used = new Map<string, number>()
  const sections: HelpSection[] = []
  let current: { id: string; level: number; heading: string; nodes: Node[] } | null = null

  const flush = () => {
    if (!current) return
    const container = document.createElement("div")
    for (const node of current.nodes) container.appendChild(node.cloneNode(true))
    sections.push({
      id: current.id,
      level: current.level,
      heading: current.heading,
      text: container.textContent ?? "",
      html: container.innerHTML,
    })
  }

  for (const node of Array.from(doc.body.childNodes)) {
    const isHeading = node instanceof HTMLElement && /^H[1-6]$/.test(node.tagName)
    if (isHeading) {
      flush()
      const heading = node
      const level = Number(heading.tagName.slice(1))
      const text = (heading.textContent ?? "").trim()
      const id = slugify(text, used)
      const clone = heading.cloneNode(true) as HTMLElement
      clone.id = id
      current = { id, level, heading: text, nodes: [clone] }
    } else if (current) {
      current.nodes.push(node)
    } else {
      current = { id: "", level: 0, heading: "", nodes: [node] }
    }
  }
  flush()
  return sections
}

/** 目录条目：只取有标题的章节。 */
export function helpHeadings(sections: HelpSection[]): HelpHeading[] {
  return sections.filter((section) => section.level > 0).map((section) => ({ id: section.id, level: section.level, text: section.heading }))
}

function textNodesOf(html: string): { doc: Document; nodes: Text[] } {
  const doc = new DOMParser().parseFromString(html, "text/html")
  const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT)
  const nodes: Text[] = []
  let node = walker.nextNode()
  while (node) {
    nodes.push(node as Text)
    node = walker.nextNode()
  }
  return { doc, nodes }
}

/**
 * 全文匹配统计：只在文本节点里查找，不会误伤标签属性
 * （例如 href 里的同名文字），也不会产生嵌套标签。
 */
export function countHtmlText(html: string, find: string): number {
  if (!find || typeof document === "undefined") return 0
  const { nodes } = textNodesOf(html)
  let total = 0
  for (const node of nodes) {
    let index = node.data.indexOf(find)
    while (index !== -1) {
      total += 1
      index = node.data.indexOf(find, index + find.length)
    }
  }
  return total
}

/** 全文替换（仅文本节点），返回新 HTML 与实际替换数量。 */
export function replaceHtmlText(html: string, find: string, replacement: string): { html: string; count: number } {
  if (!find || typeof document === "undefined") return { html, count: 0 }
  const { doc, nodes } = textNodesOf(html)
  let count = 0
  for (const node of nodes) {
    if (!node.data.includes(find)) continue
    let index = node.data.indexOf(find)
    while (index !== -1) {
      count += 1
      index = node.data.indexOf(find, index + find.length)
    }
    node.data = node.data.split(find).join(replacement)
  }
  return { html: doc.body.innerHTML, count }
}
