// node --test frontend/lib/help-document.test.mjs
// One-time isolated setup when needed (no production dependency/lockfile changes):
// npm install --prefix ./frontend/node_modules/.cache/help-document-validation --no-save --no-package-lock --ignore-scripts jsdom@26.1.0
// Set HELP_TEST_JSDOM to an existing jsdom entry if it is not installed locally.
import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { readFile } from "node:fs/promises"
import { createRequire } from "node:module"
import { after, test } from "node:test"
import vm from "node:vm"

const require = createRequire(import.meta.url)
const ts = require("typescript")
const { JSDOM } = require(process.env.HELP_TEST_JSDOM || "../node_modules/.cache/help-document-validation/node_modules/jsdom")
const host = new JSDOM("<!doctype html><html><body></body></html>")
const context = vm.createContext({
  DOMParser: host.window.DOMParser,
  document: host.window.document,
  HTMLElement: host.window.HTMLElement,
  NodeFilter: host.window.NodeFilter,
  crypto: globalThis.crypto,
  URL,
  Blob,
  setTimeout,
})
const reference = await readFile(new URL("./help-reference.html", import.meta.url), "utf8")
const documentSource = await readFile(new URL("./help-document.ts", import.meta.url), "utf8")
const htmlSource = await readFile(new URL("./help-html.ts", import.meta.url), "utf8")

function compile(source, filename, imports = {}) {
  const exports = {}
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const load = (name) => {
    if (name in imports) return imports[name]
    throw new Error(`Unexpected test import: ${name}`)
  }
  const wrapper = vm.runInContext(`(function(exports, require) { ${output}\n})`, context, { filename })
  wrapper(exports, load)
  return exports
}

const helpDocument = compile(documentSource, "help-document.ts")
const helpHtml = compile(htmlSource, "help-html.ts", {
  "./help-document": helpDocument,
  "./help-reference.html?raw": { default: reference },
})
const parse = (html) => new host.window.DOMParser().parseFromString(html, "text/html")
const sha256 = (value) => createHash("sha256").update(value).digest("hex")
const originalCSSHash = "8e790a8e175e02fceafe81bcfe6acb26ed59af21bc04117f446b94b6455527da"

function assertNoRemovedFeatures(doc) {
  assert.doesNotMatch(doc.body.textContent, /画布|canvas|留言|评论|beiluoxi\.(?:xyz|top)|备用/i)
  assert.equal(doc.querySelectorAll('[href*="canvas"],[href*="comment"],.comments,.quick-card-canvas,.endpoint-canvas').length, 0)
}

function withLocalRuntime(html, run) {
  const source = helpDocument.buildHelpDocument(html)
  const dom = new JSDOM(source, { runScripts: "outside-only" })
  try {
    const script = dom.window.document.querySelector("script[nonce]")
    assert.ok(script)
    // Execute only the audited local runtime, never source/downloaded HTML scripts.
    assert.equal(script.textContent, parse(source).querySelector("script").textContent)
    dom.window.eval(script.textContent)
    run(dom.window)
  } finally {
    dom.window.close()
  }
}

test("full reference preserves the complete original inline CSS, layout and protocol/tool documentation", () => {
  const doc = parse(reference)
  assert.equal(sha256(doc.querySelector("style").textContent), originalCSSHash)
  assert.match(doc.querySelector("style").textContent, /@media \(max-width: 760px\)/)
  assert.match(doc.querySelector("style").textContent, /\.sidebar \{ position: sticky/)
  assert.match(doc.querySelector("style").textContent, /nav \{[^}]*overflow-y: auto/)
  assert.equal(doc.querySelectorAll("script").length, 0)
  assert.equal(doc.querySelectorAll("table").length, 16)
  const rendered = parse(helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, "测试站", "https://relay.example"))
  assertNoRemovedFeatures(rendered)
  for (const label of ["快速开始", "API 接入", "接口与高级能力", "Continue 示例", "OpenCode 示例", "Claude Code / Anthropic", "Gemini 原生与 Antigravity", "CC Switch（CCS）", "故障排查", "安全与验收"]) {
    assert.ok(rendered.body.textContent.includes(label), label)
  }
  assert.equal(sha256(rendered.querySelector("style").textContent), originalCSSHash)
  assert.ok(rendered.querySelectorAll("#toc a").length > 20)
})

