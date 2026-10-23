const documentTags = new Set([
  "HTML", "HEAD", "BODY", "TITLE", "STYLE", "A", "ARTICLE", "ASIDE", "B", "BLOCKQUOTE", "BR", "BUTTON", "CAPTION", "CODE", "COL", "COLGROUP", "DD", "DEL", "DETAILS", "DIV", "DL", "DT", "EM", "FIGCAPTION", "FIGURE", "FOOTER", "H1", "H2", "H3", "H4", "H5", "H6", "HEADER", "HR", "I", "IMG", "INPUT", "INS", "KBD", "LABEL", "LI", "MAIN", "MARK", "NAV", "OL", "P", "PRE", "S", "SAMP", "SECTION", "SMALL", "SPAN", "STRONG", "SUB", "SUMMARY", "SUP", "TABLE", "TBODY", "TD", "TFOOT", "TH", "THEAD", "TIME", "TR", "U", "UL", "VAR",
])
const dangerousTags = new Set(["SCRIPT", "BASE", "LINK", "META", "IFRAME", "FRAME", "FRAMESET", "OBJECT", "EMBED", "FORM", "SVG", "MATH", "TEMPLATE", "NOSCRIPT", "VIDEO", "AUDIO"])
const documentAttributes = new Set(["alt", "align", "autocomplete", "class", "colspan", "datetime", "dir", "for", "height", "hidden", "id", "lang", "open", "placeholder", "role", "rowspan", "scope", "span", "start", "style", "tabindex", "title", "type", "width"])

function safeHref(value: string) {
  return !/[\\\u0000-\u0020]/.test(value) && (value.startsWith("#") || /^(https?:|mailto:|tel:)/i.test(value) || /^\/(?!\/)/.test(value))
}

const excludedHelpText = /画布|\bcanvas\b|留言|评论/i
const excludedHelpMarker = /(?:^|[-_\s])(?:canvas|comments?|guestbook|message-board)(?:$|[-_\s])|画布|留言|评论/i
const helpHeadingSelector = "h1,h2,h3,h4,h5,h6"

/** Delete matching text ranges without flattening the surrounding editable markup. */
function removeDocumentText(element: Element, pattern: RegExp) {
  const matches = Array.from((element.textContent || "").matchAll(pattern)).reverse()
  for (const match of matches) {
    const walker = element.ownerDocument.createTreeWalker(element, NodeFilter.SHOW_TEXT)
    const nodes: Text[] = []
    for (let node = walker.nextNode(); node; node = walker.nextNode()) nodes.push(node as Text)
    const locate = (offset: number): [Text, number] | undefined => {
      for (const node of nodes) {
        if (offset <= node.length) return [node, offset]
        offset -= node.length
      }
    }
    const start = locate(match.index!)
    const end = locate(match.index! + match[0].length)
    if (!start || !end) continue
    const range = element.ownerDocument.createRange()
    range.setStart(...start)
    range.setEnd(...end)
    range.deleteContents()
  }
}

