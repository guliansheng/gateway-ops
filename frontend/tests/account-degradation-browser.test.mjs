// Account list acceptance with mocked APIs only: no real stations or model calls.
import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer, transformWithEsbuild } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH || requireProject.resolve("playwright")).href)
const port = Number(process.env.ACCOUNT_DEGRADATION_TEST_PORT || 3059)
const entryID = "\0account-degradation-entry.tsx"
const html = '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module" src="/__account-degradation-entry.tsx"></script></body></html>'
const entry = `import { StrictMode } from 'react';import {createRoot} from 'react-dom/client';import RelayStationsPage from '/app/relay-stations-page.tsx';import '/app/globals.css';createRoot(document.getElementById('root')).render(<StrictMode><main style={{padding:16,minWidth:0}}><RelayStationsPage /></main></StrictMode>);`
const server = await createServer({ root, configFile: `${root}/vite.config.ts`, server: { host: "127.0.0.1", port, strictPort: true }, plugins: [{
  name: "account-degradation-acceptance", enforce: "pre",
  resolveId(id) { if (id === "/__account-degradation-entry.tsx") return entryID },
  async load(id) { if (id === entryID) return (await transformWithEsbuild(entry, "account-degradation-entry.tsx", { loader: "tsx", jsx: "automatic" })).code },
  configureServer(server) { server.middlewares.use(async (req, res, next) => { if (req.url === "/__account-degradation__") { res.setHeader("Content-Type", "text/html"); res.end(await server.transformIndexHtml(req.url, html)); return } next() }) },
}] })
const station = (id) => ({ id, name: id === 1 ? "验收甲站" : "验收乙站", base_url: "https://station.invalid", api_key_configured: true, auto_adjust_enabled:false, auto_adjust_no_profit_enabled:false, auto_priority_enabled:false, auto_priority_recall_enabled:false, auto_priority_recall_minutes:180, new_account_model_type_match_mode:"keyword",new_account_model_type_match_pattern:"",last_synced_at:"2026-01-01T12:00:00Z" })
const names = ["正常账号", "降智账号", "失败账号", "待确认账号", "未测试账号"]
const accounts = names.map((name,index) => ({ external_id:11+index,name,platform:"openai",type:"apikey",status:"active",schedulable:true,concurrency:10,current_concurrency:0,priority:index+2,pool_mode:false,pool_mode_retry_count:0,model_type:"gpt",user_charge_excluded:false,created_at:"2026-01-01T12:00:00Z",cost_multiplier:1,latency_samples:[],current_groups:[],unsafe_groups:[],no_profit_groups:[],downgrade_groups:[],suggested_group_ids:[],risk_state:"unassigned",can_apply:false,cache_hit_rate:0.5,success_rate:1 }))
const pelicanHTML = '<!doctype html><html><body style="margin:0;background:#fafafa"><svg width="800" height="448" viewBox="0 0 800 448"><rect width="800" height="448" fill="#fafafa"/><circle cx="400" cy="224" r="90" fill="#10b981"/><text x="320" y="400">鹈鹕预览验收</text></svg></body></html>'
const result = (id, method, status, extra = {}) => ({ id, method, model:"candidate",judge_model:"candidate",effort:"none",status,reason:"验收记录",created_at:"2026-01-01T12:00:00Z",...(method === "candy" ? { answer:"糖果最终结论：21 颗" } : { html:pelicanHTML }),...extra })
const histories = new Map([[11,[result(1,"candy","normal"),result(6,"pelican","normal")]], [12,[result(2,"pelican","degraded")]], [13,[result(3,"candy","error",{answer:""})]], [14,[result(4,"candy","inconclusive")]]])
const latest = () => Object.fromEntries([...histories].map(([id,records]) => [id,records[0]]))
const writes = []
const errors = []
const unexpected = []
const accountConfigs = new Map()
let failModels = false
let runDelay = 0
let nextID = 100
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless:true })
  const context = await browser.newContext({ viewport:{width:1840,height:1080} })
  await context.route("**/api/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const respond = (data) => route.fulfill({json:{data}})
    if (path === "/api/relay-stations") return respond([station(1),station(2)])
    if (path === "/api/sync-settings") return respond({relay_rate_enabled:false,relay_rate_interval_seconds:10,relay_snapshot_enabled:false,relay_snapshot_interval_seconds:3600})
    const overviewMatch = path.match(/^\/api\/relay-stations\/(\d+)$/)
    if (overviewMatch) return respond({station:station(Number(overviewMatch[1])),accounts,groups:[],summary:{account_count:5,risk_account_count:0,no_profit_account_count:0,no_safe_candidate_count:0,unknown_cost_count:0,auto_adjusted_count:0},adjustments:[],monitor_channels:[],sync_available:true,range:"today",usage_complete:true,usage_failed_accounts:0})
    if (/\/usage$/.test(path)) return respond({accounts:[],range:"today",complete:true,failed_accounts:0})
    if (path === "/api/relay-stations/1/account-degradation/latest") return respond(latest())
    if (path === "/api/relay-stations/2/account-degradation/latest") return respond({})
	const configMatch = path.match(/^\/api\/relay-stations\/(\d+)\/accounts\/(\d+)\/degradation\/config$/)
	if (configMatch) {
		const key = `${configMatch[1]}:${configMatch[2]}`
		if (request.method() === "PUT") { const body = request.postDataJSON(); accountConfigs.set(key, body); return respond(body) }
		return respond(accountConfigs.get(key) || {enabled:false,method:"pelican",model:"candidate",judge_model:"",effort:"none",pelican_interval_minutes:60,candy_interval_minutes:10,history_limit:100,disable_on_degraded:false,protocol:""})
	}
    const match = path.match(/^\/api\/relay-stations\/(\d+)\/accounts\/(\d+)\/(models|degradation)(?:\/(run|history)(?:\/(\d+))?)?$/)
    if (match) {
      const accountID = Number(match[2])
      if (match[3] === "models") {
        if (failModels) return route.fulfill({status:503,json:{error:"模拟模型列表不可用"}})
        return respond(accountID === 12 ? ["backup"] : ["candidate","backup"])
      }
      if (match[4] === "run") {
        const body = request.postDataJSON()
        writes.push({stationID:Number(match[1]),accountID,body})
        const actualModel = accountID === 12 && body.fallback_model ? body.fallback_model : body.model
        const saved = result(nextID++,body.method,"normal",{model:actualModel,judge_model:body.judge_model || actualModel})
        histories.set(accountID,[saved,...(histories.get(accountID) || [])])
        if (runDelay) await new Promise((done) => setTimeout(done, runDelay))
        return respond(saved)
      }
      if (match[4] === "history") {
        const remaining = match[5] ? (histories.get(accountID) || []).filter((item) => String(item.id) !== match[5]) : []
        histories.set(accountID, remaining)
        return respond({ results: remaining })
      }
      return respond({results:histories.get(accountID) || []})
    }
    unexpected.push(`${request.method()} ${path}`)
    return route.fulfill({status:500,json:{error:"unexpected mock endpoint"}})
  })
  const page = await context.newPage()
  page.on("pageerror",(error) => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${port}/__account-degradation__`)
  const rows = page.locator("div.relay-account-grid").filter({has:page.getByRole("checkbox",{name:/选择账号 /})})
  await page.getByRole("button",{name:"查看正常账号降智测试历史",exact:true}).waitFor()
  assert.equal(await rows.count(),5)
  const header = page.locator(".relay-account-grid.sticky")
  const headings = await header.locator(":scope > *").allTextContents()
  assert.equal(headings.indexOf("最近降智测试")+1,headings.indexOf("缓存命中"))
  const normalRow = rows.filter({has:page.getByRole("checkbox",{name:"选择账号 正常账号",exact:true})})
  assert.match(await normalRow.textContent(),/糖果最终结论：21 颗/)
  const degradedRow = rows.filter({has:page.getByRole("checkbox",{name:"选择账号 降智账号",exact:true})})
  assert.equal(await degradedRow.locator("iframe").count(),1)
  assert.equal(await degradedRow.locator("iframe").getAttribute("sandbox"),"allow-scripts")
  console.log("PASS: latest result before cache column, candy answer and sandboxed pelican preview")

  const filter = page.getByRole("combobox",{name:"按账号状态筛选",exact:true})
  async function selectFilter(name) { await filter.click(); await page.getByRole("option",{name,exact:true}).click() }
  await selectFilter("不降智")
  assert.equal(await rows.count(),1)
  assert.equal(await rows.getByRole("checkbox").getAttribute("aria-label"),"选择账号 正常账号")
  await selectFilter("降智")
  assert.equal(await rows.count(),1)
  assert.equal(await rows.getByRole("checkbox").getAttribute("aria-label"),"选择账号 降智账号")
  await selectFilter("全部账号")
  console.log("PASS: definitive normal/degraded filters exclude failed, inconclusive and untested accounts")

  // 关闭弹窗后「最近降智检测」列常驻显示正在测试，切去测别的账号也不丢
  runDelay = 1500
  const runningCell = page.getByRole("button",{name:"查看降智账号降智测试历史",exact:true}).locator("xpath=..")
  assert.match(await runningCell.innerText(),/鹈鹕 · candidate/)
  await page.getByRole("button",{name:"查看降智账号降智测试历史",exact:true}).click()
  let runningDialog = page.getByRole("dialog",{name:"账号降智测试",exact:true})
  await runningDialog.getByLabel("测试题目").selectOption("candy")
  await runningDialog.getByRole("button",{name:"立即测试",exact:true}).click()
  await runningDialog.getByRole("button",{name:"Close",exact:true}).click()
  await runningCell.getByText("正在测试",{exact:true}).waitFor({timeout:3000})
  const runningText = await runningCell.innerText()
  assert.match(runningText,/鹈鹕 · candidate/)
  assert.match(runningText,/疑似降智/)
  // 另一个账号开始测试时，前一个账号的运行状态必须仍在
  const otherCell = page.getByRole("button",{name:"查看失败账号降智测试历史",exact:true}).locator("xpath=..")
  await page.getByRole("button",{name:"查看失败账号降智测试历史",exact:true}).click()
  runningDialog = page.getByRole("dialog",{name:"账号降智测试",exact:true})
  await runningDialog.getByRole("button",{name:"立即测试",exact:true}).click()
  await runningDialog.getByRole("button",{name:"Close",exact:true}).click()
  await runningCell.getByText("正在测试",{exact:true}).waitFor({timeout:3000})
  await otherCell.getByText("正在测试",{exact:true}).waitFor({timeout:3000})
  await runningCell.getByText("糖果 · backup",{exact:true}).waitFor({timeout:8000})
  assert.equal(await runningCell.getByText("正在测试",{exact:true}).count(),0)
  assert.match(await runningCell.innerText(),/正常/)
  await otherCell.getByText("鹈鹕 · candidate",{exact:true}).waitFor({timeout:8000})
  runDelay = 0
  console.log("PASS: latest column keeps the running state across dialog close and another account's test")

	await page.getByRole("button",{name:"配置正常账号降智测试",exact:true}).click()
	let dialog = page.getByRole("dialog",{name:"账号降智测试配置",exact:true})
	const methodPelican = dialog.getByRole("checkbox",{name:"启用鹈鹕测试",exact:true})
	const methodCandy = dialog.getByRole("checkbox",{name:"启用糖果测试",exact:true})
	if (!(await methodPelican.isChecked())) await methodPelican.check()
	if (!(await methodCandy.isChecked())) await methodCandy.check()
	assert.equal(await methodPelican.isChecked(),true)
	assert.equal(await methodCandy.isChecked(),true)
	await dialog.getByRole("combobox",{name:"调用模型",exact:true}).first().click()
	await page.getByRole("option",{name:"candidate",exact:true}).click()
	await dialog.getByLabel("鹈鹕检测间隔（分钟）").fill("45")
	await dialog.getByLabel("糖果检测间隔（分钟）").fill("20")
	await dialog.getByLabel("历史最多保留（条）").fill("25")
	await dialog.getByRole("switch",{name:"发现降智后关闭账号",exact:true}).click()
	await dialog.getByRole("button",{name:"保存配置",exact:true}).click()
	await dialog.getByText("配置已保存",{exact:true}).waitFor()
	assert.equal(accountConfigs.get("1:11").method,"both")
	assert.equal(accountConfigs.get("1:11").history_limit,25)
	assert.equal(accountConfigs.get("1:11").disable_on_degraded,true)
	await dialog.getByRole("button",{name:"立即测试",exact:true}).click()
	await dialog.getByText("测试完成，记录已保存",{exact:true}).waitFor()
	await dialog.getByRole("region",{name:"账号降智测试历史"}).waitFor()
	assert.equal(writes.filter((write)=>write.accountID===11).length,1)
	// 账号历史行提供“加入分组历史”入口；分组选择与导入结果在分组设置验收用例中覆盖
	assert.ok(await dialog.getByRole("region",{name:"账号降智测试历史"}).getByRole("button",{name:/加入分组历史/}).count() >= 1)
	// 单条历史可删除，删除后列表同步
	const historyRegion = dialog.getByRole("region",{name:"账号降智测试历史"})
	const historyCount = histories.get(11).length
	assert.ok(historyCount >= 1)
	await historyRegion.getByRole("button",{name:/删除 .* 的测试记录/}).first().click()
	const singleDelete = page.getByRole("alertdialog")
	await singleDelete.getByRole("button",{name:"确认删除",exact:true}).click()
	await dialog.getByText("测试记录已删除",{exact:true}).waitFor()
	assert.equal(histories.get(11).length, historyCount - 1)
	assert.equal(await historyRegion.getByRole("button",{name:/删除 .* 的测试记录/}).count(), historyCount - 1)
	console.log("PASS: single account history record can be deleted")
	await page.setViewportSize({width:390,height:844})
	await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');if(!d)return false;const b=d.getBoundingClientRect();return b.x>=0&&b.width<=innerWidth&&b.height<=innerHeight})
	assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"mobile overflow")
	await dialog.getByRole("button",{name:"Close",exact:true}).click()
	await page.setViewportSize({width:1840,height:1080})
	await page.getByRole("button",{name:"验收乙站",exact:true}).click()
	await page.waitForFunction(()=>Array.from(document.querySelectorAll('button[aria-label*="降智测试历史"]')).filter(b=>b.textContent.includes('暂无测试')).length===5)
	assert.deepEqual(errors,[])
	assert.deepEqual(unexpected,[])
	console.log("PASS: account method selection, intervals, retention, auto-disable, manual run, mobile bounds and station isolation")
} catch (error) {
  console.error("Browser runtime errors:",errors,"Unexpected endpoints:",unexpected)
  throw error
} finally {
  if (browser) await browser.close()
  await server.close()
}