test("browser-saved HTML retains source sections while removing extension artifacts and normalizing TOC links", () => {
  const saved = `<!doctype html><html lang="zh-CN" data-immersive-translate-page-theme="light"><head>
    <style>${parse(reference).querySelector("style").textContent}</style><style id="_goober">.extension-overlay{position:fixed}</style>
    </head><body><header>北洛 AI</header><main><aside class="sidebar"><nav id="toc">
    <a href="https://beiluoxi.xyz/help/_full.html#section-0" class="toc-section">快速开始</a>
    <a href="https://beiluoxi.xyz/help/_full.html#section-2" class="toc-section" data-search="无限画布与模型能力">无限画布与模型能力</a>
    <a href="https://beiluoxi.xyz/help/_full.html#section-2-topic-0" class="toc-sub">接口与高级能力</a>
    <a href="https://beiluoxi.xyz/help/_full.html#section-2-topic-1" class="toc-sub">无限画布设置</a></nav></aside>
    <article id="content"><h2 id="section-0">快速开始</h2><p>保留 API 接入。</p>
    <pre><code>curl https://beiluoxi.xyz/v1/models</code><button class="copy">复制</button></pre>
    <h2 id="section-2">无限画布与模型能力</h2><h3 id="section-2-topic-0">接口与高级能力</h3>
    <table><tbody><tr><td>图片</td><td>/v1/images/generations</td></tr></tbody></table>
    <h3 id="section-2-topic-1">无限画布设置</h3><p>移除此工具。</p><h2 id="section-3">安全</h2>
    <p>保留安全提示；无限画布请使用独立 Key；保留截图提醒。</p></article></main>
    <script src="./北洛 AI 接入文档_files/help.js.下载"></script>
    <div style="all:initial"><template shadowrootmode="open"><div>浏览器扩展弹层</div></template></div>
    <div id="immersive-translate-browser-popup">扩展内容</div></body></html>`
  const prepared = parse(helpDocument.prepareHelpDocumentTemplate(saved))
  assert.equal(sha256(prepared.querySelector("style").textContent), originalCSSHash)
  assert.equal(prepared.querySelectorAll("style").length, 1)
  assert.equal(prepared.querySelectorAll("script,template,style#_goober,[id^='immersive-translate-']").length, 0)
  assert.equal(prepared.documentElement.hasAttribute("data-immersive-translate-page-theme"), false)
  assert.equal(prepared.querySelectorAll("button.copy").length, 0)
  assert.equal(prepared.querySelector('#toc a[href="#section-0"]').textContent, "快速开始")
  assert.equal(prepared.querySelector('#toc a[href="#section-2"]').textContent, "模型与高级能力")
  assert.ok(prepared.getElementById("section-2-topic-0"))
  assert.equal(prepared.getElementById("section-2-topic-1"), null)
  assert.match(prepared.body.textContent, /保留安全提示/)
  assert.match(prepared.body.textContent, /保留截图提醒/)
  assert.doesNotMatch(prepared.body.textContent, /画布|浏览器扩展弹层|扩展内容|移除此工具/)
  const rendered = parse(helpDocument.buildHelpDocument(helpHtml.renderHelpTemplate(saved, "上传站", "https://relay.example")))
  assertNoRemovedFeatures(rendered)
  assert.equal(rendered.querySelectorAll("#toc a").length, rendered.querySelectorAll("#content h2,#content h3").length)
  for (const link of rendered.querySelectorAll('#toc a[href^="#"]')) assert.ok(rendered.getElementById(link.getAttribute("href").slice(1)))
  assert.match(rendered.querySelector("pre code").textContent, /https:\/\/relay\.example\/v1\/models/)
  assert.equal(rendered.querySelectorAll("script").length, 1)
})

