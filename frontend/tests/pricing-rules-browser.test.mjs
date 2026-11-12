// Read-only browser acceptance against mocked API responses; no real billing requests.
import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { mkdir } from "node:fs/promises"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH || requireProject.resolve("playwright")).href)
const port = Number(process.env.PRICING_RULES_TEST_PORT || 3041)
const server = await createServer({ root, configFile: `${root}/vite.config.ts`, server: { host: "127.0.0.1", port, strictPort: true } })
const base = `http://127.0.0.1:${port}`
const artifacts = process.env.BROWSER_ARTIFACT_DIR
const item = (model, overrides = {}) => ({
  model, company: "DeepSeek", channel_id: 1, channel_name: "国模", station_id: 1, station_name: "规则测试站",
  billing_mode: "token", price_source: "channel_override", groups: ["标准分组"],
  input_price: 1.5e-6, output_price: 4.5e-6,
  official_pricing: { input_price: 1.5e-6, output_price: 4.5e-6 }, official_pricing_source: "channel_pricing", ...overrides,
})
const tiered = item("deepseek-tiered", {
  fast_multiplier: 2, flex_multiplier: 0.5,
  intervals: [
    { min_tokens: 0, max_tokens: 200000, tier_label: "标准上下文", input_price: 1.5e-6, output_price: 4.5e-6, cache_read_price: 0, cache_write_price: null, cache_write_1h_price: 3e-6, cache_write_12h_price: 6e-6, input_multiplier: 1.2 },
    { min_tokens: 200000, max_tokens: null, tier_label: "长上下文", input_price: 3e-6, output_price: 9e-6, cache_write_price: 4e-6, cache_write_multiplier: 2, per_request_price: 0.01 },
  ],
  time_pricing: { timezone: "Asia/Shanghai", weekdays_only: true, periods: [{ start_time: "09:00:00", end_time: "12:00:00", multiplier: 2 }, { start_time: "14:00:00", end_time: "18:00:00", multiplier: 2 }] },
})
const pricing = {
  station_id: 1, station_name: "规则测试站", currency: "USD", updated_at: new Date().toISOString(),
  public_groups: [{ name: "标准分组", rate_multiplier: 2 }, { name: "专属分组", rate_multiplier: 3 }],
  items: [
    tiered,
    // Identical rules from one channel merge, not duplicate.
    { ...tiered, groups: ["专属分组"] },
    item("deepseek-tiered", { channel_id: 2, channel_name: "专属渠道", groups: ["专属分组"], fast_multiplier: 3 }),
    item("deepseek-time", { time_pricing: { periods: [{ start_time: "09:00:00", end_time: "12:00:00", multiplier: 2 }, { start_time: "14:00:00", end_time: "18:00:00", multiplier: 2 }] } }),
    item("deepseek-midnight", { time_pricing: { timezone: "UTC", weekdays_only: false, periods: [{ start_time: "23:30:01", end_time: "00:00:00", multiplier: 1.5 }] } }),
    item("deepseek-mode-only", { flex_multiplier: 0.25 }),
    item("deepseek-plain"),
    item("deepseek-empty", { intervals: [], time_pricing: { timezone: "UTC", weekdays_only: false, periods: [] } }),
    item("deepseek-invalid", { intervals: [null, "bad", { unknown_rule: 12 }, { min_tokens: "bad", input_price: "bad", cache_read_price: 0 }], time_pricing: { timezone: "Invalid/Zone", periods: [null, { start_time: "25:00", end_time: "26:00", multiplier: 2 }, { start_time: "23:00", end_time: "02:00", multiplier: 2 }] }, fast_multiplier: -1, flex_multiplier: 0 }),
    item("deepseek-unknown-time", { time_pricing: { custom_rule: { multiplier: 3 } } }),
    item("deepseek-unknown-tier", { intervals: [{ min_tokens: 0, max_tokens: null, custom_pricing: 10, input_price: 0 }] }),
    item("deepseek-long-label", { channel_id: 4, channel_name: "long-channel-".repeat(12), intervals: [{ min_tokens: 0, max_tokens: null, tier_label: "long-tier-".repeat(20), input_price: 1e-6 }] }),
    item("deepseek-conflicting", { intervals: [{ min_tokens: 0, max_tokens: 200000, input_price: 1e-6 }, { min_tokens: 100000, max_tokens: null, input_price: 2e-6 }], time_pricing: { periods: [{ start_time: "09:00", end_time: "12:00", multiplier: 2 }, { start_time: "10:00", end_time: "13:00", multiplier: 3 }] } }),
    item("deepseek-bad-bounds", { intervals: [{ min_tokens: -1, max_tokens: 0, input_price: 1e-6 }, { max_tokens: 100, input_price: 2e-6 }] }),
    item("image-tier-labels", { billing_mode: "image", price_source: "group_image_pricing", image_price_1k: 0.03, intervals: [{ min_tokens: 0, max_tokens: null, tier_label: "1K", per_request_price: 0.03 }] }),
  ],
  summary: { models: 11, companies: 1, channels: 4, companies_list: [], platforms: [], billing_modes: ["token"] },
}
const errors = []
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } })
  await context.route("**/api/**", (route) => {
    const pathname = new URL(route.request().url()).pathname
    if (pathname === "/api/public/relay-stations/1/model-pricing") return route.fulfill({ json: { data: pricing } })
    if (pathname === "/api/public/relay-stations/2/model-pricing") return route.fulfill({ json: { data: { ...pricing, station_id: 2, currency: "CNY" } } })
    return route.fulfill({ status: 501, json: { error: `Unexpected mocked endpoint: ${pathname}` } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`${base}/public/model-pricing/1`)
  await page.getByRole("heading", { name: "deepseek-tiered", exact: true }).waitFor()
  const article = (model) => page.locator("article").filter({ has: page.getByRole("heading", { name: model, exact: true }) })
  const tierArticle = article("deepseek-tiered")
  const details = tierArticle.locator("details")
  assert.equal(await details.getAttribute("open"), null, "rules start collapsed")
  await details.locator("summary").focus()
  await page.keyboard.press("Enter")
  assert.notEqual(await details.getAttribute("open"), null, "native summary supports keyboard")
  const channel = details.getByRole("region", { name: "国模定价规则", exact: true })
  assert.equal(await channel.count(), 1, "same channel and rules deduplicated")
  const table = channel.getByRole("table", { name: "国模阶梯价格", exact: true })
  const tableText = await table.innerText()
  for (const expected of ["> 0 且 ≤ 200,000 Token", "> 200,000 Token · 无上限", "$1.5", "$4.5", "$0", "$6", "$0.01", "倍率 ×1.2", "倍率 ×2", "缓存写 · 1 小时", "缓存写 · 12 小时", "—"]) assert.ok(tableText.includes(expected), expected)
  assert.ok(!tableText.includes("$13.5"), "rules do not apply the group multiplier")
  assert.match(await details.innerText(), /未折算分组倍率/)
  const time = channel.getByRole("region", { name: "分时倍率", exact: true })
  for (const expected of ["Asia/Shanghai", "周一至周五", "09:00", "12:00", "14:00", "18:00", "×2"]) assert.ok((await time.innerText()).includes(expected), expected)
  assert.match(await channel.getByRole("region", { name: "Fast / Flex 倍率", exact: true }).innerText(), /Fast.*×2.*Flex.*×0.5/s)
  assert.equal(await details.locator("pre").count(), 0)
  console.log("PASS: tier ranges, per-million / per-request units, zero / absent prices, extended cache, multipliers, channel deduplication and keyboard toggle")

  for (const model of ["deepseek-time", "deepseek-midnight", "deepseek-mode-only", "deepseek-invalid", "deepseek-unknown-time", "deepseek-unknown-tier", "deepseek-long-label", "deepseek-conflicting", "deepseek-bad-bounds", "image-tier-labels"]) await article(model).locator("summary").click()
  const timeOnly = article("deepseek-time")
  assert.match(await timeOnly.innerText(), /时区未提供/)
  assert.match(await timeOnly.innerText(), /适用日期未提供/)
  assert.equal(await timeOnly.getByRole("table").count(), 1, "time-only rules add no tier table")
  assert.match(await article("deepseek-midnight").innerText(), /23:30:01.*24:00/s)
  assert.match(await article("deepseek-midnight").innerText(), /每日/)
  assert.match(await article("deepseek-mode-only").innerText(), /Flex.*×0.25/s)
  for (const model of ["deepseek-plain", "deepseek-empty"]) assert.equal(await article(model).locator("details").count(), 0, "no empty rule panels")
  for (const model of ["deepseek-invalid", "deepseek-unknown-time", "deepseek-unknown-tier"]) {
    const rules = article(model).locator("details")
    assert.match(await rules.innerText(), /无法识别/)
    assert.equal(await rules.locator("pre").count(), 0)
    assert.doesNotMatch(await rules.innerText(), /unknown_rule|custom_rule|custom_pricing|NaN|undefined|\[object Object\]/)
  }
  assert.match(await article("deepseek-conflicting").innerText(), /阶梯区间存在重叠/)
  assert.match(await article("deepseek-conflicting").innerText(), /计费时段存在重叠/)
  assert.match(await article("deepseek-bad-bounds").innerText(), /配置不完整/)
  assert.doesNotMatch(await article("deepseek-bad-bounds").innerText(), /> -1/)
  const imageRules = article("image-tier-labels").locator("details")
  assert.match(await imageRules.innerText(), /USD \/ 次/)
  assert.match(await imageRules.innerText(), /计费档位/)
  assert.match(await imageRules.innerText(), /\$0.03/)
  assert.doesNotMatch(await imageRules.innerText(), /Token 区间|上下文|无上限|百万 Token/)
  console.log("PASS: time-only and mode-only panels, missing metadata, second precision, end-of-day, empty / malformed configurations, overlaps and label-based image tiers")

  const filter = page.getByRole("combobox", { name: "筛选公开分组", exact: true })
  await filter.selectOption("标准分组")
  assert.equal(await tierArticle.getByRole("region", { name: "专属渠道定价规则", exact: true }).count(), 0)
  assert.doesNotMatch(await tierArticle.locator("details").innerText(), /专属分组/)
  await filter.selectOption("专属分组")
  assert.equal(await tierArticle.getByRole("region", { name: "专属渠道定价规则", exact: true }).count(), 1)
  assert.doesNotMatch(await tierArticle.locator("details").innerText(), /标准分组/)
  await filter.selectOption("all")
  // Filtering removes unmatched articles; reopen newly mounted details for screenshots.
  for (const panel of await page.locator("details").all()) {
    if (await panel.getAttribute("open") === null) await panel.locator("summary").click()
  }
  if (artifacts) {
    await mkdir(artifacts, { recursive: true })
    await tierArticle.screenshot({ path: `${artifacts}/pricing-rules-desktop.png` })
    await timeOnly.screenshot({ path: `${artifacts}/pricing-rules-time-only.png` })
  }

  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${width}px document must not overflow`)
    const overflowing = await page.locator("details[open]").evaluateAll((elements) => elements.filter((el) => el.scrollWidth > el.clientWidth).map((el) => el.innerText))
    assert.deepEqual(overflowing, [], `${width}px rules must not overflow`)
    assert.ok(await channel.getByText("标准上下文", { exact: true }).last().isVisible(), "mobile tier cards visible")
    assert.ok(await time.getByText("09:00", { exact: false }).isVisible(), "time card visible")
    if (artifacts) await tierArticle.screenshot({ path: `${artifacts}/pricing-rules-mobile-${width}.png` })
  }
  console.log("PASS: group-scoped rules and 390px / 320px responsive cards without page or rules overflow")

  await page.goto(`${base}/public/model-pricing/2`)
  await page.getByRole("heading", { name: "deepseek-tiered", exact: true }).waitFor()
  await article("deepseek-tiered").locator("summary").click()
  const cny = article("deepseek-tiered").getByRole("region", { name: "国模定价规则", exact: true })
  assert.match(await cny.innerText(), /CNY \/ 百万 Token/)
  assert.match(await cny.innerText(), /¥1.5/)
  assert.doesNotMatch(await cny.innerText(), /\$/)
  assert.deepEqual(errors, [])
  console.log("PASS: CNY formatting and no browser runtime errors")
} finally {
  if (browser) await browser.close()
  await server.close()
}
