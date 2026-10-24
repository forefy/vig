// Captures every real screen the film uses from a freshly seeded demo
// (backend on :7112 against vig_demo, frontend on :7111). From the repository
// root, after `make demo-reseed` (or the drop/migrate/seed recipe in REVIEW.md):
//   NODE_PATH=frontend/node_modules node docs/video/scripts/capture.cjs
// Writes PNGs at 2x into docs/video/assets/shots and boxes.json with the CSS
// pixel boxes of the elements the camera targets. The re-rating it performs
// is real, so the demo ends one history row richer than the seed.
const { chromium } = require('@playwright/test')
const fs = require('fs')
const path = require('path')

const OUT = path.join(__dirname, '..', 'assets', 'shots')
const API = 'http://localhost:7112'
const APP = 'http://localhost:7111'
const REPORT_AUDIT = process.env.REPORT_AUDIT || '01a16cec-e709-772f-b8ca-cc5742215600'
const SURFACE_AUDIT = process.env.SURFACE_AUDIT || '01a16cec-ea2f-7680-a042-5809bf7fa1dc'
const REASON = 'token account is a program-owned PDA'
const WORD = 'anyone'
const boxes = {}

const box = (page, sel) =>
  page.evaluate((s) => {
    const el = document.querySelector(s)
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height }
  }, sel)
const pageSize = (page) => page.evaluate(() => ({ w: document.documentElement.scrollWidth, h: document.documentElement.scrollHeight, scrollY: window.scrollY }))
const labelBoxes = (page, root) =>
  page.evaluate((rootSel) => {
    const out = []
    const r0 = document.querySelector(rootSel)
    if (!r0) return out
    for (const el of r0.querySelectorAll('.label-caps')) {
      const r = el.getBoundingClientRect()
      out.push({ text: el.textContent.trim(), x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height })
    }
    return out
  }, root)
const options = (page) =>
  page.evaluate(() =>
    [...document.querySelectorAll('[role="option"]')].map((el) => {
      const r = el.getBoundingClientRect()
      return { text: el.textContent.trim(), x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height }
    }),
  )

async function login(ctx, user) {
  const page = await ctx.newPage()
  await page.goto(`${API}/v1/auth/dev/login?user=${user}&next=/app`, { waitUntil: 'networkidle' })
  return page
}