test("uploaded template preserves original section IDs and a complete local TOC", () => {
  const doc = parse(reference)
  assert.equal(doc.querySelectorAll("#toc a").length, 25)
  assert.equal(doc.querySelectorAll("#content h2,#content h3").length, 25)
  for (const link of doc.querySelectorAll("#toc a")) {
    const href = link.getAttribute("href")
    assert.ok(href.startsWith("#"))
    assert.ok(doc.getElementById(href.slice(1)))
  }
  assert.equal(doc.getElementById("section-0-topic-0").textContent, "四步完成首次接入")
  assert.equal(doc.getElementById("section-4-topic-1").textContent, "接入完成检查")
  assert.doesNotMatch(doc.documentElement.outerHTML, /immersive-translate|_goober|shadowrootmode|北洛 AI 接入文档_files/)
})

test("station URLs retain protocol suffixes without duplicated schemes/version paths", () => {
  for (const input of ["https://relay.example/proxy/", "https://relay.example/proxy/v1/", "https://relay.example/proxy/v1beta", "relay.example/proxy/v1"]) {
    const doc = parse(helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, "新中转站", input))
    const links = Array.from(doc.querySelectorAll("a[href]")).map((a) => a.getAttribute("href"))
    for (const suffix of ["", "/v1", "/v1beta", "/antigravity/v1", "/antigravity/v1beta"]) {
      assert.ok(links.includes(`https://relay.example/proxy${suffix}`), `${input}: ${suffix}`)
    }
    assert.match(doc.body.textContent, /curl https:\/\/relay\.example\/proxy\/v1\/models/)
    assert.match(doc.body.textContent, /ANTHROPIC_BASE_URL="https:\/\/relay\.example\/proxy"/)
    assert.match(doc.body.textContent, /https:\/\/relay\.example\/proxy\/v1beta\/models\/MODEL:generateContent/)
    assert.doesNotMatch(doc.documentElement.outerHTML, /https:\/\/https?:\/\/|relay\.example\/proxy\/v1\/v1/)
    assertNoRemovedFeatures(doc)
  }
  const old = parse(helpHtml.renderHelpTemplate('<a href="https://{{ station_url }}/v1/v1/models">https://beiluoxi.top/v1beta</a><p>北洛 Beiluo beiluo beiluoxi.xyz</p>', "配置站", "https://relay.example/v1/"))
  assert.equal(old.querySelector("a").getAttribute("href"), "https://relay.example/v1/models")
  assert.equal(old.querySelector("a").textContent, "https://relay.example/v1beta")
  assert.doesNotMatch(old.body.textContent, /北洛|beiluo/i)
})

test("replacement is HTML-safe and original JSON/YAML examples remain correctly quoted", () => {
  const name = '测试 "站" <img src=x> & \\ 本站'
  const doc = parse(helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, name, "https://relay.example/v1/?secret=no#ignored"))
  assert.equal(doc.querySelector("h1").textContent, `${name} AI 接入文档`)
  assert.equal(doc.querySelectorAll("img").length, 0)
  const codes = Array.from(doc.querySelectorAll("pre code")).map((code) => code.textContent)
  const config = JSON.parse(codes.find((code) => code.includes('"$schema"')))
  assert.equal(config.provider.relay.name, `${name} OpenAI Compatible`)
  assert.equal(config.provider.relay.options.baseURL, "https://relay.example/v1")
  assert.ok(codes.some((code) => code.includes(`name: ${JSON.stringify(name)}`)))
  assert.doesNotMatch(doc.body.textContent, /secret=no|#ignored/)
  for (const url of ["javascript:alert(1)", "data:text/html,bad", "https://user:pass@relay.example", "https://relay.example\\bad", ""]) {
    const result = parse(helpDocument.buildHelpDocument(helpHtml.renderHelpTemplate('<a href="{{station_url}}">北洛</a>', name, url)))
    assert.equal(result.querySelector("a").hasAttribute("href"), false)
    assert.equal(result.querySelector("a").textContent, name)
  }
})

