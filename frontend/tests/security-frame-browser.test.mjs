import assert from 'node:assert/strict'

// Read-only production header/browser verification. Only synthetic parent
// documents and API fixtures are intercepted; child HTML/assets are real.
const pw = await import(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright')
const chromium = pw.chromium || pw.default.chromium
const target = process.env.SECURITY_TEST_TARGET || 'https://gatewayops.qidian7.de5.net'
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] })
const context = await browser.newContext()
const fixturePath = '/__gatewayops_security_frame_test'
const allowed = 'https://qidian7.top'
const denied = 'https://frame-security-denied.invalid'
let policy = ['https://qidian7.top']
let saves = 0
let authRequests = 0
await context.route('**/api/**', async (route) => {
  const path = new URL(route.request().url()).pathname
  let data = []
  if (path === '/api/auth/me') { authRequests++; data = { username: 'synthetic-admin' } }
  if (path === '/api/security-settings') {
    if (route.request().method() === 'PUT') { policy = route.request().postDataJSON().public_frame_origins; saves++ }
    data = { public_frame_origins: policy }
  }
  if (path === '/api/public/service-status') data = { services: [], updated_at: new Date().toISOString() }
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data }) })
})
const document = (src) => `<!doctype html><title>Synthetic frame policy test</title><iframe title="policy-test" src="${src}" width="1100" height="800"></iframe>`
for (const origin of [allowed, denied]) {
  await context.route(`${origin}${fixturePath}**`, async (route) => {
    const source = new URL(route.request().url()).searchParams.get('src')
    await route.fulfill({ contentType: 'text/html', body: document(source) })
  })
}
async function framed(origin, path, shouldLoad) {
  const page = await context.newPage()
  const violations = []
  page.on('console', (message) => { if (message.text().includes('frame-ancestors')) violations.push(message.text()) })
  await page.goto(`${origin}${fixturePath}?src=${encodeURIComponent(target + path)}`, { waitUntil: 'networkidle' })
  const root = page.frameLocator('iframe').locator('#root')
  if (shouldLoad) await root.waitFor({ state: 'attached', timeout: 15000 })
  else {
    assert.equal(await root.count(), 0, `unexpected document loaded: ${origin} -> ${path}`)
    assert.ok(violations.length, `missing CSP rejection evidence: ${origin} -> ${path}`)
  }
  console.log(`PASS: ${origin} -> ${path}: ${shouldLoad ? 'allowed' : 'blocked by CSP'}`)
  return page
}
try {
  const publicPage = await framed(allowed, '/public/service-status', true)
  const frame = publicPage.frames().find((frame) => frame.url().startsWith(target))
  assert.ok(frame)
  const before = authRequests
  await frame.evaluate(() => { history.pushState({}, '', '/settings'); window.dispatchEvent(new PopStateEvent('popstate')) })
  await frame.getByRole('alert').filter({ hasText: '管理后台不能在 iframe 内打开' }).waitFor()
  assert.equal(authRequests, before, 'SPA navigation mounted authentication in a public iframe')
  assert.equal(await frame.locator('input[type=password]').count(), 0)
  console.log('PASS: public iframe SPA navigation cannot mount login or management UI')
  await publicPage.close()
  await (await framed(denied, '/public/service-status', false)).close()
  await (await framed(allowed, '/', false)).close()
  await (await framed(allowed, '/settings', false)).close()
  // All ancestors must match: a trusted immediate parent cannot launder an
  // untrusted outer frame into an allowed embed.
  const nested = await context.newPage()
  const inner = `${allowed}${fixturePath}?src=${encodeURIComponent(target + '/public/service-status')}`
  const nestedViolations = []
  nested.on('console', (message) => { if (message.text().includes('frame-ancestors')) nestedViolations.push(message.text()) })
  await nested.goto(`${denied}${fixturePath}?src=${encodeURIComponent(inner)}`, { waitUntil: 'networkidle' })
  assert.ok(nestedViolations.length, 'untrusted grandparent was not blocked')
  assert.equal(nested.frames().filter((frame) => frame.url().startsWith(target)).length, 0)
  console.log('PASS: nested untrusted ancestor blocked')
  await nested.close()

  // This UI smoke test is API-mocked. It never logs in or saves a real policy.
  const settings = await context.newPage()
  await settings.goto(target + '/settings', { waitUntil: 'networkidle' })
  const input = settings.getByLabel('允许嵌入的来源', { exact: false })
  await input.waitFor()
  assert.equal(await input.inputValue(), allowed)
  await input.fill(allowed + '\nhttps://synthetic-frame.test')
  await settings.getByRole('button', { name: '保存放行策略' }).click()
  await settings.getByText('公开页面嵌入策略已保存，新加载的页面立即生效', { exact: true }).waitFor()
  assert.equal(saves, 1)
  assert.deepEqual(policy, [allowed, 'https://synthetic-frame.test'])
  await settings.reload({ waitUntil: 'networkidle' })
  assert.equal(await settings.getByLabel('允许嵌入的来源', { exact: false }).inputValue(), policy.join('\n'))
  console.log('PASS: security settings display, save and reload (mock API only)')
  await settings.close()
} finally { await browser.close() }