;(async () => {
  fs.rmSync(OUT, { recursive: true, force: true })
  fs.mkdirSync(OUT, { recursive: true })
  const browser = await chromium.launch()

  // Surface map of the token program, wide.
  {
    const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2, colorScheme: 'dark' })
    const page = await login(ctx, 'pro')
    await page.goto(`${APP}/app/audits/${SURFACE_AUDIT}/surface`, { waitUntil: 'networkidle' })
    await page.waitForTimeout(3000)
    await page.screenshot({ path: path.join(OUT, 'surface.png'), fullPage: true })
    boxes.surface = {
      page: await pageSize(page),
      nodes: await page.evaluate(() =>
        [...document.querySelectorAll('[id^="n-ix"]')].map((el) => {
          const r = el.getBoundingClientRect()
          return { id: el.id.slice(-24), text: el.textContent.trim().slice(0, 40), x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height }
        }),
      ),
    }
    await ctx.close()
  }

  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2, colorScheme: 'dark' })
  const page = await login(ctx, 'pro')
  const reportUrl = `${APP}/app/audits/${REPORT_AUDIT}/report`

  // State A: the report as seeded.
  await page.goto(reportUrl, { waitUntil: 'networkidle' })
  await page.waitForSelector('#f-1')
  await page.waitForTimeout(800)
  await page.screenshot({ path: path.join(OUT, 'a-full.png'), fullPage: true })
  boxes.a = {
    page: await pageSize(page),
    main: await box(page, 'main'),
    details: await box(page, 'details'),
    query: await box(page, 'input[aria-label="Filter findings"]'),
    nav: await box(page, 'nav[aria-label="Findings"]'),
    findings: {},
    labels: await labelBoxes(page, 'main'),
    f2severity: await box(page, '#f-2 [aria-label="Finding severity"]'),
  }
  for (const n of [1, 2, 3, 4, 5]) boxes.a.findings[n] = await box(page, `#f-${n}`)

  // Finding 1 read as a chain: each step of the strip clicked, the card captured.
  boxes.chain = { tabs: await page.evaluate(() => [...document.querySelectorAll('#f-1 [role="tab"]')].map((el) => { const r = el.getBoundingClientRect(); return { text: el.textContent.trim(), x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height } })), cards: [] }
  const tabs = page.locator('#f-1 [role="tab"]')
  const ntabs = await tabs.count()
  for (let i = 0; i < ntabs; i++) {
    if (i > 0) { await tabs.nth(i).click(); await page.waitForTimeout(350) }
    await page.locator('#f-1').screenshot({ path: path.join(OUT, `chain-${i}.png`) })
    boxes.chain.cards.push(await box(page, '#f-1'))
  }
  await page.goto(reportUrl, { waitUntil: 'networkidle' })
  await page.waitForSelector('#f-2')
  await page.waitForTimeout(600)

  // The re-rating of finding 2, for real, with every keystroke of the reason.
  const trigger = page.locator('#f-2 [aria-label="Finding severity"]')
  await trigger.scrollIntoViewIfNeeded()
  await page.evaluate(() => window.scrollBy(0, -200))
  await page.waitForTimeout(200)
  boxes.rr = { scrollY: await page.evaluate(() => window.scrollY) }
  await trigger.click()
  await page.waitForTimeout(400)
  boxes.rr.options = await options(page)
  await page.screenshot({ path: path.join(OUT, 'rr-options.png') })
  await page.getByRole('option', { name: 'medium' }).click()
  await page.waitForTimeout(500)
  boxes.rr.dialog = await box(page, '[role="dialog"]')
  boxes.rr.reasonInput = await box(page, 'input[aria-label="Reason"]')
  await page.screenshot({ path: path.join(OUT, 'rr-dialog.png') })
  const d = boxes.rr.dialog
  const dclip = { x: d.x - 8, y: d.y - boxes.rr.scrollY - 8, width: d.w + 16, height: d.h + 16 }
  await page.screenshot({ path: path.join(OUT, 'rd-00.png'), clip: dclip })
  for (let i = 0; i < REASON.length; i++) {
    await page.keyboard.type(REASON[i])
    await page.waitForTimeout(40)
    await page.screenshot({ path: path.join(OUT, `rd-${String(i + 1).padStart(2, '0')}.png`), clip: dclip })
  }
  boxes.rr.button = await page.evaluate(() => {
    const b = [...document.querySelectorAll('[role="dialog"] button')].find((x) => x.textContent.trim() === 'Downgrade')
    const r = b.getBoundingClientRect()
    return { x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height }
  })
  await page.getByRole('button', { name: 'Downgrade' }).click()
  await page.waitForTimeout(1500)

  // State B: the report after the re-rating.
  await page.goto(reportUrl, { waitUntil: 'networkidle' })
  await page.waitForSelector('#f-1')
  await page.waitForTimeout(800)
  await page.screenshot({ path: path.join(OUT, 'b-full.png'), fullPage: true })
  boxes.b = { page: await pageSize(page), details: await box(page, 'details'), query: await box(page, 'input[aria-label="Filter findings"]'), nav: await box(page, 'nav[aria-label="Findings"]'), findings: {}, f2labels: await labelBoxes(page, '#f-2'), labels: await labelBoxes(page, 'main') }
  for (const n of [1, 2, 3, 4, 5]) boxes.b.findings[n] = await box(page, `#f-${n}`)
  boxes.b.history = await page.evaluate(() => {
    const el = document.querySelector('#f-2 ol li')
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height, text: el.textContent }
  })

  // The query line on state B: click, type the status word, pick it, watch the list fold.
  const q = page.locator('input[aria-label="Filter findings"]')
  const main = boxes.a.main
  const clip = { x: main.x, y: boxes.b.query.y - 48, width: main.w, height: 1000 }
  boxes.q = { clip }
  await q.click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: path.join(OUT, 'q-00.png'), fullPage: true, clip })
  boxes.q.listbox = await box(page, '[role="listbox"]')
  for (let i = 0; i < WORD.length; i++) {
    await page.keyboard.type(WORD[i])
    await page.waitForTimeout(150)
    await page.screenshot({ path: path.join(OUT, `q-${String(i + 1).padStart(2, '0')}.png`), fullPage: true, clip })
  }
  boxes.q.suggestions = await options(page)
  await page.keyboard.press('Enter')
  await page.waitForTimeout(600)
  await page.screenshot({ path: path.join(OUT, 'b-filtered.png'), fullPage: true })
  boxes.q.filtered = { page: await pageSize(page), nav: await box(page, 'nav[aria-label="Findings"]'), f1: await box(page, '#f-1'), chip: await box(page, 'input[aria-label="Filter findings"]') }

  // The changes log, unfolded, on state B.
  await page.goto(reportUrl, { waitUntil: 'networkidle' })
  await page.waitForSelector('#f-1')
  await page.waitForTimeout(500)
  boxes.changes = { summary: await box(page, 'details summary') }
  await page.locator('details summary').first().click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: path.join(OUT, 'b-changes.png'), fullPage: true })
  boxes.changes.page = await pageSize(page)
  boxes.changes.details = await box(page, 'details')
  boxes.changes.archived = await page.evaluate(() => {
    const el = document.querySelector('#f-5')
    const r = el.closest('section').getBoundingClientRect()
    return { x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height }
  })
  await ctx.close()

  // Lessons, as admin.
  {
    const actx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2, colorScheme: 'dark' })
    const apage = await login(actx, 'admin')
    await apage.goto(`${APP}/admin/lessons`, { waitUntil: 'networkidle' })
    await apage.waitForTimeout(1000)
    await apage.screenshot({ path: path.join(OUT, 'lessons.png'), fullPage: true })
    boxes.lessons = { page: await pageSize(apage), list: await box(apage, 'main ul, main ol') }
    await actx.close()
  }

  await browser.close()
  fs.writeFileSync(path.join(OUT, 'boxes.json'), JSON.stringify(boxes, null, 1))
  console.log('captured', fs.readdirSync(OUT).length, 'files')
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