/** Browser-saved HTML can contain extension overlays and absolute links back to the source page. */
function normalizeSavedHelpDocument(doc: Document) {
  for (const template of Array.from(doc.querySelectorAll("template[shadowrootmode]"))) {
    const wrapper = template.parentElement
    if (wrapper?.parentElement === doc.body && !wrapper.querySelector("#content")) wrapper.remove()
    else template.remove()
  }
  doc.querySelectorAll('style#_goober,[id^="immersive-translate-"]').forEach((element) => element.remove())
  for (const element of doc.querySelectorAll("*")) {
    for (const attribute of Array.from(element.attributes)) {
      if (attribute.name.startsWith("data-immersive-translate-")) element.removeAttribute(attribute.name)
    }
  }
  for (const link of doc.querySelectorAll("a[href]")) {
    const href = link.getAttribute("href") || ""
    const savedAnchor = /^(?:https?:\/\/(?:www\.)?beiluoxi\.(?:xyz|top)|\{\{\s*station_url\s*\}\})\/help\/_full\.html(?:\?[^#]*)?#(.+)$/i.exec(href)
    if (!savedAnchor || !doc.getElementById(savedAnchor[1])) continue
    link.setAttribute("href", `#${savedAnchor[1]}`)
    link.removeAttribute("target")
  }
  // These buttons were inserted by the source runtime; the isolated local runtime recreates them.
  doc.querySelectorAll("pre button.copy").forEach((button) => button.remove())
}

/** Rendering-only compatibility: never overwrite saved administrator HTML or comments. */
function removeExcludedHelpContent(doc: Document) {
  normalizeSavedHelpDocument(doc)
  const content = doc.getElementById("content") || doc.querySelector("article") || doc.body
  const removedIDs = new Set<string>()
  const remove = (element: Element) => {
    if (element.id) removedIDs.add(element.id)
    for (const child of element.querySelectorAll("[id]")) removedIDs.add(child.id)
    element.remove()
  }
  doc.querySelectorAll("script").forEach(remove)
  for (const heading of Array.from(doc.querySelectorAll(helpHeadingSelector))) {
    if (!heading.isConnected || !excludedHelpText.test(heading.textContent || "")) continue
    // The source groups protocol tables with this title; retain its non-tool API reference.
    if (/^(?:无限|无线)?画布与模型能力$/.test((heading.textContent || "").trim())) {
      const oldID = heading.id
      heading.textContent = "模型与高级能力"
      if (excludedHelpMarker.test(oldID)) heading.id = "help-model-capabilities"
      if (oldID) {
        for (const link of doc.querySelectorAll("a[href]")) {
          if (link.getAttribute("href") === `#${oldID}`) {
            link.setAttribute("href", `#${heading.id}`)
            link.textContent = heading.textContent
            link.removeAttribute("data-search")
          }
        }
      }
      continue
    }
    const level = Number(heading.tagName.slice(1))
    const parent = heading.parentElement
    if (parent && parent !== content && /^(SECTION|ARTICLE)$/.test(parent.tagName) && parent.querySelector(helpHeadingSelector) === heading && !Array.from(parent.querySelectorAll(helpHeadingSelector)).some((other) => other !== heading && Number(other.tagName.slice(1)) <= level)) {
      remove(parent)
      continue
    }
    for (let next = heading.nextElementSibling; next;) {
      if (next.matches(helpHeadingSelector) && Number(next.tagName.slice(1)) <= level) break
      const following = next.nextElementSibling
      remove(next)
      next = following
    }
    remove(heading)
  }
  for (const element of Array.from(doc.body.querySelectorAll("[id],[class]"))) {
    if (!element.isConnected || element === content || element.contains(content)) continue
    if (excludedHelpMarker.test(`${element.id} ${element.className}`) || element.classList.contains("endpoint-backup")) remove(element)
  }
  for (const link of Array.from(doc.querySelectorAll("a[href]"))) {
    const href = link.getAttribute("href") || ""
    if (excludedHelpText.test(link.textContent || "") || /(?:\/|#)(?:canvas|comments?)(?:[\/#?_-]|$)/i.test(href) || (href.startsWith("#") && removedIDs.has(href.slice(1)))) {
      remove(link.closest(".quick-card,.endpoint-item,li") || link)
    }
  }
  for (const panel of doc.querySelectorAll(".endpoint-panel")) {
    if (excludedHelpText.test(panel.textContent || "")) remove(panel)
  }
  for (const pre of doc.querySelectorAll("pre")) {
    const code = pre.querySelector("code") || pre
    const text = code.textContent || ""
    const lines = text.split("\n").filter((line) => !excludedHelpText.test(line) && !/备用|另一个本站域名|两个域名/.test(line))
    if (lines.join("\n") !== text) code.textContent = lines.join("\n")
    if (!code.textContent?.trim()) remove(pre)
  }
  for (const row of Array.from(doc.querySelectorAll("tr"))) {
    const label = row.querySelector("td")?.textContent || ""
    if (/备用地址/.test(label) || (/地址切换/.test(label) && /两个域名|备用/.test(row.textContent || ""))) remove(row)
  }
  const blocks = Array.from(doc.body.querySelectorAll("p,li,td,small,.doc-meta,span,label,button"))
  for (const div of doc.body.querySelectorAll("div")) {
    if (!div.querySelector(`${helpHeadingSelector},div,p,pre,table,section,article,ul,ol`)) blocks.push(div)
  }
  for (const block of blocks) {
    if (!block.isConnected || block.closest("pre")) continue
    if (block.tagName === "SMALL" && /^\s*备用/.test(block.textContent || "")) { remove(block); continue }
    removeDocumentText(block, /[^。！？；;\n]*(?:画布|\bcanvas\b|留言|评论)[^。！？；;\n]*(?:[。！？；;]|\n|$)/gi)
    removeDocumentText(block, /\s*[·；;]\s*备用[^。！？）)\n]*/g)
    removeDocumentText(block, /[；;]\s*(?:也可|可)?切换(?:另一个本站域名|备用域名)[^，,；;。！？\n]*/g)
    if (!block.textContent?.trim() && !block.querySelector("img")) remove(block)
  }
}

/** Adapt legacy full documents/fragments only for display; preserve inline CSS verbatim. */
export function adaptHelpDocument(html: string): string {
  if (typeof DOMParser === "undefined") return html
  const doc = new DOMParser().parseFromString(html, "text/html")
  removeExcludedHelpContent(doc)
  return /<!doctype\s+html|<html(?:\s|>)/i.test(html) ? `<!doctype html>\n${doc.documentElement.outerHTML}` : doc.body.innerHTML
}

function addNavigation(doc: Document) {
  const content = doc.getElementById("content") || doc.querySelector("article") || doc.body
  const toc = doc.getElementById("toc")
  const used = new Set(Array.from(doc.querySelectorAll("[id]")).map((element) => element.id))
  const headings = Array.from(content.querySelectorAll("h2,h3"))
  for (const [index, heading] of headings.entries()) {
    if (!heading.id) {
      let id = `doc-section-${index + 1}`
      while (used.has(id)) id += "-1"
      heading.id = id
      used.add(id)
    }
  }
  // Retain editable navigation, prune dead anchors and fill only missing heading entries.
  if (toc) {
    for (const link of Array.from(toc.querySelectorAll('a[href^="#"]'))) {
      if (!doc.getElementById(link.getAttribute("href")!.slice(1))) link.remove()
    }
    const linked = new Set(Array.from(toc.querySelectorAll("a[href]")).map((link) => link.getAttribute("href")))
    for (const heading of headings) {
      if (linked.has(`#${heading.id}`)) continue
      const link = doc.createElement("a")
      link.href = `#${heading.id}`
      link.className = heading.tagName === "H2" ? "toc-section" : "toc-sub"
      link.textContent = heading.textContent
      toc.append(link)
    }
  }
  for (const table of Array.from(content.querySelectorAll("table"))) {
    if (table.parentElement?.classList.contains("table-scroll")) continue
    const wrapper = doc.createElement("div")
    wrapper.className = "table-scroll"
    table.replaceWith(wrapper)
    wrapper.append(table)
  }
}

/** Populate the source template itself, so its sidebar is part of saved/editable HTML. */
export function prepareHelpDocumentTemplate(html: string): string {
  if (typeof DOMParser === "undefined") return html
  const doc = new DOMParser().parseFromString(html, "text/html")
  removeExcludedHelpContent(doc)
  addNavigation(doc)
  return `<!doctype html>\n${doc.documentElement.outerHTML}`
}

const runtime = String.raw`(() => {
  const content = document.getElementById('content') || document.querySelector('article') || document.body;
  const toc = document.getElementById('toc');
  const links = toc ? Array.from(toc.querySelectorAll('a[href^="#"]')) : [];
  const sections = links.map(link => {
    const heading = document.getElementById(link.getAttribute('href').slice(1));
    let text = heading ? heading.textContent : link.textContent;
    for (let node = heading && heading.nextElementSibling; node && !/^H[23]$/.test(node.tagName); node = node.nextElementSibling) text += ' ' + node.textContent;
    return { link, heading, text: (text || '').toLocaleLowerCase() };
  });
  const groups = [];
  let group;
  for (const section of sections) {
    if (!group || section.heading?.tagName === 'H2' || section.link.classList.contains('toc-section')) {
      group = { parent: section, children: [] }; groups.push(group);
    } else group.children.push(section);
  }
  const search = document.getElementById('toc-search');
  const filter = () => {
    const query = search ? search.value.trim().toLocaleLowerCase() : '';
    let count = 0;
    const show = (section, visible) => {
      section.link.hidden = !visible;
      section.link.style.display = visible ? '' : 'none';
      if (visible) count++;
    };
    for (const group of groups) {
      const parentMatches = !query || group.parent.text.includes(query);
      show(group.parent, parentMatches || group.children.some(child => child.text.includes(query)));
      for (const child of group.children) show(child, parentMatches || child.text.includes(query));
    }
    const empty = document.getElementById('toc-empty');
    if (empty) empty.hidden = count > 0;
  };
  if (search) {
    search.addEventListener('input', filter);
    search.addEventListener('keydown', event => {
      if (event.key === 'Escape') { search.value = ''; filter(); search.blur(); }
    });
  }
  filter();
  let previousActive;
  const updateActive = () => {
    let active = sections.find(section => section.heading);
    for (const section of sections) if (section.heading && section.heading.getBoundingClientRect().top <= 132) active = section;
    const scrolling = document.scrollingElement;
    if (scrolling && scrolling.scrollTop > 0 && scrolling.scrollTop + window.innerHeight >= scrolling.scrollHeight - 2) {
      active = [...sections].reverse().find(section => section.heading) || active;
    }
    for (const section of sections) {
      section.link.classList.toggle('active', section === active);
      if (section === active) section.link.setAttribute('aria-current', 'location');
      else section.link.removeAttribute('aria-current');
    }
    if (toc && active && active !== previousActive && !active.link.hidden) {
      const bounds = toc.getBoundingClientRect();
      const linkBounds = active.link.getBoundingClientRect();
      if (linkBounds.top < bounds.top) toc.scrollTop += linkBounds.top - bounds.top;
      else if (linkBounds.bottom > bounds.bottom) toc.scrollTop += linkBounds.bottom - bounds.bottom;
    }
    previousActive = active;
  };
  document.addEventListener('scroll', updateActive, { passive: true });
  updateActive();
  // srcDoc inherits the outer page's base URL; native fragment navigation would reload that page inside the frame.
  for (const link of document.querySelectorAll('a[href^="#"]')) {
    link.addEventListener('click', event => {
      const target = document.getElementById(link.getAttribute('href').slice(1));
      if (!target) return;
      event.preventDefault();
      target.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' });
    });
  }
  for (const pre of content.querySelectorAll('pre')) {
    pre.querySelectorAll('button.copy').forEach(button => button.remove());
    const button = document.createElement('button');
    button.type = 'button'; button.className = 'copy'; button.textContent = '复制';
    button.setAttribute('aria-label', '复制代码');
    button.addEventListener('click', () => {
      const code = pre.querySelector('code');
      const text = code ? code.textContent : Array.from(pre.childNodes).filter(node => node !== button).map(node => node.textContent).join('');
      const area = document.createElement('textarea');
      area.value = text || ''; area.style.cssText = 'position:fixed;top:0;left:-9999px';
      document.body.append(area); area.select();
      let copied = false;
      try { copied = document.execCommand('copy'); } catch (_) {}
      area.remove(); button.textContent = copied ? '已复制' : '请选择代码复制';
      setTimeout(() => button.textContent = '复制', 1600);
    });
    pre.append(button);
  }
})();`

/** Complete HTML stays isolated; only the local, nonce-authorized UI runtime executes. */
export function buildHelpDocument(html: string, transparentBackground = false, hideDocumentHeader = false): string {
  if (typeof DOMParser === "undefined") return ""
  const doc = new DOMParser().parseFromString(html, "text/html")
  removeExcludedHelpContent(doc)
  for (const element of Array.from(doc.querySelectorAll("*"))) {
    if (!element.isConnected) continue
    if (dangerousTags.has(element.tagName)) { element.remove(); continue }
    if (!documentTags.has(element.tagName)) { element.replaceWith(...Array.from(element.childNodes)); continue }
    if (element.tagName === "INPUT" && !["search", "text"].includes(element.getAttribute("type") || "text")) { element.remove(); continue }
    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.name.toLowerCase()
      if (name === "href" && element.tagName === "A") {
        if (!safeHref(attribute.value)) element.removeAttribute(name)
      } else if (name === "src" && element.tagName === "IMG") {
        if (!/^data:image\/(png|gif|jpe?g|webp);base64,[a-z\d+/=]+$/i.test(attribute.value)) element.removeAttribute(name)
      } else if (!documentAttributes.has(name) && !name.startsWith("aria-") && !name.startsWith("data-")) {
        element.removeAttribute(name)
      }
    }
    if (element.tagName === "A" && !element.getAttribute("href")?.startsWith("#")) {
      element.setAttribute("target", "_blank")
      element.setAttribute("rel", "noopener noreferrer")
    }
  }
  if (transparentBackground) doc.documentElement.dataset.transparentPage = "true"
  addNavigation(doc)
  const nonce = Array.from(crypto.getRandomValues(new Uint8Array(18)), (value) => value.toString(16).padStart(2, "0")).join("")
  const charset = doc.createElement("meta")
  charset.setAttribute("charset", "utf-8")
  const viewport = doc.createElement("meta")
  viewport.name = "viewport"
  viewport.content = "width=device-width, initial-scale=1"
  const csp = doc.createElement("meta")
  csp.httpEquiv = "Content-Security-Policy"
  csp.content = `default-src 'none'; script-src 'nonce-${nonce}'; script-src-attr 'none'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'`
  doc.head.prepend(charset, viewport, csp)
  const fallback = doc.createElement("style")
  fallback.textContent = `html{scroll-behavior:smooth}body{overflow-wrap:anywhere}pre{position:relative}.copy{position:absolute;top:9px;right:9px;padding:3px 9px;border:1px solid #cbd5e1;border-radius:5px;background:white;color:#475569;cursor:pointer}:focus-visible{outline:2px solid #087f77;outline-offset:3px}@media(prefers-reduced-motion:reduce){html{scroll-behavior:auto}}`
  if (hideDocumentHeader) {
    doc.documentElement.dataset.embeddedHelp = "true"
    fallback.textContent += `html[data-embedded-help] body > header{display:none}html[data-embedded-help] .sidebar{top:24px;height:calc(100dvh - 48px)}html[data-embedded-help] h2,html[data-embedded-help] h3{scroll-margin-top:24px}@media(max-width:760px){html[data-embedded-help] .sidebar{height:auto}}`
  }
  if (transparentBackground) {
    fallback.textContent += `
      html[data-transparent-page], html[data-transparent-page] body { background: transparent !important; }
      html[data-transparent-page] body { margin-inline: 0; padding-inline: 0; }
      html[data-transparent-page] main { box-sizing: border-box; width: 100%; max-width: none; min-width: 0; margin-inline: 0; padding-inline: 0; }
      html[data-transparent-page] article { min-width: 0; max-width: none; }
      html[data-transparent-page] main:has(> .sidebar):has(> article) { grid-template-columns: minmax(13rem,17rem) minmax(0,1fr); justify-content: stretch; gap: clamp(1rem,2vw,2rem); }
      @media(max-width:760px) { html[data-transparent-page] main:has(> .sidebar):has(> article) { display: block; } }
    `
  }
  doc.head.append(fallback)
  const script = doc.createElement("script")
  script.setAttribute("nonce", nonce)
  script.textContent = runtime
  doc.body.append(script)
  return `<!doctype html>\n${doc.documentElement.outerHTML}`
}

export function downloadHelpHtml(html: string, name = "帮助文档") {
  const url = URL.createObjectURL(new Blob([buildHelpDocument(html)], { type: "text/html;charset=utf-8" }))
  const link = document.createElement("a")
  link.href = url
  link.download = `${name.replace(/[\\/:*?"<>|]/g, "-")}.html`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
