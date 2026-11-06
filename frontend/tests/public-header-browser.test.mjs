// Compact public header QA. API responses are mocked; no live station data is used.
import assert from "node:assert/strict"
import { mkdir, readFile } from "node:fs/promises"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE_PATH
  ? pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH).href
  : pathToFileURL(requireProject.resolve("playwright")).href)
const port = Number(process.env.PUBLIC_HEADER_TEST_PORT || 3042)
const server = await createServer({ root, configFile: `${root}/vite.config.ts`, server: { host: "127.0.0.1", port, strictPort: true } })
const helpHTML = await readFile(`${root}/lib/help-reference.html`, "utf8")
const updatedAt = "2026-01-01T12:00:00Z"
const calls = new Map()
const errors = []
const service = (id, name) => ({ id, name, url: `https://${id}.example`, history_url: `https://${id}.example/history`, updated_at: updatedAt, status: { indicator: "none", description: "全部正常" }, components: [], groups: [], incidents: [], past_incidents: [], scheduled_maintenances: [] })
let services = [service("openai", "OpenAI"), service("claude", "Claude")]
const group = { external_id: 1, name: "测试分组", platform: "openai", enabled: true, group_status: "active", status: "available", data_complete: true, updated_at: updatedAt, rate_multiplier: 1, success_count: 95, failure_count: 5, consecutive_failures: 0, calls: [], buckets: [{ start_at: updatedAt, end_at: updatedAt, total: 100, success_count: 95, failure_count: 5, availability: 0.95, data_complete: true }], cache_rate: 0.6, cache_sample_count: 51, cache_prompt_tokens: 12000000, cache_read_tokens: 7200000, cache_rate_prompt_tokens: 12000000, cache_rate_read_tokens: 7200000, cache_sample_status: "ready", first_token_ms: 150 }
const pages = [
  { path: "model-pricing/1", title: "模型价格", refresh: "刷新模型价格", endpoint: "/api/public/relay-stations/1/model-pricing" },
  { path: "relay-monitor/1", title: "渠道状态", refresh: "刷新监控", endpoint: "/api/public/relay-stations/1/group-monitor", toolbar: true, countdown: true },
  { path: "service-status", title: "AI 服务状态", refresh: "刷新服务状态", endpoint: "/api/public/service-status", toolbar: true, countdown: true },
  { path: "help/1", title: "帮助文档" },
]
const artifactDir = process.env.BROWSER_ARTIFACT_DIR
if (artifactDir) await mkdir(artifactDir, { recursive: true })
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext()
  await context.route("**/api/**", async (route) => {
    const request = route.request()
    assert.equal(request.method(), "GET")
    const url = new URL(request.url())
    calls.set(url.pathname, (calls.get(url.pathname) || 0) + 1)
    let data
    if (url.pathname.endsWith("/model-pricing")) data = { station_id: 1, station_name: "测试站", currency: "USD", updated_at: updatedAt, public_groups: [{ name: "测试分组", rate_multiplier: 1 }], items: [{ model: "deepseek-v4.1-flash", company: "DeepSeek", billing_mode: "token", price_source: "channel_override", station_id: 1, station_name: "测试站", channel_id: 1, channel_name: "测试渠道", groups: ["测试分组"], input_price: 1.5e-6, output_price: 4.5e-6, official_pricing: { input_price: 1.5e-6, output_price: 4.5e-6 }, official_pricing_source: "channel_pricing" }], summary: { models: 1, companies: 1, channels: 1, companies_list: ["DeepSeek"], platforms: [], billing_modes: ["token"] } }
    else if (url.pathname.endsWith("/group-monitor")) data = { station_id: 1, station_name: "测试站", updated_at: updatedAt, range: url.searchParams.get("range") || "90m", groups: [group], summary: { total: 1, available: 1, degraded: 0, unavailable: 0, idle: 0, disabled: 0, unknown: 0 } }
    else if (url.pathname.endsWith("/degradation")) data = { config: { enabled: false }, results: [] }
    else if (url.pathname.endsWith("/service-status")) data = { updated_at: updatedAt, days: 60, services }
    else if (url.pathname.endsWith("/help")) data = { id: 1, title: "测试站接入文档", station_name: "测试站", station_url: "https://relay.example", updated_at: updatedAt, content: helpHTML, comments: [] }
    else throw new Error(`Unexpected mocked endpoint: ${url.pathname}`)
    await new Promise((resolve) => setTimeout(resolve, 50))
    return route.fulfill({ json: { data } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  for (const size of [{ width: 1440, height: 960 }, { width: 768, height: 1024 }, { width: 390, height: 844 }, { width: 320, height: 740 }]) {
    await page.setViewportSize(size)
    let baseline
    let toolbarBaseline
    let refreshBaseline
    let tabsBaseline
    for (const entry of pages) {
      await page.goto(`http://127.0.0.1:${port}/public/${entry.path}`)
      const header = page.locator("[data-public-page-header]")
      await header.getByRole("heading", { name: entry.title, exact: true }).waitFor()
      await page.waitForFunction(() => document.querySelector('[data-public-header-metadata] time')?.getAttribute("datetime"))
      assert.equal(await header.count(), 1)
      const style = await header.evaluate((el) => {
        const read = (node, keys) => { const s = getComputedStyle(node); return Object.fromEntries(keys.map((key) => [key, s[key]])) }
        return {
          card: read(el, ["borderRadius", "borderColor", "backgroundColor", "boxShadow", "marginBottom"]),
          identity: read(el.querySelector("[data-public-header-identity]"), ["paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "gap"]),
          title: read(el.querySelector("h1"), ["fontFamily", "fontSize", "fontWeight", "lineHeight", "color"]),
          description: read(el.querySelector("[data-public-header-description]"), ["fontFamily", "fontSize", "lineHeight", "color", "marginTop"]),
          icon: read(el.querySelector("[data-public-header-icon]"), ["width", "height", "borderRadius", "backgroundColor", "color"]),
          svg: read(el.querySelector("[data-public-header-icon] svg"), ["width", "height"]),
        }
      })
      if (!baseline) baseline = style
      else assert.deepEqual(style, baseline, `${entry.title}: shared header styles at ${size.width}px`)
      assert.notEqual(style.card.boxShadow, "none")
      assert.equal(style.card.borderRadius, "14px")
      assert.equal(style.card.marginBottom, "12px")
      assert.equal(style.identity.paddingTop, "12px")
      assert.equal(style.identity.paddingBottom, "12px")
      assert.equal(style.identity.paddingLeft, "16px")
      assert.equal(style.identity.paddingRight, "16px")
      assert.equal(style.title.fontSize, "18px")
      assert.equal(style.description.fontSize, "12px")
      assert.equal(style.icon.width, "32px")
      assert.equal(style.icon.height, "32px")
      assert.equal(style.svg.width, "16px")
      assert.equal(style.svg.height, "16px")
      const bounds = await header.boundingBox()
      assert.equal(bounds.x, 24)
      assert.equal(bounds.y, 24)
      assert.equal(bounds.width, size.width - 48)
      const identity = header.locator("[data-public-header-identity]")
      const status = identity.locator("[data-public-header-status]")
      const toolbar = header.locator("[data-public-header-toolbar]")
      const identityBounds = await identity.boundingBox()
      const statusBounds = await status.boundingBox()
      const headingBounds = await identity.locator("[data-public-header-heading]").boundingBox()
      const titleBounds = await header.getByRole("heading", { name: entry.title, exact: true }).boundingBox()
      assert.equal(identityBounds.height, 76, `${entry.title}: compact identity row at ${size.width}px`)
      assert.equal(titleBounds.height, 24, `${entry.title}: title stays on one line at ${size.width}px`)
      assert.equal(statusBounds.y, identityBounds.y + 12)
      assert.equal(statusBounds.x + statusBounds.width, identityBounds.x + identityBounds.width - 16)
      assert.ok(headingBounds.x + headingBounds.width <= statusBounds.x - 7, `${entry.title}: status does not overlap heading`)
      assert.equal(await identity.locator("button").count(), entry.refresh ? 1 : 0)
      const time = status.locator("time")
      assert.equal(await time.getAttribute("datetime"), updatedAt)
      assert.match(await time.locator("..").getAttribute("title"), /更新于 2026/)
      if (size.width < 640) assert.match(await time.innerText(), /^\d{2}:\d{2}$/)
      else assert.match(await time.innerText(), /\d{2}\/\d{2}.*\d{2}:\d{2}/)
      if (entry.countdown) assert.equal(await status.getByText(/秒后刷新/).isVisible(), size.width >= 1024)
      assert.equal(await toolbar.count(), entry.toolbar ? 1 : 0, `${entry.title}: only actual controls get a second row`)
      if (entry.toolbar) {
        const toolbarStyle = await toolbar.evaluate((el) => { const s = getComputedStyle(el); return { paddingTop: s.paddingTop, paddingRight: s.paddingRight, paddingBottom: s.paddingBottom, paddingLeft: s.paddingLeft, borderTopWidth: s.borderTopWidth, backgroundColor: s.backgroundColor } })
        if (!toolbarBaseline) toolbarBaseline = toolbarStyle
        else assert.deepEqual(toolbarStyle, toolbarBaseline)
        assert.equal(toolbarStyle.paddingTop, "8px")
        assert.equal(toolbarStyle.paddingBottom, "8px")
        const toolbarBounds = await toolbar.boundingBox()
        assert.equal(toolbarBounds.y, identityBounds.y + identityBounds.height)
        assert.equal(await toolbar.locator("time").count(), 0)
        assert.equal(await toolbar.locator("[data-public-header-controls]").getByRole("tablist").count(), 1)
        assert.ok(bounds.height <= (size.width < 640 && entry.path.startsWith("relay") ? 174 : 132), `${entry.title}: compact control row (${bounds.height}px)`)
      } else {
        assert.equal(bounds.height, 78, `${entry.title}: no unused second row`)
      }
      if (entry.refresh) {
        const refresh = status.getByRole("button", { name: entry.refresh, exact: true })
        const refreshStyle = await refresh.evaluate((el) => { const s = getComputedStyle(el); return { width: s.width, height: s.height, borderRadius: s.borderRadius, fontSize: s.fontSize, fontWeight: s.fontWeight } })
        if (!refreshBaseline) refreshBaseline = refreshStyle
        else assert.deepEqual(refreshStyle, refreshBaseline)
        assert.equal(refreshStyle.width, "32px")
        assert.equal(refreshStyle.height, "32px")
        const refreshBounds = await refresh.boundingBox()
        assert.equal(refreshBounds.x + refreshBounds.width, statusBounds.x + statusBounds.width)
        assert.equal(refreshBounds.y, statusBounds.y)
        await page.waitForFunction((label) => !document.querySelector(`[aria-label="${label}"]`).disabled, entry.refresh)
        const before = calls.get(entry.endpoint)
        await refresh.click()
        await page.waitForFunction((label) => !document.querySelector(`[aria-label="${label}"]`).disabled, entry.refresh)
        assert.ok(calls.get(entry.endpoint) > before, entry.refresh)
      }
      if (entry.toolbar) {
        const tabs = toolbar.getByRole("tablist")
        const tabStyle = await tabs.getByRole("tab").first().evaluate((el) => { const s = getComputedStyle(el); return { height: s.height, fontSize: s.fontSize, fontWeight: s.fontWeight, borderRadius: s.borderRadius } })
        if (!tabsBaseline) tabsBaseline = tabStyle
        else assert.deepEqual(tabStyle, tabsBaseline)
        assert.equal(tabStyle.height, "32px")
        assert.equal(tabStyle.fontSize, "12px")
        const first = tabs.getByRole("tab").first()
        await first.focus()
        await first.press("ArrowRight")
        assert.equal(await tabs.getByRole("tab").nth(1).getAttribute("aria-selected"), "true")
        assert.equal(await tabs.getByRole("tab").nth(1).getAttribute("tabindex"), "0")
        assert.ok(await tabs.getByRole("tab").nth(1).evaluate((el) => el === document.activeElement))
        await tabs.getByRole("tab").nth(1).press("Home")
        assert.equal(await tabs.getByRole("tab").first().getAttribute("aria-selected"), "true")
      }
      if (entry.path === "service-status") {
        const contentBounds = await page.getByRole("region", { name: "服务状态详情" }).boundingBox()
        assert.equal(contentBounds.y, bounds.y + bounds.height + 12)
      }
      if (entry.path.startsWith("help")) {
        const iframe = page.locator("iframe")
        await iframe.waitFor()
        const frameBounds = await iframe.boundingBox()
        assert.equal(frameBounds.x, 24)
        assert.equal(frameBounds.width, size.width - 48)
        assert.equal(frameBounds.y, bounds.y + bounds.height + 12)
        assert.equal(frameBounds.y + frameBounds.height, size.height - 24)
        assert.ok(await page.frameLocator("iframe").locator("body > header").isHidden())
        assert.ok(await page.frameLocator("iframe").getByRole("searchbox").isVisible())
      }
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${entry.title}: horizontal overflow`)
      if (artifactDir) await page.screenshot({ path: `${artifactDir}/header-${entry.path.split("/")[0]}-${size.width}.png` })
      console.log(`PASS ${entry.title} ${size.width}px: ${bounds.height}px header, top-right update/refresh, ${entry.toolbar ? "compact controls" : "no toolbar"}`)
    }
  }
  await page.setViewportSize({ width: 1440, height: 960 })
  let darkBaseline
  for (const entry of pages) {
    await page.goto(`http://127.0.0.1:${port}/public/${entry.path}`)
    const header = page.locator("[data-public-page-header]")
    await header.waitFor()
    await page.evaluate(() => document.documentElement.classList.add("dark"))
    const colors = await header.evaluate((el) => ({ card: getComputedStyle(el).backgroundColor, title: getComputedStyle(el.querySelector("h1")).color, subtitle: getComputedStyle(el.querySelector("[data-public-header-description]")).color, icon: getComputedStyle(el.querySelector("[data-public-header-icon]")).color, metadata: getComputedStyle(el.querySelector("[data-public-header-metadata]")).color }))
    if (!darkBaseline) darkBaseline = colors
    else assert.deepEqual(colors, darkBaseline, `${entry.title}: dark header consistency`)
    if (artifactDir) await page.screenshot({ path: `${artifactDir}/header-${entry.path.split("/")[0]}-dark.png` })
  }
  services = []
  await page.evaluate(() => localStorage.removeItem("gatewayops:public-service-status:v1"))
  await page.goto(`http://127.0.0.1:${port}/public/service-status`)
  await page.waitForFunction(() => document.querySelector('[data-public-header-metadata] time')?.getAttribute("datetime"))
  assert.ok(await page.getByText("暂无服务状态数据", { exact: true }).isVisible())
  assert.equal(await page.locator("[data-public-header-toolbar]").count(), 0)
  assert.equal((await page.locator("[data-public-page-header]").boundingBox()).height, 78)
  console.log("PASS: empty service list leaves no unused toolbar")
  assert.deepEqual(errors, [])
  console.log("PASS: compact heights, top-right refresh/time, optional toolbar, tab keyboard navigation, dark colors, 24px inset and no runtime errors")
} finally {
  if (browser) await browser.close()
  await server.close()
}
