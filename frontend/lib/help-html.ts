import referenceHelpHtml from "./help-reference.html?raw"
import { adaptHelpDocument, prepareHelpDocumentTemplate } from "./help-document"

// 帮助文档 HTML：目录解析、受限安全清洗与可配置中转站信息。

export interface HelpSection {
  /** 章节锚点 id；前言块没有锚点。 */
  id: string
  /** 标题级别 1-6；0 表示第一个标题之前的前言内容。 */
  level: number
  /** 标题文字，level 0 时为空字符串。 */
  heading: string
  /** 整个章节的纯文本，用于搜索过滤。 */
  text: string
  /** 含锚点 id 的章节 HTML。 */
  html: string
}

export interface HelpHeading {
  id: string
  level: number
  text: string
}

const allowedTags = new Set([
  "A", "B", "BLOCKQUOTE", "BR", "CAPTION", "CODE", "DEL", "DETAILS", "DIV", "EM", "FIGCAPTION", "FIGURE",
  "H1", "H2", "H3", "H4", "H5", "H6", "HR", "I", "IMG", "INS", "KBD", "LI", "MARK", "OL", "P", "PRE",
  "S", "SAMP", "SMALL", "SPAN", "STRONG", "SUB", "SUMMARY", "SUP", "TABLE", "TBODY", "TD", "TFOOT", "TH",
  "THEAD", "TR", "U", "UL", "VAR",
])

// 不安全容器连同内容整体移除，而不是 unwrap，避免脚本文本混进文档。
const blockedTags = new Set(["BASE", "EMBED", "FORM", "IFRAME", "INPUT", "LINK", "META", "NOSCRIPT", "OBJECT", "SCRIPT", "SELECT", "STYLE", "SVG", "MATH", "TEMPLATE", "TEXTAREA", "VIDEO", "AUDIO"])
const globalAttributes = new Set(["alt", "align", "class", "colspan", "height", "id", "rowspan", "scope", "start", "title", "type", "width"])
const safeStyleProperties = new Set([
  "background", "background-color", "border", "border-bottom", "border-color", "border-left", "border-radius",
  "border-style", "border-top", "border-width", "color", "font-size", "font-style", "font-weight", "height",
  "list-style", "margin", "margin-bottom", "margin-left", "margin-right", "margin-top", "max-width", "padding",
  "padding-bottom", "padding-left", "padding-right", "padding-top", "text-align", "text-decoration", "vertical-align", "width",
])

function safeStyle(style: string): string {
  const declarations: string[] = []
  for (const declaration of style.split(";")) {
    const separator = declaration.indexOf(":")
    if (separator < 1) continue
    const property = declaration.slice(0, separator).trim().toLowerCase()
    const value = declaration.slice(separator + 1).trim()
    const lowerValue = value.toLowerCase()
    if (!safeStyleProperties.has(property) || !value || /url\s*\(|expression|@import|javascript|vbscript|[<>\\]/i.test(value) || lowerValue.includes("/*") || lowerValue.includes("*/")) continue
    if (!/^[#\w\s.,()%/+\-]+$/.test(value)) continue
    declarations.push(`${property}: ${value}`)
  }
  return declarations.join("; ")
}

function safeLink(value: string): boolean {
  const url = value.trim()
  if (!url || /[\\\u0000-\u001f]/.test(url) || url.startsWith("//")) return false
  if (url.startsWith("#") || url.startsWith("/") || url.startsWith("./") || url.startsWith("../")) return true
  if (/^[a-z][a-z\d+.-]*:/i.test(url)) return /^(https?:|mailto:|tel:)/i.test(url)
  return !url.startsWith("\\")
}

function safeImageSource(value: string): boolean {
  const src = value.trim()
  return safeLink(src) || /^data:image\/(?:png|gif|jpe?g|webp);base64,[a-z\d+/=]+$/i.test(src)
}

/**
 * 使用原生 DOMParser 对管理员编写的 HTML 做允许列表清洗：移除脚本/危险容器，
 * 限定标签、属性、链接协议和行内样式。服务端渲染时返回转义文本，确保不会直接注入未清洗 HTML。
 */
export function sanitizeHelpHtml(html: string): string {
  if (typeof DOMParser === "undefined") {
    return html.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character] ?? character)
  }

  const doc = new DOMParser().parseFromString(html, "text/html")
  const elements = Array.from(doc.body.querySelectorAll("*"))
  for (const element of elements) {
    if (!element.isConnected) continue
    if (blockedTags.has(element.tagName)) {
      element.remove()
      continue
    }
    if (!allowedTags.has(element.tagName)) {
      element.replaceWith(...Array.from(element.childNodes))
      continue
    }

    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.name.toLowerCase()
      const value = attribute.value
      if (name === "style") {
        const clean = safeStyle(value)
        if (clean) element.setAttribute("style", clean)
        else element.removeAttribute(name)
        continue
      }
      if (name.startsWith("on") || name === "srcdoc" || name === "srcset") {
        element.removeAttribute(name)
        continue
      }
      if (element.tagName === "A" && name === "href") {
        if (!safeLink(value)) element.removeAttribute(name)
        continue
      }
      if (element.tagName === "A" && name === "target") {
        if (value !== "_blank" && value !== "_self") element.removeAttribute(name)
        continue
      }
      if (element.tagName === "IMG" && name === "src") {
        if (!safeImageSource(value)) element.removeAttribute(name)
        continue
      }
      if (!globalAttributes.has(name)) element.removeAttribute(name)
    }

    if (element.tagName === "A" && element.getAttribute("target") === "_blank") {
      element.setAttribute("rel", "noopener noreferrer")
    }
    if (element.tagName === "IMG" && !element.hasAttribute("src")) element.replaceWith(...Array.from(element.childNodes))
  }
  return doc.body.innerHTML
}

