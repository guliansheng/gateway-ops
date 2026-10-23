// Mocked group settings acceptance: no real configuration, history deletion or model calls.
import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer, transformWithEsbuild } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH || requireProject.resolve("playwright")).href)
const port = Number(process.env.GROUP_SETTINGS_TEST_PORT || 3044)
const entryID = "\0group-settings-acceptance.tsx"
const html = '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module" src="/__group-settings-entry.tsx"></script></body></html>'
const entry = `
import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { GroupManagementTest } from '/app/relay-stations-page.tsx'
import { DegradationPromptSettingsButton } from '/components/monitor/degradation-prompt-settings.tsx'
import { AccountDegradationTestButton } from '/components/monitor/account-degradation-test.tsx'
import { GroupDegradationStatus } from '/components/monitor/group-degradation-settings.tsx'
import { DegradationUsageButton } from '/components/monitor/degradation-usage-dialog.tsx'
import { DynamicMonitorDialog } from '/components/relay/dynamic-monitor-dialog.tsx'
import '/app/globals.css'
const initial = ['甲组', '乙组'].map((name, index) => ({ id:index+1,external_id:101+index,name,description:'',platform:'openai',status:'active',is_exclusive:false,account_types:[],model_types:['gpt'],rate_multiplier:1,monitor_enabled:true,model_pricing_enabled:true,account_count:0,priced_account_count:0,channels:[] }))
function Harness() {
 const [stationID,setStationID]=useState(1)
 const [groups,setGroups]=useState(initial)
 async function update(id,patch) { await fetch('/api/relay-stations/'+stationID+'/groups/'+id,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(patch)});setGroups(current=>current.map(g=>g.external_id===id?{...g,...patch}:g)) }
 const [dynamicOpen,setDynamicOpen]=useState(false)
 const [dynamic,setDynamic]=useState({enabled:true,groups:[{name:'测试监测组',first_token_seconds:10,consecutive_count:2,single_timeout_seconds:0,account_external_ids:[12]}],error_groups:[{id:'error-one',name:'错误组',status_codes:[403],threshold:3,lookback_minutes:10,allow_auto_open:false,auto_open_minutes:0}],min_accounts_per_group:0,allow_auto_open:false,auto_open_minutes:0})
 const dynamicAccounts=[{external_id:11,name:'测试账号',platform:'openai',schedulable:true,current_groups:[]},{external_id:12,name:'另一账号',platform:'openai',schedulable:true,current_groups:[]}]
 return <div style={{padding:24}}><button onClick={()=>setStationID(value=>value===1?2:1)}>切换测试站点</button><button onClick={()=>setDynamicOpen(true)}>打开动态监测</button><DynamicMonitorDialog open={dynamicOpen} busy={false} config={dynamic} state={{closed_accounts:[]}} accounts={dynamicAccounts} groups={[]} onOpenChange={setDynamicOpen} onSave={value=>{setDynamic(value);setDynamicOpen(false)}} /><div data-station-tools><DegradationPromptSettingsButton stationID={stationID} /><AccountDegradationTestButton stationID={stationID} accountID={11} accountName="测试账号" /><DegradationUsageButton stationID={stationID} /></div><div data-public-status><GroupDegradationStatus stationID={1} groupID={101} /></div><div data-public-status-show><GroupDegradationStatus stationID={1} groupID={102} /></div><GroupManagementTest overview={{station:{id:stationID,name:'测试站'},groups,accounts:[]}} busy={false} refreshing={false} testingGroupID={null} deletingGroupID={null} onUpdate={update} onOrderChange={async()=>{}} onTest={()=>{}} onDelete={()=>{}} onRefresh={()=>{}} /></div>
}
createRoot(document.getElementById('root')).render(<Harness />)
`
const server = await createServer({ root, configFile: `${root}/vite.config.ts`, server: { host: "127.0.0.1", port, strictPort: true }, plugins: [{
  name: "group-settings-acceptance", enforce: "pre",
  resolveId(id) { if (id === "/__group-settings-entry.tsx") return entryID },
  async load(id) { if (id === entryID) return (await transformWithEsbuild(entry, "group-settings-acceptance.tsx", { loader: "tsx", jsx: "automatic" })).code },
  transform(code, id) { if (id.split("?")[0] === `${root}/app/relay-stations-page.tsx`) return `${code}\nexport { GroupManagement as GroupManagementTest };` },
  configureServer(server) { server.middlewares.use(async (req, res, next) => { if (req.url === "/__group-settings__") { res.setHeader("Content-Type", "text/html"); res.end(await server.transformIndexHtml(req.url, html)); return } next() }) },
}] })
const defaultConfig = { enabled: false, method: "candy", model: "candidate", judge_model: "", effort: "none", interval_minutes: 30, preview_mode: "hover", result_visibility: "show" }
const configs = new Map([["1:101", { ...defaultConfig }], ["1:102", { ...defaultConfig }], ["2:101", { ...defaultConfig }]])
const record = (id, patch = {}) => ({ id, method: "candy", model: "candidate", judge_model: "candidate", effort: "none", status: "normal", reason: "通过检测", answer: "21", created_at: `2026-01-01T12:0${id}:00Z`, used_account_id: 501, used_account_name: "上游账号A", input_tokens: 120, output_tokens: 30, base_cost: 0.002, cost_multiplier: 0.8, actual_cost: 0.0016, ...patch })
const histories = new Map([["1:101", [record(3), record(2), record(1)]], ["1:102", [record(4)]]])
const prompts = new Map([["1", { candy_prompt: "测试糖果题", candy_expected_answer: "21", pelican_prompt: "测试鹈鹕 HTML / SVG 提示词", pelican_reference: "测试鹈鹕参考标准" }], ["2", { candy_prompt: "另一站题目", candy_expected_answer: "42", pelican_prompt: "另一站 HTML / SVG 提示词", pelican_reference: "另一站鹈鹕参考标准" }]])
const accountHistories = new Map()
const writes = []
const errors = []
let failPromptRead = true
let failPromptWrite = true
let failHistoryClear = true
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1840, height: 1080 } })
  await context.route("**/api/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    const promptMatch = url.pathname.match(/^\/api\/relay-stations\/(\d+)\/degradation-prompts$/)
    const publicMatch = url.pathname.match(/^\/api\/public\/relay-stations\/(\d+)\/groups\/(\d+)\/degradation$/)
    if (publicMatch) {
      const key = `${publicMatch[1]}:${publicMatch[2]}`
      const hidden = { used_account_id: 0, used_account_name: "", input_tokens: 0, output_tokens: 0, base_cost: 0, cost_multiplier: 0, actual_cost: 0 }
      const visibility = publicMatch[2] === "102" ? "show" : "hide"
      // The hide group also carries a failed call: public bars must drop it.
      const results = [{ ...record(2), ...hidden, status: "normal" }, { ...record(1), ...hidden, status: "degraded", reason: "未通过检测" }]
      if (visibility === "hide") results.unshift({ ...record(3), ...hidden, status: "error", reason: "模拟上游调用失败" })
      return route.fulfill({ json: { data: { config: { ...(configs.get(key) || defaultConfig), enabled: true, result_visibility: visibility }, results } } })
    }
    const accountMatch = url.pathname.match(/^\/api\/relay-stations\/(\d+)\/accounts\/(\d+)\/(models|degradation)(?:\/(run|history))?$/)
    if (accountMatch) {
      if (accountMatch[3] === "models") return route.fulfill({ json: { data: ["candidate", "reviewer"] } })
      const key = `${accountMatch[1]}:${accountMatch[2]}`
      if (accountMatch[4] === "history") {
        assert.equal(method, "DELETE")
        writes.push({ path: url.pathname, method })
        accountHistories.set(key, [])
        return route.fulfill({ json: { data: { results: [] } } })
      }
      if (accountMatch[4] === "run") {
        const body = request.postDataJSON()
        const result = { ...record(8), method: body.method, status: "error", reason: "模拟上游 HTTP 403" }
        accountHistories.set(key, [result, ...(accountHistories.get(key) || [])])
        writes.push({ path: url.pathname, method, body })
        // Delayed on purpose: the dialog must stay closable while the test runs.
        await new Promise((done) => setTimeout(done, 400))
        return route.fulfill({ json: { data: result } })
      }
      return route.fulfill({ json: { data: { results: accountHistories.get(key) || [] } } })
      return route.fulfill({ json: { data: { results: accountHistories.get(key) || [] } } })
    }
    if (promptMatch) {
      if (method === "GET") {
        if (failPromptRead) { failPromptRead = false; return route.fulfill({ status: 503, json: { error: "模拟设置读取失败" } }) }
        return route.fulfill({ json: { data: prompts.get(promptMatch[1]) } })
      }
      assert.equal(method, "PUT")
      const body = request.postDataJSON()
      writes.push({ path: url.pathname, method, body })
      if (failPromptWrite) { failPromptWrite = false; return route.fulfill({ status: 503, json: { error: "模拟设置保存失败" } }) }
      prompts.set(promptMatch[1], body)
      return route.fulfill({ json: { data: body } })
    }
    const usageMatch = url.pathname.match(/^\/api\/relay-stations\/(\d+)\/degradation-usage$/)
    if (usageMatch) {
      const scope = url.searchParams.get("scope") || "all"
      const page = Number(url.searchParams.get("page") || 1)
      const pageSize = Number(url.searchParams.get("page_size") || 20)
      const items = [
        ...(histories.get(`${usageMatch[1]}:101`) || []).map((item) => ({ ...item, scope: "group", scope_id: 101, scope_name: "甲组" })),
        ...(accountHistories.get(`${usageMatch[1]}:11`) || []).map((item) => ({ ...item, scope: "account", scope_id: 11, scope_name: "测试账号" })),
      ].filter((item) => scope === "all" || item.scope === scope)
      const sum = (field) => items.reduce((total, item) => total + (item[field] || 0), 0)
      const totals = { runs: items.length, input_tokens: sum("input_tokens"), output_tokens: sum("output_tokens"), base_cost: sum("base_cost"), actual_cost: sum("actual_cost") }
      return route.fulfill({ json: { data: { items: items.slice((page - 1) * pageSize, page * pageSize), totals, total: items.length, page, page_size: pageSize, pages: Math.max(1, Math.ceil(items.length / pageSize)) } } })
    }
    const match = url.pathname.match(/^\/api\/relay-stations\/(\d+)\/groups\/(\d+)(?:\/(models|degradation)(?:\/(history|run))?)?$/)
    assert.ok(match, `unexpected endpoint ${url.pathname}`)
    const key = `${match[1]}:${match[2]}`
    if (match[3] === "models") return route.fulfill({ json: { data: ["candidate", "reviewer"] } })
    if (!match[3]) { assert.equal(method, "PUT"); writes.push({ path: url.pathname, method, body: request.postDataJSON() }); return route.fulfill({ json: { data: request.postDataJSON() } }) }
    if (match[4] === "run") {
      const runMethod = url.searchParams.get("method") || "candy"
      const result = { ...record(9), method: runMethod, created_at: "2026-01-01T12:09:00Z" }
      histories.set(key, [result, ...(histories.get(key) || [])].slice(0, configs.get(key)?.history_limit || 100))
      writes.push({ path: url.pathname, method })
      await new Promise((done) => setTimeout(done, 400))
      return route.fulfill({ json: { data: result } })
    }
    if (match[4] === "history") {
      assert.equal(method, "DELETE")
      writes.push({ path: url.pathname, method })
      if (failHistoryClear) { failHistoryClear = false; return route.fulfill({ status: 409, json: { error: "检测正在运行，请稍后清空" } }) }
      histories.set(key, [])
      return route.fulfill({ json: { data: { config: configs.get(key), results: [] } } })
    }
    if (method === "PUT") { const body = request.postDataJSON(); configs.set(key, body); histories.set(key, (histories.get(key) || []).slice(0, body.history_limit)); writes.push({ path: url.pathname, method, body }) }
    return route.fulfill({ json: { data: { config: configs.get(key) || defaultConfig, results: histories.get(key) || [] } } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${port}/__group-settings__`)
  const settings = page.getByRole("button", { name: "降智测试设置", exact: true })
  await settings.waitFor()
  const tools = page.locator("[data-station-tools]")
  assert.equal(await tools.locator("button").first().getAttribute("aria-label"), "降智测试设置")
  const filterBounds = await tools.boundingBox()
  const settingsBounds = await settings.boundingBox()
  assert.equal(settingsBounds.x, filterBounds.x)
  const groupHeadings = await page.locator("[data-group-management-header]").locator(":scope > *").allTextContents()
  assert.equal(groupHeadings.indexOf("最近降智检测") + 1, groupHeadings.indexOf("类型"))
  const latestDetection = page.getByRole("button", { name: "查看甲组最近降智检测：糖果 candidate", exact: true })
  await latestDetection.waitFor()
  assert.match(await latestDetection.textContent(), /糖果 · candidate/)
  assert.equal(await page.getByRole("button", { name: "查看乙组最近降智检测：糖果 candidate", exact: true }).count(), 1)
  console.log("PASS: latest group detection column sits before the type column")
  const latestCellBox = await latestDetection.locator("xpath=../..").boundingBox()
  const latestHeaderBox = await page.locator("[data-group-management-header]").locator(":scope > *").nth(6).boundingBox()
  assert.ok(Math.abs(latestCellBox.x - latestHeaderBox.x) < 1 && Math.abs(latestCellBox.width - latestHeaderBox.width) < 1, "latest detection cell matches its header column")
  const priceSwitch = page.getByRole("switch", { name: "甲组关闭模型价格", exact: true })
  await priceSwitch.click()
  await page.getByRole("switch", { name: "甲组开启模型价格", exact: true }).waitFor()
  assert.deepEqual(writes.at(-1).body, { model_pricing_enabled: false })
  assert.equal(await page.getByRole("switch", { name: "甲组关闭公开监控", exact: true }).isChecked(), true)
  await page.getByRole("switch", { name: "甲组开启模型价格", exact: true }).click()
  await page.getByRole("switch", { name: "甲组切换为专属分组", exact: true }).click()
  await page.getByRole("switch", { name: "甲组切换为公开分组", exact: true }).waitFor()
  assert.deepEqual(writes.at(-1).body, { is_exclusive: true, monitor_enabled: false, model_pricing_enabled: false })
  await page.getByRole("switch", { name: "甲组切换为公开分组", exact: true }).click()
  await page.getByRole("switch", { name: "甲组关闭模型价格", exact: true }).waitFor()
  assert.deepEqual(writes.at(-1).body, { is_exclusive: false, monitor_enabled: true, model_pricing_enabled: true })
  console.log("PASS: left settings entry, independent price toggle and public/exclusive defaults")

  await settings.click()
  let dialog = page.getByRole("dialog", { name: "降智测试设置", exact: true })
  await dialog.getByRole("button", { name: "重新读取设置" }).click()
  await dialog.getByLabel("糖果题提示词", { exact: true }).waitFor()
  assert.equal(await dialog.locator("textarea").count(), 4)
  assert.equal(await dialog.getByLabel("糖果题标准答案", { exact: true }).inputValue(), "21")
  assert.equal(await dialog.getByLabel(/鹈鹕.*标准答案/).count(), 0)
  await dialog.getByLabel("糖果题提示词", { exact: true }).fill("自定义糖果题：最少几颗？")
  await dialog.getByLabel("糖果题标准答案", { exact: true }).fill("42")
  await dialog.getByLabel("鹈鹕测试提示词", { exact: true }).fill("自定义鹈鹕 HTML / SVG 动画")
  await dialog.getByLabel("鹈鹕测试参考标准", { exact: true }).fill("自定义鹈鹕参考标准")
  await dialog.getByRole("button", { name: "保存设置", exact: true }).click()
  await dialog.getByText("模拟设置保存失败", { exact: true }).waitFor()
  assert.equal(await dialog.getByLabel("糖果题标准答案", { exact: true }).inputValue(), "42")
  await dialog.getByRole("button", { name: "保存设置", exact: true }).click()
  await dialog.getByText("测试设置已保存", { exact: true }).waitFor()
  assert.deepEqual(prompts.get("1"), { candy_prompt: "自定义糖果题：最少几颗？", candy_expected_answer: "42", pelican_prompt: "自定义鹈鹕 HTML / SVG 动画", pelican_reference: "自定义鹈鹕参考标准" })
  if (process.env.GROUP_SETTINGS_ARTIFACTS) await page.screenshot({ path: `${process.env.GROUP_SETTINGS_ARTIFACTS}/standards-desktop.png`, fullPage: true })
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  console.log("PASS: four prompt fields, split pelican prompt/reference, no pelican answer, read retry, failed-save preservation and station-scoped save")

  await page.getByRole("button", { name: "配置甲组降智监控", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "分组降智监测配置", exact: true })
  const historyLimit = dialog.getByLabel("历史最多保留（条）", { exact: true })
  await historyLimit.waitFor()
  assert.equal(await historyLimit.inputValue(), "20")
  await historyLimit.fill("2")
  await dialog.getByLabel("鹈鹕检测间隔（分钟）").fill("60")
  await dialog.getByLabel("糖果检测间隔（分钟）").fill("10")
  assert.ok(await dialog.getByRole("button", { name: "清空历史", exact: true }).isDisabled())
  await dialog.getByRole("button", { name: "保存配置", exact: true }).click()
  await dialog.getByText("配置已保存", { exact: true }).waitFor()
  assert.equal(configs.get("1:101").history_limit, 2)
  assert.equal(configs.get("1:101").pelican_interval_minutes, 60)
  assert.equal(configs.get("1:101").candy_interval_minutes, 10)
  assert.equal(await dialog.locator("[data-degradation-history] button").count(), 3)
  assert.match(await dialog.locator("[data-degradation-history] button").nth(1).textContent(), /调用账号 上游账号A/)
  await dialog.getByRole("button", { name: "清空历史", exact: true }).click()
  let confirm = page.getByRole("alertdialog")
  await confirm.waitFor()
  const beforeDeletes = writes.filter((write) => write.method === "DELETE").length
  await confirm.getByRole("button", { name: "取消", exact: true }).click()
  assert.equal(writes.filter((write) => write.method === "DELETE").length, beforeDeletes)
  await dialog.getByRole("button", { name: "清空历史", exact: true }).click()
  confirm = page.getByRole("alertdialog")
  await confirm.getByRole("button", { name: "确认清空", exact: true }).click()
  await confirm.getByText("检测正在运行，请稍后清空", { exact: true }).waitFor()
  assert.equal(histories.get("1:101").length, 2)
  await confirm.getByRole("button", { name: "确认清空", exact: true }).click()
  await dialog.getByText("检测历史已清空", { exact: true }).waitFor()
  assert.ok(await dialog.getByText("暂无检测记录", { exact: true }).isVisible())
  assert.equal(histories.get("1:102").length, 1)
  assert.equal(configs.get("1:101").history_limit, 2)
  assert.ok(writes.filter((write) => write.method === "DELETE").every((write) => write.path === "/api/relay-stations/1/groups/101/degradation/history"))
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  console.log("PASS: legacy history limit, save/prune, cancel without DELETE, failed-clear preservation, scoped clear and unchanged configuration")

  await page.getByRole("button", { name: "测试测试账号降智能力", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "账号降智测试", exact: true })
  await dialog.getByLabel("测试题目", { exact: true }).selectOption("candy")
  await dialog.getByRole("combobox", { name: "调用模型", exact: true }).click()
  await page.getByRole("option", { name: "candidate", exact: true }).click()
  await dialog.getByRole("button", { name: "立即测试", exact: true }).click()
  await dialog.getByRole("status").filter({ hasText: "测试完成，记录已保存" }).waitFor()
  assert.equal(accountHistories.get("1:11").length, 1)
  assert.equal(writes.at(-1).body.method, "candy")
  if (process.env.GROUP_SETTINGS_ARTIFACTS) await page.screenshot({ path: `${process.env.GROUP_SETTINGS_ARTIFACTS}/account-degradation-desktop.png`, fullPage: true })
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("button", { name: "测试测试账号降智能力", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "账号降智测试", exact: true })
  await dialog.getByRole("button", { name: /糖果测试.*candidate/ }).waitFor()
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  console.log("PASS: manual account test, upstream error persisted and history reload")

  // 账号降智记录可清空
  await page.getByRole("button", { name: "测试测试账号降智能力", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "账号降智测试", exact: true })
  await dialog.getByRole("button", { name: /糖果测试.*candidate/ }).waitFor()
  await dialog.getByRole("button", { name: "清空记录", exact: true }).click()
  let confirmDialog = page.getByRole("alertdialog")
  await confirmDialog.getByRole("button", { name: "确认清空", exact: true }).click()
  await dialog.getByText("测试记录已清空", { exact: true }).waitFor()
  assert.ok(await dialog.getByText("暂无测试记录", { exact: true }).isVisible())
  assert.ok(writes.some((write) => write.method === "DELETE" && write.path === "/api/relay-stations/1/accounts/11/degradation/history"))
  console.log("PASS: account degradation history can be cleared")

  // 关闭弹窗后账号测试在后台继续，重开显示运行中并自动补记录
  await dialog.getByRole("button", { name: "立即测试", exact: true }).click()
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("button", { name: "测试测试账号降智能力", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "账号降智测试", exact: true })
  await dialog.getByRole("button", { name: "测试中", exact: true }).waitFor({ timeout: 3000 })
  await dialog.getByRole("status").filter({ hasText: "测试完成，记录已保存" }).waitFor()
  assert.match(await dialog.locator("article").first().textContent(), /鹈鹕测试 · candidate/)
  console.log("PASS: closing the dialog keeps the account run running in the background")

  // 关闭弹窗后分组检测在后台继续，重开显示运行中并自动补记录
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("button", { name: "配置甲组降智监控", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "分组降智监测配置", exact: true })
  await dialog.getByRole("switch", { name: "未开启", exact: true }).click()
  await dialog.getByRole("button", { name: "保存配置", exact: true }).click()
  await dialog.getByText("配置已保存", { exact: true }).waitFor()
  await dialog.getByRole("button", { name: "立即检测", exact: true }).click()
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  await page.getByRole("button", { name: "配置甲组降智监控", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "分组降智监测配置", exact: true })
  await dialog.getByRole("button", { name: "检测中", exact: true }).waitFor({ timeout: 3000 })
  await dialog.getByText("检测完成，结果已保存", { exact: true }).waitFor()
  const groupRunRow = dialog.locator("[data-degradation-history] button").nth(1)
  assert.match(await groupRunRow.textContent(), /调用账号 上游账号A/)
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  console.log("PASS: closing the dialog keeps the group run running in the background")

  // 费用统计：分页表格、范围筛选与实际扣费合计
  accountHistories.set("1:11", Array.from({ length: 24 }, (_, index) => ({ ...record(30 + index), created_at: `2026-01-02T00:${String(index).padStart(2, "0")}:00Z` })))
  await page.getByRole("button", { name: "降智测试费用统计", exact: true }).click()
  const usageDialog = page.getByRole("dialog", { name: "降智测试费用统计", exact: true })
  await usageDialog.getByText("共 25 条记录", { exact: true }).waitFor()
  assert.equal(await usageDialog.locator("tbody tr").count(), 20)
  assert.match(await usageDialog.locator("tbody tr").first().textContent(), /上游账号A/)
  assert.equal(await usageDialog.getByText("$0.04", { exact: true }).count(), 1)
  assert.ok(await usageDialog.getByRole("button", { name: "上一页", exact: true }).isDisabled())
  await usageDialog.getByRole("button", { name: "下一页", exact: true }).click()
  await usageDialog.getByText("第 2 / 2 页", { exact: true }).waitFor()
  assert.equal(await usageDialog.locator("tbody tr").count(), 5)
  await usageDialog.getByRole("combobox", { name: "筛选降智测试范围", exact: true }).click()
  await page.getByRole("option", { name: "仅账号测试", exact: true }).click()
  await usageDialog.getByText("共 24 条记录", { exact: true }).waitFor()
  await usageDialog.getByRole("button", { name: "Close", exact: true }).click()
  console.log("PASS: degradation usage dialog pages, filters and totals the real charge")

  // 公开监控在“不展示测试结果”时，悬浮说明仍给出通过情况
  const publicBars = page.locator("[data-public-status] button")
  await publicBars.first().waitFor()
  assert.equal(await publicBars.count(), 2)
  await publicBars.first().hover()
  await page.getByText("疑似降智", { exact: true }).last().waitFor()
  assert.ok(await page.getByText("未通过检测，疑似降智", { exact: true }).last().isVisible())
  await publicBars.last().hover()
  await page.getByText("通过检测", { exact: true }).last().waitFor()
  console.log("PASS: hidden public results still explain pass or fail on hover")

  // “展示预览”时悬浮说明测试项目；调用失败的记录不展示
  const showBars = page.locator("[data-public-status-show] button")
  await showBars.first().waitFor()
  assert.equal(await showBars.count(), 2)
  await showBars.first().hover()
  const previewCard = page.locator("[data-slot='hover-card-content']").last()
  await previewCard.getByText("测试项目：糖果测试 · candidate", { exact: true }).waitFor()
  await previewCard.getByText("疑似降智", { exact: true }).waitFor()
  console.log("PASS: preview mode names the test item and hides failed calls")
  await page.getByRole("button", { name: "打开动态监测", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "账号动态监测", exact: true })
  await dialog.getByRole("checkbox").first().click()
  assert.ok(await dialog.getByRole("switch", { name: "启用报错组 1", exact: true }).isChecked())
  await dialog.getByRole("switch", { name: "启用报错组 1", exact: true }).click()
  await dialog.getByRole("button", { name: "保存规则", exact: true }).click()
  await page.getByRole("button", { name: "打开动态监测", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "账号动态监测", exact: true })
  assert.ok(await dialog.getByRole("checkbox").first().isChecked())
  assert.equal(await dialog.getByRole("switch", { name: "启用报错组 1", exact: true }).isChecked(), false)
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  console.log("PASS: protected account selection and disabled error group survive save/reopen")
  await page.getByRole("button", { name: "切换测试站点", exact: true }).click()
  await settings.click()
  dialog = page.getByRole("dialog", { name: "降智测试设置", exact: true })
  await dialog.getByLabel("糖果题提示词", { exact: true }).waitFor()
  assert.equal(await dialog.getByLabel("糖果题提示词", { exact: true }).inputValue(), "另一站题目")
  await page.setViewportSize({ width: 390, height: 844 })
  await page.waitForFunction(() => { const element = document.querySelector('[role="dialog"]'); if (!element) return false; const bounds = element.getBoundingClientRect(); return bounds.x >= 0 && bounds.width <= innerWidth && bounds.height <= innerHeight }, { timeout: 5000 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "mobile settings overflow")
  const dialogBounds = await dialog.boundingBox()
  assert.ok(dialogBounds.x >= 0 && dialogBounds.width <= 390 && dialogBounds.height <= 844)
  if (process.env.GROUP_SETTINGS_ARTIFACTS) await page.screenshot({ path: `${process.env.GROUP_SETTINGS_ARTIFACTS}/standards-mobile.png`, fullPage: true })
  assert.deepEqual(errors, [])
  console.log("PASS: station switching, mobile dialog bounds, no model calls and no runtime errors")
} catch (error) {
  console.error("Browser runtime errors:", errors)
  throw error
} finally {
  if (browser) await browser.close()
  await server.close()
}
