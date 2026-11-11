// Channel activity cards: isolated page, mocked APIs, no real upstream requests or writes.
// Run with a separately installed Playwright module via PLAYWRIGHT_MODULE_PATH.
import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const requireProject = createRequire(`${root}/package.json`)
const { createServer, transformWithEsbuild } = await import(pathToFileURL(requireProject.resolve("vite")).href)
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE_PATH
  ? pathToFileURL(process.env.PLAYWRIGHT_MODULE_PATH).href
  : pathToFileURL(requireProject.resolve("playwright")).href)
const entryID = "\0channel-activity-test-entry.tsx"
const html = '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module" src="/__channel-activity-test-entry.tsx"></script></body></html>'
const entry = `
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import ChannelsPage from '/app/channels-page.tsx'
import { RefreshProvider, useTriggerRefresh } from '/lib/refresh-context.tsx'
import '/app/globals.css'
function Harness() {
  const refresh = useTriggerRefresh()
  return <main style={{padding:24}}><button onClick={refresh}>刷新测试数据</button><ChannelsPage /></main>
}
createRoot(document.getElementById('root')).render(<StrictMode><RefreshProvider><Harness /></RefreshProvider></StrictMode>)
`
const port = Number(process.env.CHANNEL_ACTIVITY_TEST_PORT || 3041)
const server = await createServer({
  root, configFile: `${root}/vite.config.ts`,
  server: { host: "127.0.0.1", port, strictPort: true },
  plugins: [{
    name: "isolated-channel-activity-test", enforce: "pre",
    resolveId(id) { if (id === "/__channel-activity-test-entry.tsx") return entryID },
    async load(id) {
      if (id === entryID) return (await transformWithEsbuild(entry, "channel-activity-test-entry.tsx", { loader: "tsx", jsx: "automatic" })).code
    },
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        if (req.url !== "/__channel-activity-test__") return next()
        res.setHeader("Content-Type", "text/html")
        res.end(await server.transformIndexHtml(req.url, html))
      })
    },
  }],
})
const now = Date.now()
const channels = [
  { id: 1, name: `长名称渠道${"测试".repeat(35)}`, type: "newapi", site_url: "https://newapi.channel.test/console?from=monitor#overview" },
  { id: 2, name: "Sub2API测试渠道", type: "sub2api", site_url: "http://sub2api.channel.test/account" },
  { id: 3, name: "未配置地址渠道", type: "newapi", site_url: "" },
].map((channel) => ({ ...channel, monitor_enabled: false, balance_mode: "manual", accounts: [], group_names: [] }))
const rateChanges = Array.from({ length: 50 }, (_, index) => ({
  id: 1000 + index, channel_id: index === 49 ? 999 : index === 48 ? 3 : index % 2 + 1, model_name: `倍率组 ${String(index + 1).padStart(2, "0")}`,
  old_ratio: 1, new_ratio: index % 2 ? 0.5 : 1.5, changed_at: new Date(now - index * 60000).toISOString(),
}))
const recentGroups = Array.from({ length: 50 }, (_, index) => ({
  id: 2000 + index, channel_id: index === 49 ? 999 : index === 48 ? 3 : index % 2 + 1,
  model_name: `新增分组 ${String(index + 1).padStart(2, "0")}${index === 0 ? `-${"超长分组名".repeat(20)}` : ""}`,
  description: index === 0 ? "首次发现的分组说明" : "", ratio: 1.234, completion_ratio: 1,
  first_seen_at: new Date(now - index * 60000).toISOString(), last_seen_at: new Date(now).toISOString(),
}))
const summary = {
  total_channels: channels.length, active_channels: 0, failed_channels: 0, total_balance: 0,
  cumulative_recharge_amount: 0, consumption_amount: 0, user_charge_amount: 0,
  excluded_user_charge_amount: 0, matched_account_count: 0, user_charge_complete: true,
  user_charge_breakdown: { channel_amount: 0, channel_complete: true, unlinked_amount: 0 },
  lowest_balance: null, channels: [], rate_change_count: 50,
  recent_rate_changes: rateChanges, recent_groups: recentGroups,
  recent_notification_logs: [], relay: [], recent_relay_adjustments: [],
}
const errors = []
const unexpectedRequests = []
const requests = []
const siteVisits = []
let generation = 1
let releaseSummary = null
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  await context.route(/^https?:\/\/(?:newapi|sub2api)\.channel\.test\//, async (route) => {
    siteVisits.push(route.request().url())
    await route.fulfill({ status: 200, contentType: "text/html", body: '<!doctype html><html lang="zh-CN"><head><title>模拟渠道页面</title></head><body>模拟渠道页面</body></html>' })
  })
  await context.route("**/api/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    requests.push(`${request.method()} ${url.pathname}${url.search}`)
    if (request.method() !== "GET") {
      unexpectedRequests.push(`${request.method()} ${url.pathname}`)
      return route.fulfill({ status: 501, json: { error: "No writes are allowed in this test" } })
    }
    if (url.pathname === "/api/dashboard/summary") {
      const range = url.searchParams.get("range")
      if (range === "30d") return route.fulfill({ status: 503, json: { error: "模拟统计读取失败" } })
      if (range === "7d") {
        await new Promise((resolve) => { releaseSummary = resolve })
        return route.fulfill({ json: { data: { ...summary, range, recent_groups: [{ ...recentGroups[1], model_name: "7天新增分组" }], recent_rate_changes: [{ ...rateChanges[1], model_name: "7天倍率组" }] } } })
      }
      if (range === "24h") return route.fulfill({ json: { data: { ...summary, range, recent_rate_changes: [], recent_groups: [] } } })
      const groups = generation === 1 ? recentGroups : [{ ...recentGroups[0], model_name: "刷新后的新增分组" }, ...recentGroups.slice(1)]
      const changes = generation === 1 ? rateChanges : [{ ...rateChanges[0], model_name: "刷新后的倍率组" }, ...rateChanges.slice(1)]
      return route.fulfill({ json: { data: { ...summary, range, recent_rate_changes: changes, recent_groups: groups } } })
    }
    if (url.pathname === "/api/channels") return route.fulfill({ json: { data: channels } })
    if (["/api/channels/metrics", "/api/channels/latency-trends", "/api/captcha-configs"].includes(url.pathname)) return route.fulfill({ json: { data: [] } })
    if (url.pathname === "/api/sync-settings") return route.fulfill({ json: { data: { channel_enabled: false, channel_interval_minutes: 30, channel_rate_enabled: false, channel_rate_interval_seconds: 60 } } })
    unexpectedRequests.push(`${request.method()} ${url.pathname}`)
    return route.fulfill({ status: 501, json: { error: `Unexpected mocked endpoint: ${url.pathname}` } })
  })
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${port}/__channel-activity-test__`)
  const changes = page.getByRole("region", { name: "最近倍率变动", exact: true })
  const groups = page.getByRole("region", { name: "最近新增分组", exact: true })
  await groups.getByText(recentGroups[0].model_name, { exact: true }).waitFor()
  assert.equal(await changes.locator("li").count(), 50)
  assert.equal(await groups.locator("li").count(), 50)
  assert.match(await changes.innerText(), /所选时间范围内最近 50 条/)
  assert.match(await groups.innerText(), /最近 50 条，按首次发现时间排序/)
  assert.equal(await changes.getByText("50 条", { exact: true }).count(), 1)
  assert.equal(await groups.getByText("50 条", { exact: true }).count(), 1)
  assert.deepEqual(await groups.locator("li").evaluateAll((items) => items.map((item) => item.querySelector("span.text-sm").textContent)), recentGroups.map((group) => group.model_name))
  assert.match(await groups.locator("li").first().innerText(), /首次发现的分组说明/)
  assert.match(await groups.locator("li").first().innerText(), /当前倍率 1\.234/)
  assert.equal(await groups.getByText("新增", { exact: true }).count(), 0, "new groups do not need a redundant new badge")
  assert.equal(await groups.getByText("NewAPI", { exact: true }).count(), 0, "badges show channel names only")
  assert.equal(await groups.getByText("Sub2API", { exact: true }).count(), 0, "badges show channel names only")
  assert.equal(await groups.getByText(channels[0].name, { exact: true }).count(), 24, "each NewAPI row shows its channel name once")
  assert.equal(await groups.getByText(channels[1].name, { exact: true }).count(), 24, "each Sub2API row shows its channel name once")
  assert.equal(await groups.getByText("#999", { exact: true }).count(), 1)
  const channelBadges = await groups.locator("li").evaluateAll((rows) => rows.map((row) => {
    const badge = row.querySelector(":scope > span[title]")
    const name = row.querySelector("span.text-sm")
    return badge ? {
      text: badge.textContent.trim(), title: badge.title, classes: [...badge.classList],
      onRight: badge.getBoundingClientRect().left >= name.getBoundingClientRect().right - 1,
    } : null
  }))
  for (let index = 0; index < recentGroups.length; index++) {
    const channel = channels.find((item) => item.id === recentGroups[index].channel_id)
    const badge = channelBadges[index]
    assert.ok(badge, "each group has a channel badge at the right of the row")
    assert.equal(badge.text, channel?.name ?? `#${recentGroups[index].channel_id}`)
    assert.equal(badge.title, badge.text, "hover exposes the full channel name")
    assert.ok(badge.onRight, "channel badge sits to the right of the group name")
    const color = channel?.type === "sub2api" ? "success" : channel?.type === "newapi" ? "brand" : "foreground"
    assert.ok(badge.classes.includes(`text-${color}`), "Sub2API badges are green, NewAPI badges are blue, missing metadata is neutral")
    if (channel) {
      assert.ok(badge.classes.includes(`bg-${color}/10`))
      assert.ok(badge.classes.includes(`ring-${color}/20`))
    }
  }
  const longChannelName = groups.locator("li > span[title]").first().locator(".truncate")
  assert.ok(await longChannelName.evaluate((element) => element.scrollWidth > element.clientWidth && getComputedStyle(element).textOverflow === "ellipsis"), "long channel names truncate instead of displacing group content")
  const changeChannelBadges = await changes.locator("li span.ring-inset").evaluateAll((badges) => badges.map((badge) => ({
    name: badge.querySelector(".truncate").textContent, classes: [...badge.classList],
  })))
  assert.equal(changeChannelBadges.length, 50, "each rate-change row retains its channel badge")
  for (let index = 0; index < rateChanges.length; index++) {
    const channel = channels.find((item) => item.id === rateChanges[index].channel_id)
    const badge = changeChannelBadges[index]
    const color = channel?.type === "sub2api" ? "success" : channel?.type === "newapi" ? "brand" : "foreground"
    assert.equal(badge.name, channel?.name ?? `#${rateChanges[index].channel_id}`)
    assert.ok(badge.classes.includes(`text-${color}`), "rate-change channel badges use the same green/blue palette as new-group badges")
    assert.ok(badge.classes.includes(channel ? `bg-${color}/10` : "bg-foreground/5"))
    assert.ok(badge.classes.includes(channel ? `ring-${color}/20` : "ring-border"))
  }
  for (const [card, rows] of [[changes, rateChanges], [groups, recentGroups]]) {
    assert.equal(await card.getByRole("link").count(), 48, "only channels with configured site URLs are links")
    for (let index = 0; index < rows.length; index++) {
      const channel = channels.find((item) => item.id === rows[index].channel_id)
      const row = card.locator("li").nth(index)
      if (!channel?.site_url) {
        assert.equal(await row.getByRole("link").count(), 0, "missing channels and empty URLs remain non-clickable text")
        assert.equal(await row.getByText(channel?.name ?? `#${rows[index].channel_id}`, { exact: true }).count(), 1)
        continue
      }
      const link = row.getByRole("link", { name: `在新标签页打开渠道 ${channel.name}`, exact: true })
      assert.equal(await link.getAttribute("href"), channel.site_url)
      assert.equal(await link.getAttribute("target"), "_blank")
      assert.equal(await link.getAttribute("rel"), "noopener noreferrer")
    }
  }
  console.log("PASS: both cards expose accessible channel links with exact configured URLs, new-tab targets and safe non-clickable fallbacks")
  assert.ok(await changes.locator("li").first().getByText("+50.0%", { exact: true }).evaluate((badge) => badge.classList.contains("text-danger")), "rate increases stay red")
  assert.ok(await changes.locator("li").nth(1).getByText("-50.0%", { exact: true }).evaluate((badge) => badge.classList.contains("text-success")), "rate decreases stay green")
  console.log("PASS: rate-change channel badges use Sub2API green/NewAPI blue with neutral fallback; increase/decrease colors are unchanged")
  assert.match(await changes.locator("li").first().innerText(), /1\.000\s*→\s*1\.500/)
  assert.equal(requests.filter((request) => request === "GET /api/dashboard/summary?range=today").length, 1, "both cards and KPIs share one summary request")
  assert.equal(requests.filter((request) => request === "GET /api/channels").length, 1, "channel metadata requests are deduplicated")
  const changesBox = await changes.boundingBox()
  const groupsBox = await groups.boundingBox()
  assert.equal(changesBox.y, groupsBox.y, "desktop cards share one row")
  assert.equal(changesBox.height, groupsBox.height, "desktop cards have equal heights")
  assert.ok(groupsBox.x >= changesBox.x + changesBox.width, "new groups are to the right of changes")
  assert.ok(Math.abs(changesBox.width - groupsBox.width) < 1)
  const scrollSelector = "[data-radix-scroll-area-viewport]"
  for (const card of [changes, groups]) {
    const metrics = await card.locator(scrollSelector).evaluate((viewport) => ({ height: viewport.clientHeight, scrollHeight: viewport.scrollHeight }))
    assert.ok(metrics.height > 0 && metrics.scrollHeight > metrics.height, "50 rows scroll inside each card")
  }
  await groups.locator(scrollSelector).evaluate((viewport) => { viewport.scrollTop = viewport.scrollHeight })
  assert.ok(await groups.locator(scrollSelector).evaluate((viewport) => viewport.scrollTop > 0))
  assert.equal(await changes.locator(scrollSelector).evaluate((viewport) => viewport.scrollTop), 0, "cards scroll independently")
  await groups.locator(scrollSelector).evaluate((viewport) => { viewport.scrollTop = 0 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "desktop has no horizontal overflow")
  const originalURL = page.url()
  for (const card of [changes, groups]) {
    for (const channel of channels.filter((item) => item.site_url)) {
      const link = card.getByRole("link", { name: `在新标签页打开渠道 ${channel.name}`, exact: true }).first()
      const useKeyboard = card === groups && channel.type === "sub2api"
      if (useKeyboard) await link.focus()
      const [popup] = await Promise.all([
        page.waitForEvent("popup"),
        useKeyboard ? link.press("Enter") : link.click(),
      ])
      await popup.waitForLoadState("domcontentloaded")
      assert.equal(popup.url(), channel.site_url, "clicking a name opens the channel site in a new page")
      assert.equal(await popup.evaluate(() => window.opener), null, "channel sites cannot access the monitoring window")
      assert.equal(await popup.evaluate(() => document.referrer), "", "channel navigation does not leak the monitoring URL")
      assert.equal(page.url(), originalURL, "the monitoring page stays in place")
      await popup.close()
    }
  }
  assert.equal(siteVisits.length, 4, "all popup navigation is mocked without visiting real channel sites")
  console.log("PASS: both cards open NewAPI/Sub2API sites in separate pages on click or keyboard activation without navigating the monitor or exposing its window")
  if (process.env.BROWSER_ARTIFACT_DIR) await page.screenshot({ path: `${process.env.BROWSER_ARTIFACT_DIR}/channel-activity-desktop.png`, fullPage: true })
  console.log("PASS: right-aligned channel name badges, Sub2API green/NewAPI blue, no duplicate/new labels, 50 rows, shared requests and independent scrolling")

  await page.setViewportSize({ width: 390, height: 844 })
  const mobileChanges = await changes.boundingBox()
  const mobileGroups = await groups.boundingBox()
  assert.ok(mobileGroups.y >= mobileChanges.y + mobileChanges.height, "narrow screens stack the cards")
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "long names do not overflow mobile")
  for (const card of [changes, groups]) {
    assert.ok(await card.evaluate((element) => element.scrollWidth <= element.clientWidth))
  }
  if (process.env.BROWSER_ARTIFACT_DIR) await page.screenshot({ path: `${process.env.BROWSER_ARTIFACT_DIR}/channel-activity-mobile.png`, fullPage: true })
  console.log("PASS: mobile card stacking and long-name overflow protection")

  generation = 2
  await page.getByRole("button", { name: "刷新测试数据", exact: true }).click()
  await groups.getByText("刷新后的新增分组", { exact: true }).waitFor()
  await changes.getByText("刷新后的倍率组", { exact: true }).waitFor()
  assert.equal(await groups.locator("li").count(), 50)
  console.log("PASS: global refresh updates both activity cards")

  await page.getByRole("group", { name: "渠道统计时间范围" }).getByRole("button", { name: "7 天", exact: true }).click()
  await groups.getByText("加载中…", { exact: true }).waitFor()
  assert.equal(await groups.getByText("刷新后的新增分组", { exact: true }).count(), 0, "range changes clear old groups while loading")
  await changes.getByText("加载中…", { exact: true }).waitFor()
  assert.equal(typeof releaseSummary, "function")
  releaseSummary()
  await groups.getByText("7天新增分组", { exact: true }).waitFor()
  await changes.getByText("7天倍率组", { exact: true }).waitFor()
  assert.equal(await groups.locator("li").count(), 1)
  assert.equal(await changes.locator("li").count(), 1)
  assert.equal(requests.filter((request) => request === "GET /api/dashboard/summary?range=7d").length, 1)
  await page.getByRole("group", { name: "渠道统计时间范围" }).getByRole("button", { name: "24 小时", exact: true }).click()
  await groups.getByText("暂无新增分组记录", { exact: true }).waitFor()
  await changes.getByText("暂无倍率变动记录", { exact: true }).waitFor()
  await page.getByRole("group", { name: "渠道统计时间范围" }).getByRole("button", { name: "30 天", exact: true }).click()
  await groups.getByRole("alert").waitFor()
  assert.match(await groups.getByRole("alert").innerText(), /模拟统计读取失败/)
  assert.match(await changes.getByRole("alert").innerText(), /模拟统计读取失败/)
  assert.equal(await groups.getByText("暂无新增分组记录", { exact: true }).count(), 0, "errors are not reported as empty results")
  assert.deepEqual(unexpectedRequests, [])
  assert.deepEqual(errors, [])
  console.log("PASS: shared time filtering, loading without stale rows, empty/error states and no runtime errors or API writes")
} finally {
  releaseSummary?.()
  if (browser) await browser.close()
  await server.close()
}