test("legacy full HTML and reconstruction fragments lose canvas/comments only at render time", () => {
  const legacy = `<!doctype html><html><head><style>${parse(reference).querySelector("style").textContent}</style></head><body>
    <aside class="sidebar"><nav id="toc"><a href="#start">自定义快速目录</a><a href="#canvas-capabilities">无限画布与模型能力</a><a href="#canvas-settings">画布设置</a><a href="#comments">留言</a></nav></aside>
    <article id="content"><h2 id="start">快速开始</h2><p>保留 API 接入。画布、图片能力在后续章节。</p>
    <div class="quick-card quick-card-canvas"><a href="https://beiluoxi.xyz/canvas/">打开画布</a></div>
    <div class="endpoint-panel"><strong>画布页面地址和 API 地址不是一回事</strong><div class="endpoint-item endpoint-primary"><code>https://beiluoxi.xyz</code></div></div>
    <div class="doc-meta">推荐地址 beiluoxi.xyz · 备用地址 beiluoxi.top</div>
    <div class="quick-card"><code>https://beiluoxi.xyz/v1</code><small>备用：<code>https://beiluoxi.top/v1</code></small></div>
    <h2 id="canvas-capabilities">无限画布与模型能力</h2><h3>接口与高级能力</h3><table><tbody><tr><td>图片</td><td>/v1/images/generations</td></tr><tr><td>视频</td><td>无限画布暂不开放</td></tr></tbody></table>
    <h3 id="canvas-settings">无限画布：地址与配置</h3><p>canvas-section-body</p><h3>无限画布：视频生成</h3><p>canvas-video-body</p>
    <h2 id="errors">故障排查</h2><p>保留故障排查。</p><pre><code>[ ] OpenAI https://beiluoxi.xyz/v1\n[ ] 备用地址 beiluoxi.top\n[ ] 无限画布设置</code></pre>
    <table><tbody><tr><td>备用地址</td><td>https://beiluoxi.top</td></tr><tr><td>地址切换</td><td>两个域名接入同一套北洛服务</td></tr></tbody></table>
    </article><aside class="comments"><h2>留言</h2><p>comment-body</p></aside></body></html>`
  const saved = legacy
  const rendered = parse(helpDocument.buildHelpDocument(helpHtml.renderHelpTemplate(legacy, "兼容站", "https://relay.example")))
  assert.equal(legacy, saved)
  assertNoRemovedFeatures(rendered)
  assert.doesNotMatch(rendered.body.textContent, /canvas-section-body|canvas-video-body|comment-body/)
  assert.match(rendered.body.textContent, /\/v1\/images\/generations/)
  assert.match(rendered.body.textContent, /保留故障排查/)
  assert.equal(sha256(rendered.querySelector("style").textContent), originalCSSHash)
  assert.equal(rendered.querySelector('#toc a[href="#start"]').textContent, "自定义快速目录")
  for (const link of rendered.querySelectorAll('#toc a[href^="#"]')) assert.ok(rendered.getElementById(link.getAttribute("href").slice(1)))
  const fragment = '<section><h2>快速开始</h2><p>保留片段</p></section><section><h2>无线画布</h2><p>old-tool-body</p></section><section><h2>留言</h2><p>old-message-body</p></section>'
  const adapted = helpDocument.adaptHelpDocument(fragment)
  assert.doesNotMatch(adapted, /old-tool-body|old-message-body|画布|留言/)
  assert.match(adapted, /保留片段/)
  assert.ok(!adapted.startsWith("<!doctype"))
  const legacyCard = helpDocument.adaptHelpDocument('<h2>接入提示</h2><div class="callout"><strong>独立接入</strong><span>无限画布说明</span></div>')
  assert.match(legacyCard, /独立接入/)
  assert.doesNotMatch(legacyCard, /画布/)
})

