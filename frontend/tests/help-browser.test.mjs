// Complete HTML help-page QA. No real APIs, clipboard writes or stored documents are used.
import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { createHash } from "node:crypto"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE_PATH
  ? pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH).href
  : pathToFileURL(requireProject.resolve("playwright")).href)
const port = Number(process.env.HELP_PAGES_TEST_PORT || 3040)
const server = await createServer({ root, configFile: `${root}/vite.config.ts`, server: { host: "127.0.0.1", port, strictPort: true } })
const source = await readFile(`${root}/lib/help-reference.html`, "utf8")
const sourceCSS = source.match(/<style>([\s\S]*?)<\/style>/)[1]
const cssHash = createHash("sha256").update(sourceCSS).digest("hex")
// Optional uploaded source is tested before its user-authorized deletion; tests remain runnable afterwards.
const uploadedSource = process.env.HELP_SOURCE_HTML ? await readFile(process.env.HELP_SOURCE_HTML, "utf8") : null
let content = source
const calls = []
const errors = []
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 } })
  await context.route("**/api/**", async (route) => {
    const url = new URL(route.request().url())
    calls.push(url.pathname)
    if (url.pathname === "/api/public/relay-stations/1/help") return route.fulfill({ json: { data: { id: 1, title: "测试站接入文档", station_name: "测试站", station_url: "https://relay.example/proxy/v1/", content, updated_at: new Date().toISOString(), comments: [] } } })
    return route.fulfill({ status: 501, json: { error: `Unexpected mocked endpoint: ${url.pathname}` } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  const cases = [source, source.replaceAll("{{station_name}}", "北洛").replaceAll("{{station_url}}", "https://beiluoxi.xyz"), ...(uploadedSource ? [uploadedSource] : [])]
  for (const [index, html] of cases.entries()) {
    content = html
    await page.goto(`http://127.0.0.1:${port}/public/help/1`)
    const frame = page.frameLocator("iframe")
    await frame.getByRole("heading", { name: "测试站 AI 接入文档", exact: true }).waitFor()
    const iframe = page.locator("iframe")
    assert.ok(!(await iframe.getAttribute("sandbox")).includes("allow-same-origin"))
    const bounds = await iframe.boundingBox()
    assert.equal(bounds.x, 24)
    const headerBounds = await page.locator("[data-public-page-header]").boundingBox()
    assert.equal(bounds.y, headerBounds.y + headerBounds.height + 12)
    assert.equal(bounds.width, 1440 - 48)
    assert.equal(bounds.y + bounds.height, 960 - 24)
    assert.ok(await frame.locator("body > header").isHidden())
    const summary = await frame.locator("body").evaluate((body) => ({ text: body.innerText, tables: body.querySelectorAll("table").length, css: body.ownerDocument.querySelector("style").textContent, links: [...body.querySelectorAll("a[href]")].map((link) => link.getAttribute("href")), scriptCount: body.querySelectorAll("script").length, layout: getComputedStyle(body.querySelector("main")).gridTemplateColumns }))
    assert.doesNotMatch(summary.text, /北洛|Beiluo|beiluoxi\.(xyz|top)|画布|canvas|留言|评论|备用/i)
    assert.equal(summary.tables, 16)
    assert.equal(createHash("sha256").update(summary.css).digest("hex"), cssHash)
    assert.equal(summary.scriptCount, 1)
    assert.ok(summary.links.includes("https://relay.example/proxy/v1"))
    assert.ok(summary.links.includes("https://relay.example/proxy/v1beta"))
    assert.ok(summary.layout.split(" ").length >= 2)
    const search = frame.getByRole("searchbox")
    await search.fill("ANTHROPIC_AUTH_TOKEN")
    const visible = await frame.locator("#toc a:visible").allTextContents()
    assert.ok(visible.includes("API 接入"))
    assert.ok(visible.includes("Claude Code / Anthropic"))
    assert.ok(!visible.includes("安全与验收"))
    await search.fill("__nothing_matches__")
    assert.ok(await frame.locator("#toc-empty").isVisible())
    await search.press("Escape")
    assert.equal(await search.inputValue(), "")
    assert.ok((await frame.locator("#toc a:visible").count()) > 20)
    const lastLink = frame.locator("#toc a").filter({ hasText: "接入完成检查" })
    await lastLink.click()
    await frame.getByRole("heading", { name: "接入完成检查", exact: true }).waitFor()
    const helpFrame = page.frames().find((candidate) => candidate !== page.mainFrame())
    await helpFrame.waitForFunction(() => window.scrollY > 1000)
    assert.equal(helpFrame.url(), "about:srcdoc")
    await helpFrame.waitForFunction(() => document.querySelector('#toc a[aria-current="location"]')?.textContent === "接入完成检查", undefined, { timeout: 5000 })
    await helpFrame.evaluate(() => { window.scrollTo(0, 0); document.documentElement.style.scrollBehavior = "auto"; document.execCommand = (command) => { window.__copied = command === "copy" ? document.querySelector("textarea")?.value : null; return command === "copy" } })
    const pre = frame.locator("pre").filter({ hasText: "curl https://relay.example/proxy/v1/models" }).first()
    await pre.getByRole("button", { name: "复制代码", exact: true }).click()
    assert.equal(await pre.getByRole("button").innerText(), "已复制")
    assert.match(await helpFrame.evaluate(() => window.__copied), /curl https:\/\/relay\.example\/proxy\/v1\/models/)
    if (index === 0 && process.env.BROWSER_ARTIFACT_DIR) await page.screenshot({ path: `${process.env.BROWSER_ARTIFACT_DIR}/help-desktop.png`, fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  const frame = page.frameLocator("iframe")
  const bounds = await page.locator("iframe").boundingBox()
  assert.equal(bounds.x, 24)
  const headerBounds = await page.locator("[data-public-page-header]").boundingBox()
  assert.equal(bounds.y, headerBounds.y + headerBounds.height + 12)
  assert.equal(bounds.width, 390 - 48)
  assert.equal(bounds.y + bounds.height, 844 - 24)
  const helpFrame = page.frames().find((candidate) => candidate !== page.mainFrame())
  assert.ok(await helpFrame.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "help mobile overflow")
  assert.ok(await frame.getByRole("searchbox").isVisible())
  if (process.env.BROWSER_ARTIFACT_DIR) await page.screenshot({ path: `${process.env.BROWSER_ARTIFACT_DIR}/help-mobile.png`, fullPage: true })
  assert.ok(calls.length >= 2)
  assert.ok(calls.every((path) => path === "/api/public/relay-stations/1/help"))
  assert.deepEqual(errors, [])
  console.log("PASS: full-viewport original HTML/CSS, 16 tables, current/legacy station replacement, protocol suffixes, no canvas/comments, search/Escape, anchors/scroll highlighting, local copy event, mobile layout, no runtime errors")
} finally {
  if (browser) await browser.close()
  await server.close()
}