/** 完整参照页模板；正文、样式和左侧导航都保存在可编辑 HTML 中。 */
export const defaultHelpDocumentContent = prepareHelpDocumentTemplate(referenceHelpHtml)

const htmlEscapes: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }

/** Station configuration is a base URL, not an OpenAI/Gemini versioned endpoint. */
function helpStationBaseURL(value: string): string {
  const input = value.trim()
  if (!input || /[\\\u0000-\u0020]/.test(input)) return ""
  try {
    const url = new URL(/^[a-z][a-z\d+.-]*:/i.test(input) ? input : `https://${input}`)
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return ""
    const path = url.pathname.replace(/\/+$/, "").replace(/(?:\/v1(?:beta|alpha)?)+$/i, "")
    return `${url.origin}${path}`
  } catch {
    return ""
  }
}

function helpStationTokens(value: string): string {
  return value
    .replace(/(?:https?:\/\/|\/\/)?(?:www\.)?beiluoxi\.(?:xyz|top)\b/gi, "{{station_url}}")
    .replace(/北洛|\bbeiluo\b/gi, "{{station_name}}")
    .replace(/https?:\/\/(\{\{\s*station_url\s*\}\})/gi, "$1")
    .replace(/(\{\{\s*station_url\s*\}\})\/v1(?:beta|alpha)?(?=\/v1(?:beta|alpha)?(?:[\/?#\s"'<>]|$))/gi, "$1")
}

/** Adapt old templates at render time; replace names, links and examples, never stored data. */
export function renderHelpTemplate(html: string, stationName: string, stationURL: string): string {
  const values: Record<string, string> = {
    station_name: stationName || "中转站",
    station_url: helpStationBaseURL(stationURL || ""),
  }
  const substitute = (value: string, code = false) => helpStationTokens(value).replace(/\{\{\s*(station_name|station_url)\s*\}\}/g, (placeholder, key: string, offset: number, source: string) => {
    const result = values[key]
    // Preserve valid quoted JSON/YAML names in the original configuration examples.
    const quotedName = code && key === "station_name" && /"[^"\n]*$/.test(source.slice(0, offset)) && /^[^"\n]*"/.test(source.slice(offset + placeholder.length))
    return quotedName ? JSON.stringify(result).slice(1, -1) : result
  })
  if (typeof DOMParser === "undefined") {
    return helpStationTokens(html).replace(/\{\{\s*(station_name|station_url)\s*\}\}/g, (_placeholder, key: string) => values[key].replace(/[&<>"']/g, (character) => htmlEscapes[character]))
  }
  const adapted = adaptHelpDocument(html)
  const doc = new DOMParser().parseFromString(adapted, "text/html")
  const walker = doc.createTreeWalker(doc.documentElement, NodeFilter.SHOW_TEXT, {
    acceptNode: (node) => node.parentElement?.closest("script,style") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
  })
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node as Text
    text.data = substitute(text.data, Boolean(text.parentElement?.closest("pre")))
  }
  for (const element of doc.querySelectorAll("*")) {
    for (const attribute of Array.from(element.attributes)) {
      if (attribute.name !== "style" && !attribute.name.startsWith("on")) element.setAttribute(attribute.name, substitute(attribute.value))
    }
  }
  return /<!doctype\s+html|<html(?:\s|>)/i.test(adapted) ? `<!doctype html>\n${doc.documentElement.outerHTML}` : doc.body.innerHTML
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
  const doc = new DOMParser().parseFromString(sanitizeHelpHtml(html), "text/html")
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
      const heading = node as HTMLElement
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

function textNodesOf(html: string): { doc: Document; nodes: Text[]; fullDocument: boolean } {
	const doc = new DOMParser().parseFromString(html, "text/html")
	const fullDocument = /<!doctype\s+html|<html(?:\s|>)/i.test(html)
	const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT, {
		acceptNode(node) {
			return node.parentElement?.closest("script,style,textarea,option") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT
		},
	})
	const nodes: Text[] = []
	let node = walker.nextNode()
	while (node) {
		nodes.push(node as Text)
		node = walker.nextNode()
	}
	return { doc, nodes, fullDocument }
}

/** 全文匹配统计：只在文本节点里查找，不会误伤标签属性或产生嵌套标签。 */
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
	const { doc, nodes, fullDocument } = textNodesOf(html)
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
	return { html: fullDocument ? `<!doctype html>\n${doc.documentElement.outerHTML}` : doc.body.innerHTML, count }

}