test("iframe document authorizes only a nonce-bound local runtime and preserves inline CSS", () => {
  const supplied = '<!doctype html><html><head><style>.source { color: red; } @import "https://invalid.example/style";</style><meta http-equiv="refresh" content="0; url=https://invalid.example"><base href="https://invalid.example"></head><body onload="untrusted-handler"><script src="https://invalid.example/remote"></script><script>untrusted-script</script><iframe src="https://invalid.example/frame"></iframe><form action="https://invalid.example"><input name="token"></form><a href="javascript:untrusted" onclick="untrusted-click">bad link</a><img src="https://invalid.example/image" onerror="untrusted-error"><p style="color:blue">preserved</p></body></html>'
  const source = helpDocument.buildHelpDocument(supplied)
  const doc = parse(source)
  assert.equal(doc.querySelector("style").textContent, parse(supplied).querySelector("style").textContent)
  assert.equal(doc.querySelector("p").getAttribute("style"), "color:blue")
  assert.equal(doc.querySelectorAll("script").length, 1)
  assert.equal(doc.querySelectorAll("script[src],base,link,iframe,form").length, 0)
  assert.equal(doc.querySelectorAll("[onload],[onclick],[onerror],img[src],a[href]").length, 0)
  assert.doesNotMatch(source, /untrusted-script|untrusted-handler|untrusted-click|untrusted-error/)
  const script = doc.querySelector("script")
  assert.match(script.getAttribute("nonce"), /^[a-f\d]{36}$/)
  const csp = doc.querySelector('meta[http-equiv="Content-Security-Policy"]').getAttribute("content")
  assert.ok(csp.includes(`script-src 'nonce-${script.getAttribute("nonce")}'`))
  for (const rule of ["default-src 'none'", "script-src-attr 'none'", "connect-src 'none'", "form-action 'none'", "base-uri 'none'", "img-src data:"]) assert.ok(csp.includes(rule))
  assert.ok(!csp.includes("script-src 'unsafe-inline'"))
  assert.notEqual(parse(helpDocument.buildHelpDocument(supplied)).querySelector("script").getAttribute("nonce"), script.getAttribute("nonce"))
})

test("local TOC runtime supports content search, parent grouping, empty state and Escape reset", () => {
  withLocalRuntime(helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, "运行站", "https://relay.example"), (window) => {
    const doc = window.document
    const search = doc.getElementById("toc-search")
    const empty = doc.getElementById("toc-empty")
    search.value = "ANTHROPIC_AUTH_TOKEN"
    search.dispatchEvent(new window.Event("input"))
    const visible = Array.from(doc.querySelectorAll("#toc a")).filter((link) => !link.hidden)
    assert.ok(visible.some((link) => link.textContent === "API 接入"))
    assert.ok(visible.some((link) => link.textContent === "Claude Code / Anthropic"))
    assert.ok(empty.hidden)
    search.value = "__no_matches__"
    search.dispatchEvent(new window.Event("input"))
    assert.equal(empty.hidden, false)
    search.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape" }))
    assert.equal(search.value, "")
    assert.ok(empty.hidden)
    assert.ok(Array.from(doc.querySelectorAll("#toc a")).every((link) => !link.hidden))
    assert.equal(doc.querySelectorAll('#toc a[aria-current="location"]').length, 1)
    const links = Array.from(doc.querySelectorAll('#toc a[href^="#"]'))
    const selected = links.find((link) => link.textContent === "Claude Code / Anthropic")
    for (const link of links) doc.getElementById(link.getAttribute("href").slice(1)).getBoundingClientRect = () => ({ top: link === selected ? 80 : 500 })
    const toc = doc.getElementById("toc")
    toc.getBoundingClientRect = () => ({ top: 0, bottom: 100 })
    selected.getBoundingClientRect = () => ({ top: 300, bottom: 320 })
    toc.scrollTop = 0
    doc.dispatchEvent(new window.Event("scroll"))
    assert.equal(selected.getAttribute("aria-current"), "location")
    assert.equal(toc.scrollTop, 220)
    Object.defineProperty(doc, "scrollingElement", { value: { scrollTop: 2000, scrollHeight: 2000 + window.innerHeight }, configurable: true })
    doc.dispatchEvent(new window.Event("scroll"))
    assert.equal(links.at(-1).getAttribute("aria-current"), "location")
  })
})

test("local fragment links scroll within srcDoc without navigating to the outer page", () => {
  withLocalRuntime(helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, "目录站", "https://relay.example"), (window) => {
    const doc = window.document
    const link = doc.querySelector('#toc a[href^="#"]')
    const target = doc.getElementById(link.getAttribute("href").slice(1))
    let scrollOptions
    target.scrollIntoView = (options) => { scrollOptions = options }
    window.matchMedia = () => ({ matches: true })
    const event = new window.MouseEvent("click", { bubbles: true, cancelable: true })
    link.dispatchEvent(event)
    assert.equal(event.defaultPrevented, true)
    assert.equal(scrollOptions.behavior, "auto")
    assert.equal(scrollOptions.block, "start")
    assert.equal(window.location.href, "about:blank")
  })
})

