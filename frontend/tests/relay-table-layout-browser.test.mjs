// Relay table layout acceptance: isolated Vite page, strict mocks, no real station calls.
import assert from "node:assert/strict"
import { mkdtempSync } from "node:fs"
import { createRequire } from "node:module"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer, transformWithEsbuild } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH || requireProject.resolve("playwright")).href)
const port = Number(process.env.RELAY_TABLE_LAYOUT_TEST_PORT || 3046)
const origin = `http://127.0.0.1:${port}`
const artifacts = mkdtempSync(join(tmpdir(), "gatewayops-relay-tables-"))
const entryID = "\0relay-table-layout-entry.tsx"
const html = '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module" src="/__relay-table-layout-entry.tsx"></script></body></html>'
const entry = `import {StrictMode} from 'react';import {createRoot} from 'react-dom/client';import RelayStationsPage from '/app/relay-stations-page.tsx';import '/app/globals.css';createRoot(document.getElementById('root')).render(<StrictMode><main style={{padding:16,minWidth:0}}><RelayStationsPage /></main></StrictMode>);`
const server = await createServer({
  root, configFile: `${root}/vite.config.ts`, cacheDir: join(artifacts, "vite-cache"),
  server: { host: "127.0.0.1", port, strictPort: true },
  plugins: [{
    name: "relay-table-layout-acceptance", enforce: "pre",
    resolveId(id) { if (id === "/__relay-table-layout-entry.tsx") return entryID },
    async load(id) { if (id === entryID) return (await transformWithEsbuild(entry, "relay-table-layout-entry.tsx", { loader: "tsx", jsx: "automatic" })).code },
    configureServer(vite) { vite.middlewares.use(async (req, res, next) => {
      if (req.url !== "/__relay-table-layout__") return next()
      res.setHeader("Content-Type", "text/html")
      res.end(await vite.transformIndexHtml(req.url, html))
    }) },
  }],
})
const station = { id: 1, name: "表格验收站", base_url: "https://station.invalid", api_key_configured: true, auto_adjust_enabled: false, auto_adjust_no_profit_enabled: false, auto_priority_enabled: false, auto_priority_recall_enabled: false, auto_priority_recall_minutes: 180, new_account_model_type_match_mode: "keyword", new_account_model_type_match_pattern: "", last_synced_at: "2026-01-01T12:00:00Z" }
const groups = Array.from({ length: 16 }, (_, index) => ({ id: index + 1, external_id: 101 + index, name: `分组${String(index + 1).padStart(2, "0")}`, description: index === 0 ? "用于验证窄屏编辑" : "表格布局验收", platform: "openai", status: index % 3 ? "active" : "inactive", is_exclusive: index % 2 === 1, account_types: ["apikey"], model_types: [index % 2 ? "claude" : "gpt"], rate_multiplier: (index + 1) / 2, monitor_enabled: index % 2 === 0, model_pricing_enabled: index % 2 === 0, account_count: 0, priced_account_count: 0, channels: [] }))
const accounts = Array.from({ length: 24 }, (_, index) => ({ external_id: 11 + index, name: `账号${String(index + 1).padStart(2, "0")}${index === 0 ? "-long-name-for-table-truncation-verification" : ""}`, platform: "openai", type: "apikey", status: "active", schedulable: index % 2 === 0, concurrency: 10, current_concurrency: 0, priority: index + 2, pool_mode: false, pool_mode_retry_count: 0, model_type: index % 2 ? "claude" : "gpt", user_charge_excluded: false, created_at: `2026-01-${String(index + 1).padStart(2, "0")}T12:00:00Z`, cost_multiplier: 0.5, latency_samples: [], current_groups: [groups[index % groups.length]], unsafe_groups: [], no_profit_groups: [], downgrade_groups: [], suggested_group_ids: [], risk_state: "unassigned", can_apply: false, cache_hit_rate: 0.5, success_rate: 1 }))
for (const group of groups) group.account_count = accounts.filter((account) => account.current_groups[0].external_id === group.external_id).length
const config = { enabled: false, method: "pelican", model: "candidate", judge_model: "", effort: "none", interval_minutes: 60, pelican_interval_minutes: 60, candy_interval_minutes: 10, history_limit: 100, disable_on_degraded: false, protocol: "", preview_mode: "hover", result_visibility: "show" }
const writes = [], unexpected = [], errors = []
let browser
async function openPage(width, initialModule = "accounts") {
  const context = await browser.newContext({ viewport: { width, height: 1000 }, serviceWorkers: "block" })
  await context.addInitScript((module) => {
    if (!localStorage.getItem("uh_relay_detail_module")) localStorage.setItem("uh_relay_detail_module", module)
    localStorage.setItem("uh_relay_account_risk_open", "true")
    localStorage.setItem("uh_relay_group_management_open", "true")
  }, initialModule)
  await context.route("**/*", async (route) => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname, method = request.method()
    if (url.origin !== origin) {
      unexpected.push(`external ${method} ${url.href}`)
      return route.abort("blockedbyclient")
    }
    if (!path.startsWith("/api/")) return route.continue()
    const respond = (data) => route.fulfill({ json: { data } })
    try {
      const update = path.match(/^\/api\/relay-stations\/1\/groups\/(\d+)$/)
      if (method === "PUT" && update) {
        const group = groups.find((group) => group.external_id === Number(update[1]))
        assert.ok(group)
        const body = request.postDataJSON()
        assert.deepEqual(Object.keys(body).sort(), ["description", "model_types", "name", "rate_multiplier"])
        writes.push({ path, method, body })
        Object.assign(group, body)
        return respond(group)
      }
      assert.equal(method, "GET", `unexpected mutation ${method} ${path}`)
      if (path === "/api/relay-stations") return respond([station])
      if (path === "/api/sync-settings") return respond({ relay_rate_enabled: false, relay_rate_interval_seconds: 10, relay_snapshot_enabled: false, relay_snapshot_interval_seconds: 3600 })
      if (path === "/api/relay-stations/1") return respond({ station, accounts, groups, summary: { account_count: accounts.length, risk_account_count: 0, no_profit_account_count: 0, no_safe_candidate_count: 0, unknown_cost_count: 0, auto_adjusted_count: 0 }, adjustments: [], monitor_channels: [], sync_available: true, range: "today", usage_complete: true, usage_failed_accounts: 0 })
      if (path === "/api/relay-stations/1/usage") return respond({ accounts: [], range: "today", complete: true, failed_accounts: 0 })
      if (path === "/api/relay-stations/1/account-degradation/latest") return respond({})
      if (/^\/api\/relay-stations\/1\/groups\/\d+\/degradation$/.test(path)) return respond({ config, results: [] })
      if (/^\/api\/relay-stations\/1\/accounts\/\d+\/degradation\/config$/.test(path)) return respond(config)
      if (/^\/api\/relay-stations\/1\/accounts\/\d+\/degradation$/.test(path)) return respond({ results: [] })
      if (/^\/api\/relay-stations\/1\/(groups|accounts)\/\d+\/models$/.test(path)) return respond(["candidate"])
      throw new Error(`unexpected endpoint ${method} ${path}`)
    } catch (error) {
      unexpected.push(error.message)
      return route.fulfill({ status: 500, json: { error: "Unexpected mock request" } })
    }
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`${origin}/__relay-table-layout__`)
  await page.locator(initialModule === "accounts" ? "[data-relay-account-scroll] .relay-account-grid" : "[data-group-management-row]").first().waitFor()
  return { context, page }
}
const selectors = { accounts: "[data-relay-account-scroll]", groups: "[data-relay-group-scroll]" }
async function assertTable(page, module, expectedRows) {
  const selector = selectors[module]
  const columns = module === "accounts" ? 22 : 14
  const minWidth = module === "accounts" ? 3136 : 1920
  const scroll = page.locator(selector)
  await scroll.waitFor()
  await page.waitForFunction(({ selector, columns }) => {
    const row = document.querySelector(selector)?.querySelector(".relay-account-grid, .relay-group-grid")
    return row && getComputedStyle(row).gridTemplateColumns.split(" ").length === columns
  }, { selector, columns })
  await scroll.evaluate((el) => { el.scrollLeft = 0; el.scrollTop = 0 })
  const metrics = await scroll.evaluate((el, module) => {
    const grids = [...el.querySelectorAll(module === "accounts" ? ".relay-account-grid" : ".relay-group-grid")]
    const grid = (node) => ({ display: getComputedStyle(node).display, columns: getComputedStyle(node).gridTemplateColumns.split(" "), height: node.getBoundingClientRect().height, width: node.getBoundingClientRect().width, cells: [...node.children].map((cell) => { const style = getComputedStyle(cell), rect = cell.getBoundingClientRect(); return { x: rect.x, width: rect.width, height: rect.height, display: style.display } }) })
    return { width: el.clientWidth, scrollWidth: el.scrollWidth, documentWidth: document.documentElement.scrollWidth, viewport: innerWidth, grids: grids.map(grid), articles: el.querySelectorAll("article").length }
  }, module)
  assert.equal(metrics.grids.length, expectedRows + 1, `${module}: header and data rows`)
  assert.equal(metrics.articles, 0, `${module}: no responsive cards`)
  assert.ok(metrics.scrollWidth > metrics.width, `${module}: internal horizontal overflow`)
  assert.ok(metrics.documentWidth <= metrics.viewport + 1, `${module}: page must not overflow (${metrics.documentWidth}/${metrics.viewport})`)
  const header = metrics.grids[0]
  for (const [index, grid] of metrics.grids.entries()) {
    assert.equal(grid.display, "grid")
    assert.equal(grid.columns.length, columns)
    assert.equal(grid.cells.length, columns)
    assert.ok(grid.width >= minWidth)
    assert.ok(grid.height < 240, `${module}: row ${index} must not become a stacked card`)
    assert.ok(Math.abs(grid.width - header.width) <= 1)
    for (let column = 0; column < columns; column++) {
      const cell = grid.cells[column]
      assert.notEqual(cell.display, "none", `${module}: hidden column ${column}`)
      assert.ok(cell.height > 0 && cell.width > 0)
      assert.ok(Math.abs(cell.x - header.cells[column].x) <= 1.5, `${module}: row ${index} column ${column} alignment`)
      assert.ok(Math.abs(cell.width - header.cells[column].width) <= 1.5, `${module}: column ${column} width`)
    }
  }
  await scroll.evaluate((el) => { el.scrollLeft = el.scrollWidth; el.scrollTop = 100 })
  const fixed = await scroll.evaluate((el, module) => {
    const grids = el.querySelectorAll(module === "accounts" ? ".relay-account-grid" : ".relay-group-grid")
    const header = grids[0], row = grids[1], rect = el.getBoundingClientRect()
    return { scrollLeft: el.scrollLeft, top: header.getBoundingClientRect().top - rect.top, firstPosition: getComputedStyle(row.children[0]).position, lastPosition: getComputedStyle(row.lastElementChild).position, lastRight: row.lastElementChild.getBoundingClientRect().right - rect.right, firstLeft: row.children[0].getBoundingClientRect().left - rect.left }
  }, module)
  assert.ok(fixed.scrollLeft > 0)
  assert.ok(Math.abs(fixed.top) <= 1, `${module}: sticky header follows vertical scroll`)
  if (page.viewportSize().width >= 1024) {
    assert.equal(fixed.lastPosition, "sticky", `${module}: desktop actions stay fixed`)
    assert.ok(Math.abs(fixed.lastRight) <= 1)
    if (module === "accounts") { assert.equal(fixed.firstPosition, "sticky"); assert.ok(Math.abs(fixed.firstLeft) <= 1) }
  } else {
    assert.notEqual(fixed.firstPosition, "sticky", `${module}: mobile columns must scroll`)
    assert.notEqual(fixed.lastPosition, "sticky", `${module}: mobile actions must scroll`)
  }
  await scroll.evaluate((el) => { el.scrollLeft = 0; el.scrollTop = 0 })
}
async function switchModule(page, module) {
  await page.getByRole("tab", { name: module === "accounts" ? "账号列表" : "分组管理", exact: true }).click()
  await page.locator(selectors[module]).waitFor()
}
async function shot(page, module, label) {
  const table = page.locator(selectors[module])
  await table.scrollIntoViewIfNeeded()
  await page.screenshot({ path: join(artifacts, `${label}-${module}.png`) })
}
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const { page, context } = await openPage(1440)
  for (const width of [1440, 1023, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 })
    await switchModule(page, "accounts")
    await assertTable(page, "accounts", accounts.length)
    if (width === 1440 || width === 390) await shot(page, "accounts", String(width))
    await switchModule(page, "groups")
    await assertTable(page, "groups", groups.length)
    if (width === 1440 || width === 390) await shot(page, "groups", String(width))
    console.log(`PASS: ${width}px account/group columns, alignment, internal scrolling and sticky headers`)
  }
  await switchModule(page, "accounts")
  const accountRows = page.locator(`${selectors.accounts} .relay-account-grid`).filter({ has: page.getByRole("checkbox", { name: /选择账号 / }) })
  await page.getByRole("textbox", { name: "按账号名称筛选", exact: true }).fill(accounts[0].name)
  assert.equal(await accountRows.count(), 1)
  const checkbox = page.getByRole("checkbox", { name: `选择账号 ${accounts[0].name}`, exact: true })
  await checkbox.click()
  await page.getByText("已选 1 个账号", { exact: true }).waitFor()
  await checkbox.click()
  await page.getByRole("textbox", { name: "按账号名称筛选", exact: true }).fill("")
  await page.getByRole("button", { name: "优先级排序", exact: true }).click()
  assert.equal(await accountRows.first().getByRole("checkbox").getAttribute("aria-label"), `选择账号 ${accounts[0].name}`)
  await accountRows.first().getByRole("button", { name: `配置${accounts[0].name}降智测试`, exact: true }).click()
  await page.getByRole("dialog").waitFor()
  await page.getByRole("dialog").getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  console.log("PASS: narrow-screen account filtering, selection, sorting and action-column configuration")

  await switchModule(page, "groups")
  const groupRows = page.locator("[data-group-management-row]")
  await page.getByRole("combobox", { name: "筛选分组类型", exact: true }).click()
  await page.getByRole("option", { name: "专属", exact: true }).click()
  assert.equal(await groupRows.count(), groups.length / 2)
  await page.getByRole("combobox", { name: "筛选分组类型", exact: true }).click()
  await page.getByRole("option", { name: "全部类型", exact: true }).click()
  await page.getByRole("button", { name: "倍率排序", exact: true }).click()
  assert.equal(await groupRows.first().getByRole("textbox").first().inputValue(), groups.at(-1).name)
  const name = groups[0].name
  await page.getByRole("button", { name: `展开${name}中的账号`, exact: true }).click()
  const expanded = page.getByRole("table", { name: `${name}的关联账号`, exact: true })
  await expanded.waitFor()
  assert.equal(await expanded.getByRole("columnheader").count(), 7)
  assert.equal(await expanded.locator("tbody tr").count(), groups[0].account_count)
  await page.getByRole("button", { name: `收起${name}中的账号`, exact: true }).click()
  await page.getByRole("textbox", { name: `${name}分组描述`, exact: true }).fill("窄屏表格保存验收")
  const saved = page.waitForResponse((response) => response.url() === `${origin}/api/relay-stations/1/groups/101` && response.request().method() === "PUT")
  await page.getByRole("button", { name: `保存${name}`, exact: true }).click()
  await saved
  await page.waitForFunction(() => document.querySelector('[aria-label="保存分组01"]')?.disabled === true)
  assert.equal(writes.length, 1)
  assert.equal(writes[0].body.description, "窄屏表格保存验收")
  await page.getByRole("button", { name: `配置${name}降智监控`, exact: true }).click()
  await page.getByRole("dialog").waitFor()
  await page.getByRole("dialog").getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  console.log("PASS: narrow-screen group filtering, sorting, nested account table, edit/save and action-column configuration")
  await context.close()

  for (const width of [390, 320]) {
    const { page: cold, context: coldContext } = await openPage(width, "groups")
    await assertTable(cold, "groups", groups.length)
    await switchModule(cold, "accounts")
    await assertTable(cold, "accounts", accounts.length)
    await cold.reload()
    await assertTable(cold, "accounts", accounts.length)
    await coldContext.close()
    console.log(`PASS: ${width}px cold loading, module persistence and reload always use tables`)
  }
  assert.deepEqual(errors, [])
  assert.deepEqual(unexpected, [])
  assert.equal(writes.length, 1)
  console.log("PASS: no browser errors, external traffic, real detections, deletes or unexpected mutations")
  console.log(`Screenshots: ${artifacts}`)
} catch (error) {
  console.error("Unexpected requests:", unexpected)
  console.error("Browser errors:", errors)
  console.error(`Artifacts: ${artifacts}`)
  throw error
} finally {
  await browser?.close()
  await server.close()
}
