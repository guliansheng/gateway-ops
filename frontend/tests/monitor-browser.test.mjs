// Run with a separately installed Playwright module via PLAYWRIGHT_MODULE_PATH.
// All API calls are mocked. No real station data, configuration or model calls are used.
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"
import assert from "node:assert/strict"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer, transformWithEsbuild } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE_PATH
  ? pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH).href
  : pathToFileURL(requireProject.resolve("playwright")).href)
const entryID = "\0monitor-test-entry.tsx"
const tableHTML = '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module" src="/__monitor-test-entry.tsx"></script></body></html>'
const tableEntry = `
import { StrictMode, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { GroupManagementTest } from '/app/relay-stations-page.tsx'
import '/app/globals.css'
const groups = ['甲组', '乙组', '丙组'].map((name, index) => ({ id:index+1, external_id:101+index, name, description:'', platform:'openai', status:'active', is_exclusive:false, account_types:[], model_types:['gpt'], rate_multiplier:1, monitor_enabled:true, account_count:0, priced_account_count:0, channels:[] }))
function Harness() {
  const [stationID,setStationID]=useState(1)
  return <div style={{padding:24}}><button onClick={()=>setStationID(2)}>切换测试中转站</button><GroupManagementTest overview={{ station:{id:stationID,name:'测试站'},groups,accounts:[] }} busy={false} refreshing={false} testingGroupID={null} deletingGroupID={null} onUpdate={async()=>{}} onOrderChange={async()=>{}} onTest={()=>{}} onDelete={()=>{}} onRefresh={()=>{}} /></div>
}
createRoot(document.getElementById('root')).render(<StrictMode><Harness /></StrictMode>)
`
const port = Number(process.env.MONITOR_TEST_PORT || 3011)
const server = await createServer({
  root, configFile: `${root}/vite.config.ts`,
  server: { host: "127.0.0.1", port, strictPort: true },
  plugins: [{
    name: "isolated-monitor-test", enforce: "pre",
    resolveId(id) { if (id === "/__monitor-test-entry.tsx") return entryID },
    async load(id) {
      if (id === entryID) return (await transformWithEsbuild(tableEntry, "monitor-test-entry.tsx", { loader: "tsx", jsx: "automatic" })).code
    },
    transform(code, id) {
      if (id.split("?")[0] === `${root}/app/relay-stations-page.tsx`) return `${code}\nexport { GroupManagement as GroupManagementTest };`
    },
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        if (req.url === "/__monitor-test__") {
          res.setHeader("Content-Type", "text/html")
          res.end(await server.transformIndexHtml(req.url, tableHTML))
          return
        }
        next()
      })
    },
  }],
})
const base = `http://127.0.0.1:${port}`
const defaultConfig = { enabled: false, method: "pelican", model: "", judge_model: "", effort: "medium", interval_minutes: 15, preview_mode: "hover", result_visibility: "show" }
const configs = new Map([
  ["1:101", { ...defaultConfig, model: "model-a" }],
  ["1:102", { ...defaultConfig }],
  ["1:103", { ...defaultConfig, model: "model-c" }],
  ["2:101", { ...defaultConfig, model: "station-two-a" }],
  ["2:102", { ...defaultConfig, model: "station-two-b" }],
  ["2:103", { ...defaultConfig, model: "station-two-c" }],
])
const failures = new Set(["1:103"])
const writes = []
const errors = []
const artifacts = process.env.BROWSER_ARTIFACT_DIR
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1760, height: 1080 } })
  await context.route("**/api/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const match = url.pathname.match(/^\/api\/relay-stations\/(\d+)\/groups\/(\d+)\/(degradation|models)$/)
    if (match) {
      const key = `${match[1]}:${match[2]}`
      if (match[3] === "models") return route.fulfill({ json: { data: ["model-a", "model-b", "model-c"] } })
      if (request.method() === "GET") {
        if (failures.has(key)) return route.fulfill({ status: 503, json: { error: "模拟配置读取失败" } })
        return route.fulfill({ json: { data: { config: configs.get(key) ?? defaultConfig, results: [] } } })
      }
      if (request.method() === "PUT") {
        const body = request.postDataJSON()
        writes.push({ key, body }); configs.set(key, body)
        await new Promise((resolve) => setTimeout(resolve, 80))
		return route.fulfill({ json: { data: { config: body, results: [] } } })
      }
    }
    if (url.pathname.endsWith("/group-monitor")) {
      const now = new Date().toISOString()
      const groups = [90, 70, 50, 49].map((score, index) => ({ external_id: 101 + index, name: `可用率${score}%`, description: index === 1 ? "测试分组说明，用于验证不同内容长度的卡片保持同行等高，指标按自然顺序排列。" : "", platform: "openai", rate_multiplier: index === 0 ? 1.23456 : 1, enabled: true, group_status: "active", status: "available", data_complete: true, updated_at: now, success_count: score, failure_count: 100 - score, consecutive_failures: 0, calls: [], cache_rate: index === 0 ? 0.75 : 0.5, cache_sample_count: 51, cache_prompt_tokens: index === 0 ? 12000000 : 10200000, cache_read_tokens: index === 0 ? 9000000 : 5100000, cache_rate_prompt_tokens: index === 0 ? 4000000 : 10200000, cache_rate_read_tokens: index === 0 ? 3000000 : 5100000, cache_sample_status: "ready", first_token_ms: 120, buckets: [{ start_at: now, end_at: now, total: 100, success_count: score, failure_count: 100 - score, availability: score / 100, first_token_ms: 120, data_complete: true }] }))
      groups.push({ ...groups[0], external_id: 105, name: "无请求", success_count: 0, failure_count: 0, cache_rate: null, cache_sample_count: 0, cache_prompt_tokens: 0, cache_read_tokens: 0, cache_rate_prompt_tokens: 0, cache_rate_read_tokens: 0, cache_sample_status: "insufficient", buckets: [{ start_at: now, end_at: now, total: 0, success_count: 0, failure_count: 0, data_complete: true }] })
      return route.fulfill({ json: { data: { station_id: 1, station_name: "测试站", range: url.searchParams.get("range") ?? "90m", updated_at: now, groups, summary: { total: 5, available: 4, idle: 1, degraded: 0, unavailable: 0, disabled: 0, unknown: 0 } } } })
    }
    if (/^\/api\/public\/relay-stations\/\d+\/groups\/\d+\/degradation$/.test(url.pathname)) {
      const groupID = Number(url.pathname.match(/\/groups\/(\d+)\//)?.[1])
      const candy = {id:1,method:"candy",model:"candy-model",judge_model:"judge",effort:"none",status:"normal",reason:"必须隐藏的评审理由",answer:"糖果答案 42",created_at:"2026-01-01T12:34:00Z"}
      const pelican = {id:2,method:"pelican",model:"pelican-model",judge_model:"judge",effort:"none",status:"degraded",reason:"必须隐藏的评审理由",html:"<!doctype html><html><body><p>鹈鹕 HTML 内容</p></body></html>",created_at:"2026-01-01T12:35:00Z"}
      return route.fulfill({json:{data:{config:{...defaultConfig,enabled:groupID>=101&&groupID<=103,method:"both",model:"gpt-6-astra",effort:"none",interval_minutes:30,result_visibility:groupID===103?"hide":"show"},results:groupID===101?[candy]:groupID===102?[pelican]:groupID===103?[candy]:[]}}})
    }
    return route.fulfill({ status: 501, json: { error: `Unexpected mocked endpoint: ${request.method()} ${url.pathname}` } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`${base}/__monitor-test__`)
  const a = page.getByRole("switch", { name: "甲组开启降智监控" })
  const b = page.getByRole("switch", { name: "乙组开启降智监控" })
  await a.waitFor()
  await page.waitForFunction(() => !document.querySelector('[aria-label="甲组开启降智监控"]').disabled)
  assert.equal(await b.isDisabled(), true)
  const labels = await page.locator("div.sticky.top-0").first().innerText()
  assert.match(labels, /监控\s*降智监控\s*模型价格\s*状态/)
  await a.click()
  await page.getByRole("switch", { name: "甲组关闭降智监控" }).waitFor()
  assert.equal(writes.length, 1)
  assert.equal(writes[0].key, "1:101")
  assert.equal(writes[0].body.model, "model-a")
  assert.equal(configs.get("1:102").enabled, false)
  assert.equal(await b.isDisabled(), true)
  await page.getByRole("button", { name: "配置乙组降智监控", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("调用模型",{exact:true}).fill("model-b")
  await dialog.getByLabel("鹈鹕检测间隔（分钟）").fill("60")
  await dialog.getByLabel("糖果检测间隔（分钟）").fill("30")
  await dialog.getByRole("switch").click()
  await dialog.getByRole("button", { name: "保存配置", exact: true }).click()
  await dialog.getByText("配置已保存", { exact: true }).waitFor()
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  const enabledB = page.getByRole("switch", { name: "乙组关闭降智监控" })
  await enabledB.waitFor()
  assert.equal(await enabledB.isChecked(), true)
  assert.equal(configs.get("1:102").model, "model-b")
  assert.equal(configs.get("1:102").pelican_interval_minutes, 60)
  assert.equal(configs.get("1:102").candy_interval_minutes, 30)
  assert.equal(configs.get("1:101").model, "model-a")
  await enabledB.click()
  await page.getByRole("switch", { name: "乙组开启降智监控" }).waitFor()
  assert.equal(configs.get("1:102").enabled, false)
  assert.equal(configs.get("1:101").enabled, true)
  await page.getByRole("button", { name: "配置丙组降智监控", exact: true }).click()
  await dialog.getByRole("button", { name: "重新读取配置", exact: true }).waitFor()
  assert.equal(await dialog.getByRole("button", { name: "保存配置", exact: true }).count(), 0)
  failures.delete("1:103")
  await dialog.getByRole("button", { name: "重新读取配置", exact: true }).click()
  await dialog.getByLabel("调用模型", { exact: true }).waitFor()
  assert.equal(await dialog.getByLabel("调用模型", { exact: true }).inputValue(), "model-c")
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("button", { name: "切换测试中转站", exact: true }).click()
  await page.waitForFunction(() => !document.querySelector('[aria-label="甲组开启降智监控"]').disabled)
  await page.getByRole("switch", { name: "甲组开启降智监控" }).click()
  await page.getByRole("switch", { name: "甲组关闭降智监控" }).waitFor()
  assert.equal(writes.at(-1).key, "2:101")
  assert.equal(writes.at(-1).body.model, "station-two-a")
  if (artifacts) await page.screenshot({ path: `${artifacts}/group-management.png`, fullPage: true })
  console.log("PASS: column order, per-group toggle, config-save sync, independent methods/models, failed load retry, station scope")
  await page.goto(`${base}/public/relay-monitor/1`)
  await page.getByRole("heading", { name: "可用率90%", exact: true }).waitFor()
  await page.getByRole("heading", { name: "降智检测", exact: true }).first().waitFor()
  assert.equal(await page.locator("article").filter({ has: page.getByRole("heading", { name: "可用率90%", exact: true }) }).getByText("倍率 1.235x", { exact: true }).count(), 1)
  assert.equal(await page.locator("article").filter({ has: page.getByRole("heading", { name: "无请求", exact: true }) }).getByText("样本不足", { exact: true }).count(), 1)
  assert.equal(await page.locator("article").filter({ has: page.getByRole("heading", { name: "可用率90%", exact: true }) }).getByText("75.0%", { exact: true }).count(), 1)
  assert.match(await page.locator("[data-public-header-details]").last().innerText(), /缓存率 52\.9% · 有效组 4\/5/)
  assert.doesNotMatch((await page.locator("article").allInnerTexts()).join("\n"), /条流式请求|prompt tokens|缓存样本：/)
  for (const [score, colorClass] of [[90, "bg-emerald-600"], [70, "bg-emerald-300"], [50, "bg-yellow-400"], [49, "bg-red-500"]]) {
    const card = page.locator("article").filter({ has: page.getByRole("heading", { name: `可用率${score}%`, exact: true }) })
    const bar = card.locator('button[role="listitem"]').first()
    assert.match(await bar.getAttribute("class"), new RegExp(colorClass))
    const style = await card.evaluate((el) => ({ shadow: getComputedStyle(el).boxShadow, radius: getComputedStyle(el).borderRadius }))
    assert.notEqual(style.shadow, "none")
    assert.ok(parseFloat(style.radius) > 12, style.radius)
    assert.doesNotMatch(await card.innerText(), /糖果题|gpt-6-astra|none|每 30 分钟|仅显示通过情况/)
  }
  for (const [score, colorClass] of [[90, "bg-emerald-600"], [70, "bg-emerald-300"], [50, "bg-yellow-400"], [49, "bg-red-500"]]) {
    const card = page.locator("article").filter({ has: page.getByRole("heading", { name: `可用率${score}%`, exact: true }) })
    const bar = card.locator('button[role="listitem"]').first()
    assert.match(await bar.getAttribute("class"), new RegExp(colorClass))
    const style = await card.evaluate((el) => ({ shadow: getComputedStyle(el).boxShadow, radius: getComputedStyle(el).borderRadius }))
    assert.notEqual(style.shadow, "none")
    assert.ok(parseFloat(style.radius) > 12, style.radius)
    assert.doesNotMatch(await card.innerText(), /糖果题|gpt-6-astra|none|每 30 分钟|仅显示通过情况/)
  }
  const candyCard = page.locator("article").filter({has:page.getByRole("heading",{name:"可用率90%",exact:true})})
  const candyLabel = /2026\/1\/1 12:34.*candy-model/
  const candyBar = candyCard.getByLabel("降智检测").getByLabel(candyLabel)
  await candyBar.hover()
  const candyPreview = page.locator("[data-radix-popper-content-wrapper]").last()
  await candyPreview.getByTitle("candy-model").waitFor()
  assert.match(await candyPreview.innerText(), /糖果答案 42/)
  assert.doesNotMatch(await candyPreview.innerText(), /必须隐藏的评审理由|通过检测/)
  const hint = "悬浮预览，点击查看详情"
  assert.equal(await candyCard.getByText(hint, { exact: true }).count(), 1)
  await candyBar.click()
  await page.getByRole("dialog").getByText("糖果答案 42",{exact:true}).waitFor()
  await page.getByRole("button",{name:"Close",exact:true}).click()
  const pelicanCard = page.locator("article").filter({has:page.getByRole("heading",{name:"可用率70%",exact:true})})
  const pelicanLabel = /2026\/1\/1 12:35.*pelican-model/
  const pelicanBar = pelicanCard.getByLabel("降智检测").getByLabel(pelicanLabel)
  await pelicanBar.hover()
  const pelicanPreview = page.locator("[data-radix-popper-content-wrapper]").last()
  await pelicanPreview.getByTitle("pelican-model").waitFor()
  assert.equal(await pelicanPreview.locator("iframe").count(),1)
  assert.doesNotMatch(await pelicanPreview.innerText(),/必须隐藏的评审理由|未通过检测/)
  const pelicanCentering = await pelicanPreview.frameLocator("iframe").locator("html").evaluate(() => {
    const box = document.body.querySelector("p").getBoundingClientRect()
    return { dx: Math.abs(box.left + box.width / 2 - window.innerWidth / 2), dy: Math.abs(box.top + box.height / 2 - window.innerHeight / 2) }
  })
  assert.ok(pelicanCentering.dx < 2 && pelicanCentering.dy < 2, `preview content is centered on both axes: ${JSON.stringify(pelicanCentering)}`)
  console.log("PASS: pelican preview content is centered on both axes")
  const hiddenCard = page.locator("article").filter({has:page.getByRole("heading",{name:"可用率50%",exact:true})})
  assert.doesNotMatch(await hiddenCard.getByLabel("降智检测").innerText(),/糖果答案 42|candy-model/)
  assert.equal(await hiddenCard.getByText(hint, { exact: true }).count(), 0)
  console.log("PASS: degradation hint only appears when public results are shown")
  const cardLayout = await page.locator("article").evaluateAll((cards) => cards.slice(0, 4).map((card) => {
    const bounds = card.getBoundingClientRect()
    const header = card.querySelector("header").getBoundingClientRect()
    const metricGrid = card.querySelector("[data-public-monitor-metrics]")
    const metrics = metricGrid.parentElement
    const labels = [...metricGrid.querySelectorAll("div > p:first-child")].map((node) => node.textContent)
    const last = card.lastElementChild.getBoundingClientRect()
    return { y: bounds.y, height: bounds.height, metricsY: metrics.getBoundingClientRect().y, headerBottom: header.bottom, marginTop: getComputedStyle(metrics).marginTop, trailingSpace: bounds.bottom - last.bottom, labels }
  }))
  assert.ok(cardLayout.every((card) => card.y === cardLayout[0].y && card.height === cardLayout[0].height), "same-row monitor cards are equally tall")
  assert.ok(cardLayout.every((card) => card.marginTop === "0px"), "metrics are not pushed down with auto margins")
  for (const card of cardLayout) assert.deepEqual(card.labels, ["可用率", "缓存率", "首 Token"], "cache rate includes sample quality on the card")
  assert.equal(cardLayout[2].metricsY, cardLayout[2].headerBottom)
  assert.ok(cardLayout[1].metricsY > cardLayout[2].metricsY, "longer description stays above its metrics in natural flow")
  assert.ok(cardLayout[2].trailingSpace > 30, "short cards leave unused space at the bottom")
  const firstTrendBar = page.locator("article").first().locator('button[role="listitem"]').first()
  await firstTrendBar.hover()
  const trendTooltip = page.getByRole("tooltip")
  await trendTooltip.waitFor()
  const trendTooltipText = await trendTooltip.innerText()
  assert.match(trendTooltipText, /次请求/)
  assert.match(trendTooltipText, /可用率/)
  assert.match(trendTooltipText, /首 Token/)
  assert.doesNotMatch(trendTooltipText, /缓存率|条流式请求|prompt tokens|缓存样本：/)
  await page.getByRole("heading", { name: "渠道状态", exact: true }).hover()
  console.log("PASS: three metric cards with cache sample quality, bucket tooltip without cache details, natural top-to-bottom content, same-row card heights, bottom whitespace and no degradation configuration summary")
  await page.getByRole("tab", { name: "7d", exact: true }).click()
  await page.waitForFunction(() => document.querySelector('[aria-selected="true"]').textContent === "7d")
  if (artifacts) await page.screenshot({ path: `${artifacts}/group-monitor-desktop.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  if (artifacts) await page.screenshot({ path: `${artifacts}/group-monitor-mobile.png`, fullPage: true })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "monitor mobile overflow")
  assert.deepEqual(errors, [])
  console.log("PASS: four colors, larger card radius, shadows, time-range switch, mobile overflow, no browser runtime errors")
} finally {
  if (browser) await browser.close()
  await server.close()
}