test("local copy runtime copies configured code text, never executes examples", () => {
  withLocalRuntime(helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, "复制站", "https://relay.example"), (window) => {
    const doc = window.document
    const pres = doc.querySelectorAll("pre")
    assert.equal(doc.querySelectorAll("button.copy").length, pres.length)
    const pre = Array.from(pres).find((element) => element.textContent.includes("curl https://relay.example/v1/models"))
    let copied
    doc.execCommand = (command) => {
      assert.equal(command, "copy")
      copied = doc.querySelector("textarea").value
      return true
    }
    pre.querySelector("button.copy").click()
    assert.equal(copied, pre.querySelector("code").textContent)
    assert.match(copied, /https:\/\/relay\.example\/v1\/models/)
    assert.equal(doc.querySelectorAll("textarea").length, 0)
    assert.equal(pre.querySelector("button.copy").textContent, "已复制")
  })
})

test("find/replace retains complete document head, CSS, navigation and editable source", () => {
  const before = helpHtml.defaultHelpDocumentContent
  const expected = helpHtml.countHtmlText(before, "API Key")
  assert.ok(expected > 0)
  const result = helpHtml.replaceHtmlText(before, "API Key", "独立密钥")
  assert.equal(result.count, expected)
  assert.ok(result.html.startsWith("<!doctype html>"))
  const doc = parse(result.html)
  assert.equal(sha256(doc.querySelector("style").textContent), originalCSSHash)
  assert.ok(doc.querySelector("head title"))
  assert.ok(doc.querySelectorAll("#toc a").length > 20)
  assert.equal(helpHtml.countHtmlText(result.html, "API Key"), 0)
  assert.match(doc.body.textContent, /独立密钥/)
})

test("public help uses the shared header without a second page header or comment APIs; its frame stays isolated", async () => {
  const page = await readFile(new URL("../app/public-help-page.tsx", import.meta.url), "utf8")
  const frame = await readFile(new URL("../components/ui/rich-text-editor.tsx", import.meta.url), "utf8")
  assert.doesNotMatch(page, /<header|<aside|Textarea|MessageSquare|PublicHelpCommenter|\/help\/(?:comments|commenter)|data\??\.comments/)
  assert.match(page, /<HelpDocumentFrame/)
  assert.match(page, /<PublicPageHeader/)
  assert.match(page, /flex h-dvh min-w-0 flex-col overflow-hidden/)
  assert.match(page, /hideDocumentHeader/)
  assert.doesNotMatch(page, /transparentBackground/)
  const sandbox = frame.match(/sandbox="([^"]+)"/)[1].split(/\s+/)
  assert.ok(sandbox.includes("allow-scripts"))
  assert.ok(!sandbox.includes("allow-same-origin"))
  assert.match(frame, /referrerPolicy="no-referrer"/)
})

test("hiding the source header is public-page-only and preserves the saved document and original CSS", () => {
  const source = helpHtml.renderHelpTemplate(helpHtml.defaultHelpDocumentContent, "共享页头站", "https://relay.example")
  const embedded = parse(helpDocument.buildHelpDocument(source, false, true))
  const standalone = parse(helpDocument.buildHelpDocument(source))
  assert.equal(embedded.documentElement.dataset.embeddedHelp, "true")
  assert.equal(standalone.documentElement.hasAttribute("data-embedded-help"), false)
  assert.ok(embedded.querySelector("body > header"))
  assert.ok(standalone.querySelector("body > header"))
  assert.equal(sha256(embedded.querySelector("style").textContent), originalCSSHash)
  assert.equal(sha256(standalone.querySelector("style").textContent), originalCSSHash)
  assert.match(embedded.head.lastElementChild.textContent, /body > header\{display:none\}/)
  assert.doesNotMatch(standalone.documentElement.outerHTML, /data-embedded-help/)
  assert.equal(embedded.querySelectorAll("#toc a").length, standalone.querySelectorAll("#toc a").length)
})

after(() => host.window.close())
