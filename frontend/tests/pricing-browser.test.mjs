// Mocked public-page checks; install Playwright separately and set PLAYWRIGHT_MODULE_PATH.
import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE_PATH
  ? pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH).href
  : pathToFileURL(requireProject.resolve("playwright")).href)
const port = Number(process.env.PUBLIC_PAGES_TEST_PORT || 3039)
const server = await createServer({ root, configFile: `${root}/vite.config.ts`, server: { host: "127.0.0.1", port, strictPort: true } })
const base = `http://127.0.0.1:${port}`
const item = (model, company, overrides = {}) => ({ model, company, channel_id: 1, channel_name: "设置价格的渠道", station_id: 1, station_name: "测试站", billing_mode: "token", price_source: "channel_override", groups: ["Z-优先分组", "A-后置分组"], input_price: 1.5e-6, output_price: 4.5e-6, official_pricing: { input_price: 1.5e-6, output_price: 4.5e-6 }, official_pricing_source: "channel_pricing", ...overrides })
const pricing = {
  station_id: 1, station_name: "测试站", currency: "USD", updated_at: new Date().toISOString(), warnings: ["分组「失败分组」模型读取失败，价格列表可能不完整。"],
  public_groups: [{ name: "Z-优先分组", rate_multiplier: 2 }, { name: "A-后置分组", rate_multiplier: 1 }, { name: "生图分组", rate_multiplier: 10 }],
  items: [
    item("deepseek-v4.1-flash", "DeepSeek"),
    item("step-3.5-flash", "阶跃星辰"),
    item("mimo-v2-flash", "小米"),
    item("paint-alias", "Google", { billing_mode: "per_request", price_source: "group_image_pricing", groups: ["生图分组"], image_price_1k: 0.03, image_price_2k: 0.04, image_price_4k: 0.06 }),
    item("hidden-request-price", "其他", { billing_mode: "per_request", per_request_price: 10 }),
  ],
  summary: { models: 5, companies: 5, channels: 1, companies_list: [], platforms: [], billing_modes: ["token", "per_request"] },
}
const errors = []
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1700, height: 1000 } })
  await context.route("**/api/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/api/public/relay-stations/1/model-pricing") return route.fulfill({ json: { data: pricing } })
    return route.fulfill({ status: 501, json: { error: `Unexpected mocked endpoint: ${url.pathname}` } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`${base}/public/model-pricing/1`)
  await page.getByRole("heading", { name: "deepseek-v4.1-flash", exact: true }).waitFor()
  assert.equal(await page.getByRole("heading", { name: "hidden-request-price", exact: true }).count(), 0)
  const deepseek = page.getByRole("table", { name: "deepseek-v4.1-flash公开分组价格", exact: true })
  const rows = await deepseek.getByRole("row").allTextContents()
  assert.match(rows[1], /渠道设置原价/)
  assert.match(rows[1], /\$1\.5/)
  assert.match(rows[1], /\$4\.5/)
  assert.match(rows[2], /Z-优先分组/)
  assert.match(rows[3], /A-后置分组/)
  assert.match(rows[2], /\$3/)
  assert.match(rows[2], /\$9/)
  const image = page.getByRole("table", { name: "paint-alias公开分组价格", exact: true })
  assert.equal(await image.getByRole("row").count(), 2)
  assert.match(await image.innerText(), /1K \(\$\)/)
  assert.match(await image.innerText(), /2K \(\$\)/)
  assert.match(await image.innerText(), /4K \(\$\)/)
  for (const price of ["$0.03", "$0.04", "$0.06"]) assert.ok((await image.innerText()).includes(price))
  assert.doesNotMatch(await image.innerText(), /原价|\$0\.3|\$0\.4|\$0\.6/)
  const groupButtons = await page.getByRole("complementary", { name: "价格筛选" }).getByRole("button").allTextContents()
  assert.ok(groupButtons.findIndex((text) => text.includes("Z-优先分组")) < groupButtons.findIndex((text) => text.includes("A-后置分组")))
  for (const company of ["阶跃星辰", "小米"]) {
    const header = page.locator("section > header").filter({ has: page.getByRole("heading", { name: company, exact: true }) })
    assert.ok(await header.locator("img").evaluate((el) => el.complete && el.naturalWidth > 0), `${company} icon loads`)
  }
  await page.getByRole("button", { name: /按次付费/ }).click()
  assert.equal(await page.getByRole("heading", { name: "deepseek-v4.1-flash", exact: true }).count(), 0)
  assert.equal(await page.getByRole("heading", { name: "paint-alias", exact: true }).count(), 1)
  await page.getByRole("button", { name: "重置", exact: true }).click()
  await page.getByLabel("模型搜索", { exact: true }).fill("deepseek-v4.1")
  assert.equal(await page.getByRole("heading", { name: "paint-alias", exact: true }).count(), 0)
  assert.equal(await page.getByRole("heading", { name: "deepseek-v4.1-flash", exact: true }).count(), 1)
  await page.getByRole("button", { name: "重置", exact: true }).click()
  const groupFilter = page.getByRole("complementary", { name: "价格筛选" }).getByRole("button", { name: /生图分组/ })
  await groupFilter.click()
  assert.equal(await page.getByRole("heading", { name: "paint-alias", exact: true }).count(), 1)
  assert.equal(await page.getByRole("heading", { name: "deepseek-v4.1-flash", exact: true }).count(), 0)
  assert.doesNotMatch(await image.innerText(), /原价|×10/)
  for (const price of ["$0.03", "$0.04", "$0.06"]) assert.ok((await image.innerText()).includes(price))
  assert.ok(await page.getByRole("alert").getByText("部分分组价格暂未读取完整", { exact: true }).isVisible())
  assert.ok(await page.getByRole("alert").getByText(pricing.warnings[0], { exact: true }).isVisible())
  await page.getByRole("button", { name: "重置", exact: true }).click()
  if (process.env.BROWSER_ARTIFACT_DIR) await page.screenshot({ path: `${process.env.BROWSER_ARTIFACT_DIR}/model-pricing-desktop.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  const billing = page.getByRole("combobox", { name: "筛选计费方式", exact: true })
  assert.deepEqual(await billing.locator("option").evaluateAll((options) => options.map((option) => option.value)), ["all", "token", "request"])
  await billing.selectOption("request")
  assert.equal(await page.getByRole("heading", { name: "paint-alias", exact: true }).count(), 1)
  assert.equal(await page.getByRole("heading", { name: "deepseek-v4.1-flash", exact: true }).count(), 0)
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "pricing mobile overflow")
  if (process.env.BROWSER_ARTIFACT_DIR) await page.screenshot({ path: `${process.env.BROWSER_ARTIFACT_DIR}/model-pricing-mobile.png`, fullPage: true })
  assert.deepEqual(errors, [])
  console.log("PASS: saved $1.5/$4.5 channel prices, group ordering/multiplier, original-free $0.03/$0.04/$0.06 image prices, two billing kinds, search/filter, brand icons, mobile layout, no runtime errors")
} finally {
  if (browser) await browser.close()
  await server.close()
}
