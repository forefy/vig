import { test, expect } from '@playwright/test'
import type { Page } from '@playwright/test'
import { STARTERS } from '../src/features/atlas/starters'

// Atlas against the real stack: the seeded builds, DuckDB in the browser
// over their parts, a starter run into a graph, a node pressed into its
// place beside the picture, the engine's message on a bad statement, and
// the page at phone width.

const rows = /\d+ rows · [\d.]+ s/

async function open(page: Page) {
  await page.goto('/v1/auth/dev/login?user=pro')
  await page.goto('/app/atlas')
  await expect(page.getByRole('heading', { name: 'Indexed sources' })).toBeVisible()
  await expect(page.getByText(rows)).toBeVisible({ timeout: 60_000 })
}

// A starter pressed and its picture drawn. Results that name nodes draw
// too, so the graph tab may already show the statement before; the legend
// naming this result's own relation says the new picture is in.
async function drawn(page: Page, label: string, rel: string) {
  await page.getByRole('button', { name: label, exact: true }).click()
  await expect(page.getByRole('tab', { name: 'graph' })).toBeVisible({ timeout: 60_000 })
  await page.getByRole('tab', { name: 'graph' }).click()
  await expect(page.getByText(new RegExp(`^${rel} \\d+$`)).first()).toBeVisible({ timeout: 60_000 })
}

// A node of the graph by the start of its id; pressed through the event
// the view listens for, since the picture may hold it outside the frame.
function node(page: Page, prefix: string) {
  return page.locator('svg[aria-label="The result as a graph"] g:has(> circle)', { has: page.locator('title', { hasText: new RegExp(`^${prefix}`) }) }).first()
}

test('opens on every build and runs a starter into a graph with a place', async ({ page }) => {
  await open(page)
  await expect(page.getByRole('button', { name: /^all builds/ })).toHaveAttribute('aria-pressed', 'true')
  // The opening statement lists functions: nodes, drawn with their relations around them.
  await expect(page.getByRole('tab', { name: 'graph' })).toBeVisible()
  await page.getByRole('tab', { name: 'graph' }).click()
  await expect(page.getByText(/\d+ nodes/)).toBeVisible()
  await expect(page.getByText(/around/)).toBeVisible({ timeout: 30_000 })
  await page.getByRole('tab', { name: 'table' }).click()
  await page.getByTitle('mattermost/mattermost-plugin-github').click()
  await expect(page.getByText('mattermost/mattermost-plugin-github', { exact: true })).toBeVisible()
  await drawn(page, 'a handler and what it reaches', 'calls')
  const svg = page.getByRole('group', { name: 'The result as a graph' })
  await expect(svg).toBeVisible()
  await expect(page.getByText(/\d+ nodes/)).toBeVisible()
  await node(page, 'fn:').dispatchEvent('click')
  await expect(page.getByRole('button', { name: 'expand' })).toBeVisible()
  await expect(page.locator('pre code span.flex').first()).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: 'expand' }).click()
  await expect(page.getByText(/\d+ nodes/)).toBeVisible()
})

test('a capture entry opens its request beside its response', async ({ page }) => {
  await open(page)
  await page.getByTitle('https://openrouter.ai').click()
  await drawn(page, 'a page and what it triggers', 'triggers')
  await node(page, 'entry:').dispatchEvent('click')
  await expect(page.getByText('host: openrouter.ai').first()).toBeVisible({ timeout: 30_000 })
})

test('a value that travels shows the lines that carry it', async ({ page }) => {
  await open(page)
  await page.getByTitle('https://openrouter.ai').click()
  await drawn(page, 'a value that travels', 'yields')
  await node(page, 'value:').dispatchEvent('click')
  await expect(page.getByText(/\d+ yield · \d+ carry/)).toBeVisible({ timeout: 30_000 })
  await expect(page.locator('pre code span.flex.bg-accent\\/70').first()).toBeVisible()
  // A fold opens on its own, and folds again. The block is pinned by its
  // index, since the button that names it changes on the press.
  const fold = page.getByRole('button', { name: /^Show \d+ folded lines/ }).first()
  const at = await fold.evaluate((el) => [...document.querySelectorAll('pre')].indexOf(el.closest('pre')!))
  const block = page.locator('pre').nth(at)
  const folded = await block.locator('span.flex').count()
  const n = Number((await fold.getAttribute('aria-label'))!.match(/\d+/)![0])
  await fold.click()
  await expect(block.locator('span.flex')).toHaveCount(folded + n)
  await block.getByRole('button', { name: /^Fold \d+ lines/ }).first().click()
  await expect(block.locator('span.flex')).toHaveCount(folded)
})

test('a bad statement shows the engine message once, and two statements show the last', async ({ page }) => {
  await open(page)
  await page.getByRole('textbox', { name: 'SQL' }).fill('SELECT repo, nope FROM functions LIMIT 3;')
  await page.getByRole('button', { name: 'run' }).click()
  await expect(page.getByText(/Referenced column "nope" not found/)).toBeVisible({ timeout: 30_000 })
  await page.getByRole('textbox', { name: 'SQL' }).fill('SELEC repo FROM functions LIMIT 1;')
  await page.getByRole('button', { name: 'run' }).click()
  await expect(page.getByText(/syntax error at or near "SELEC"/)).toBeVisible({ timeout: 30_000 })
  const block = await page.locator('main pre').filter({ hasText: 'LINE 1:' }).first().innerText()
  expect(block.match(/LINE 1:/g)?.length ?? 0).toBe(1)
  await page.getByRole('textbox', { name: 'SQL' }).fill('SELECT 1 AS a; SELECT 2 AS b;')
  await page.getByRole('button', { name: 'run' }).click()
  await expect(page.getByRole('columnheader', { name: 'b' })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByRole('columnheader', { name: 'a', exact: true })).toHaveCount(0)
})

test.describe('phone', () => {
  test.use({ viewport: { width: 375, height: 812 } })
  test('fits the width with chip rails', async ({ page }) => {
    await open(page)
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
    expect(wide).toBe(false)
    await expect(page.getByRole('button', { name: /^all builds/ })).toBeVisible()
    await expect(page.getByRole('button', { name: /^tables · \d+/ })).toBeVisible()
  })
})

// The production shell: the prerendered landing hydrates and the app shell
// renders Atlas with nothing in the console, no inline script the CSP
// forbids and no hydration of one page's markup against another.
test('the landing and atlas load with a clean console', async ({ page }) => {
  const errors: string[] = []
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto('/v1/auth/dev/login?user=pro')
  await page.goto('/')
  await expect(page.getByRole('heading').first()).toBeVisible()
  await page.waitForTimeout(1500)
  await open(page)
  await page.waitForTimeout(1500)
  expect(errors).toEqual([])
})

// What the independent review found and the fixes have to hold: parts are
// read by Range, not whole; a result keeps two columns of one name and
// reads a decimal as its value; the page scrolls over a graph until the
// frame is pressed; a picked sink's expansion joins the sink.
test('parts are fetched once per browser and kept across visits', async ({ browser }, info) => {
  // A persistent profile has a disk cache, which a fresh context lacks.
  const context = await (await import('@playwright/test')).chromium.launchPersistentContext(info.outputPath('profile'), { headless: true })
  const page = await context.newPage()
  const base = info.project.use.baseURL ?? 'http://localhost:7111'
  const dates = new Map<string, string>()
  let bytes = 0
  let repeats = 0
  page.on('response', (r) => {
    if (!/\.parquet(\?|$)/.test(r.url())) return
    const seen = dates.get(r.url())
    if (seen === undefined) {
      dates.set(r.url(), r.headers()['date'] ?? '')
      bytes += Number(r.headers()['content-length'] ?? 0)
    } else if (seen !== (r.headers()['date'] ?? '')) repeats++
  })
  await page.goto(`${base}/v1/auth/dev/login?user=pro`)
  const catalogue = await (await page.request.get(`${base}/v1/atlas/index`)).json()
  const total = (catalogue.audits as { bytes: number }[]).reduce((s, a) => s + a.bytes, 0)
  await page.goto(`${base}/app/atlas`)
  await expect(page.getByText(rows)).toBeVisible({ timeout: 90_000 })
  await page.waitForTimeout(1500)
  expect(bytes).toBeGreaterThan(0)
  expect(bytes).toBeLessThanOrEqual(total * 1.01)
  await page.goto(`${base}/app`)
  await page.goto(`${base}/app/atlas`)
  await expect(page.getByText(rows)).toBeVisible({ timeout: 90_000 })
  await page.waitForTimeout(1500)
  // Every part came back with the date it was first served: the cache
  // answered, the server did not.
  expect(repeats).toBe(0)
  await context.close()
  void browser
})

test('a result keeps repeated columns and reads decimals as values', async ({ page }) => {
  await open(page)
  await page.getByRole('textbox', { name: 'SQL' }).fill("SELECT 'x' AS a, 'y' AS a, 1.5 AS lit, 3.75::DECIMAL(10,2) AS dec2;")
  await page.getByRole('button', { name: 'run' }).click()
  await expect(page.getByRole('columnheader', { name: 'a_1' })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByRole('cell', { name: 'y', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: '1.5', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: '3.75', exact: true })).toBeVisible()
  await page.getByRole('textbox', { name: 'SQL' }).fill('SELECT 1 AS one;')
  await page.getByRole('button', { name: 'run' }).click()
  await expect(page.getByRole('columnheader', { name: 'one' })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByRole('columnheader', { name: 'a_1' })).toHaveCount(0)
})

test('the page scrolls over a graph until the frame is pressed, and a node opens readable', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await open(page)
  await page.getByTitle('mattermost/mattermost-plugin-github').click()
  await drawn(page, 'an input reaching a sink', 'flows')
  const svg = page.getByRole('group', { name: 'The result as a graph' })
  await expect(svg).toBeVisible()
  // The frame is measured once the relations around the result are in,
  // and brought whole into view, so the wheel lands on it.
  await expect(page.getByText(/^around \d+$/).first()).toBeVisible({ timeout: 30_000 })
  // The page may then sit at its foot, so the wheel goes up, where the
  // page always has room to scroll.
  await svg.evaluate((el) => el.scrollIntoView({ block: 'center' }))
  const at = await page.evaluate(() => window.scrollY)
  expect(at).toBeGreaterThan(0)
  const scale = async () => Number((await svg.locator('> g').getAttribute('transform'))!.match(/scale\(([\d.]+)\)/)![1])
  // The picture opens framing the result; its labels read at any scale.
  expect(await svg.locator('text').first().evaluate((el) => el.getBoundingClientRect().height)).toBeGreaterThanOrEqual(9)
  const box = (await svg.boundingBox())!
  expect(box.y + box.height).toBeLessThanOrEqual(800)
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.wheel(0, -120)
  await page.waitForTimeout(300)
  expect(await page.evaluate(() => window.scrollY)).toBeLessThan(at)
  await page.evaluate((y) => window.scrollTo(0, y), at)
  await svg.click({ position: { x: 8, y: 8 } })
  const before = await scale()
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.wheel(0, -120)
  await page.waitForTimeout(300)
  expect(await page.evaluate(() => window.scrollY)).toBe(at)
  expect(await scale()).toBe(before)
  await node(page, 'sink:').dispatchEvent('click')
  await expect(page.getByRole('button', { name: 'expand' })).toBeVisible()
  expect(await scale()).toBeGreaterThanOrEqual(0.8)
  // A pan sticks: it survives the frame's own re-renders and a keystroke
  // in the SQL box, which re-renders the whole console.
  const transform = async () => (await svg.locator('> g').getAttribute('transform'))!
  await svg.click({ position: { x: 8, y: 8 } })
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.wheel(0, 120)
  await page.waitForTimeout(400)
  const panned = await transform()
  expect(panned).not.toBe(before)
  await page.getByRole('textbox', { name: 'SQL' }).press('End')
  await page.getByRole('textbox', { name: 'SQL' }).type(' ')
  await page.waitForTimeout(400)
  expect(await transform()).toBe(panned)
})

// A pan taken right after a build is picked, while the scope change may
// still be settling, survives whatever that change does.
test('a pan taken right after picking a build survives the scope settling', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await open(page)
  await page.getByTitle('mattermost/mattermost-plugin-github').click()
  await drawn(page, 'an input reaching a sink', 'flows')
  const svg = page.getByRole('group', { name: 'The result as a graph' })
  await expect(svg).toBeVisible()
  const transform = async () => (await svg.locator('> g').getAttribute('transform'))!
  const fitted = await transform()
  await svg.evaluate((el) => el.scrollIntoView({ block: 'center' }))
  const box = (await svg.boundingBox())!
  await svg.click({ position: { x: 8, y: 8 } })
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.wheel(0, 120)
  await page.waitForTimeout(400)
  const panned = await transform()
  expect(panned).not.toBe(fitted)
  await page.waitForTimeout(3000)
  // The relations around the result have come meanwhile, and the pan held.
  await expect(page.getByText(/^around \d+$/).first()).toBeVisible()
  expect(await transform()).toBe(panned)
})

// The dark scheme, which the product is designed for first: the page takes
// it, the marks and labels stay legible on it, nothing logs.
test.describe('dark scheme', () => {
  test.use({ colorScheme: 'dark' })
  test('renders dark with legible marks and a clean console', async ({ page }) => {
    const errors: string[] = []
    page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
    page.on('pageerror', (e) => errors.push(e.message))
    await open(page)
    expect(await page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(true)
    const lum = (c: string) => {
      const [r, g, b] = c.match(/\d+/g)!.map(Number)
      return (0.2126 * r! + 0.7152 * g! + 0.0722 * b!) / 255
    }
    const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
    const fg = await page.evaluate(() => getComputedStyle(document.body).color)
    expect(lum(bg)).toBeLessThan(0.2)
    expect(lum(fg)).toBeGreaterThan(0.7)
    await page.getByTitle('mattermost/mattermost-plugin-github').click()
    await drawn(page, 'an input reaching a sink', 'flows')
    await node(page, 'sink:').dispatchEvent('click')
    const marked = page.locator('pre code span.flex.bg-bad-soft\\/40').first()
    await expect(marked).toBeVisible({ timeout: 30_000 })
    const markBg = await marked.evaluate((el) => getComputedStyle(el).backgroundColor)
    const rowBg = await page.locator('pre code span.flex').nth(0).evaluate((el) => getComputedStyle(el).backgroundColor)
    expect(markBg).not.toBe(rowBg)
    const label = await page.locator('svg[aria-label="The result as a graph"] text').first().evaluate((el) => getComputedStyle(el).fill)
    expect(lum(label)).toBeGreaterThan(0.5)
    expect(errors).toEqual([])
  })
})

// Touch: one finger pans, two fingers pinch, the frame refuses the page's
// scroll only once pressed. Trackpad: small wheel deltas pan and zoom by
// their size, not by a fixed step per event.
test.describe('touch and trackpad', () => {
  test.use({ hasTouch: true })
  test('a tap picks the node under the finger, whatever node the click after it lands on', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await open(page)
    await page.getByTitle('mattermost/mattermost-plugin-github').click()
    await drawn(page, 'a handler and what it reaches', 'calls')
    const svg = page.getByRole('group', { name: 'The result as a graph' })
    const nodes = svg.locator('g[role=button]')
    const a = nodes.nth(0)
    const b = nodes.nth(1)
    const at = async (n: typeof a) => {
      const r = (await n.locator('circle').last().boundingBox())!
      return { x: r.x + r.width / 2, y: r.y + r.height / 2 }
    }
    const touch = async (x: number, y: number) =>
      svg.evaluate(
        (el, { x, y }) => {
          for (const type of ['pointerdown', 'pointerup']) el.dispatchEvent(new PointerEvent(type, { pointerId: 9, pointerType: 'touch', isPrimary: true, clientX: x, clientY: y, bubbles: true }))
        },
        { x, y },
      )
    // A still first touch takes the frame and picks the node under it; the
    // browser's own click may land elsewhere, and the tap holds.
    const pa = await at(a)
    await touch(pa.x, pa.y)
    await b.dispatchEvent('click')
    await expect(a).toHaveAttribute('aria-pressed', 'true')
    await expect(b).toHaveAttribute('aria-pressed', 'false')
    expect(await svg.evaluate((el) => el.classList.contains('touch-none'))).toBe(true)
    // The next tap, on the frame now taken, picks the other node.
    await page.waitForTimeout(500)
    const pb = await at(b)
    await touch(pb.x, pb.y)
    await expect(b).toHaveAttribute('aria-pressed', 'true')
  })
  test('a finger pans, two pinch, small wheel deltas move gently', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await open(page)
    await page.getByTitle('mattermost/mattermost-plugin-github').click()
    await drawn(page, 'an input reaching a sink', 'flows')
    const svg = page.getByRole('group', { name: 'The result as a graph' })
    await expect(svg).toBeVisible()
    await page.waitForTimeout(2500)
    const read = async () => {
      const t = (await svg.locator('> g').getAttribute('transform'))!
      const m = t.match(/translate\(([-\d.]+) ([-\d.]+)\) scale\(([\d.]+)\)/)!
      return { x: Number(m[1]), y: Number(m[2]), k: Number(m[3]) }
    }
    expect(await svg.evaluate((el) => el.classList.contains('touch-pan-y'))).toBe(true)
    const before = await read()
    const touch = async (steps: { type: string; id: number; x: number; y: number }[]) =>
      svg.evaluate((el, steps) => {
        const r = el.getBoundingClientRect()
        for (const s of steps) el.dispatchEvent(new PointerEvent(s.type, { pointerId: s.id, pointerType: 'touch', isPrimary: s.id === 1, clientX: r.left + s.x, clientY: r.top + s.y, bubbles: true }))
      }, steps)
    // A first touch only takes the frame, so a swipe over it scrolls the
    // page; the next one pans.
    await touch([{ type: 'pointerdown', id: 1, x: 100, y: 100 }, { type: 'pointermove', id: 1, x: 160, y: 130 }, { type: 'pointerup', id: 1, x: 160, y: 130 }])
    await page.waitForTimeout(200)
    expect(await read()).toEqual(before)
    expect(await svg.evaluate((el) => el.classList.contains('touch-none'))).toBe(true)
    await touch([{ type: 'pointerdown', id: 1, x: 100, y: 100 }, { type: 'pointermove', id: 1, x: 160, y: 130 }, { type: 'pointerup', id: 1, x: 160, y: 130 }])
    await page.waitForTimeout(200)
    const panned = await read()
    expect(Math.round(panned.x - before.x)).toBe(60)
    expect(Math.round(panned.y - before.y)).toBe(30)
    expect(panned.k).toBe(before.k)
    await touch([{ type: 'pointerdown', id: 1, x: 100, y: 100 }, { type: 'pointerdown', id: 2, x: 200, y: 100 }, { type: 'pointermove', id: 2, x: 300, y: 100 }, { type: 'pointerup', id: 2, x: 300, y: 100 }, { type: 'pointerup', id: 1, x: 100, y: 100 }])
    await page.waitForTimeout(200)
    const pinched = await read()
    expect(pinched.k / panned.k).toBeGreaterThan(1.8)
    expect(pinched.k / panned.k).toBeLessThan(2.2)
    // Trackpad: ten small wheel ticks pan by their sum; a small ctrl wheel
    // zooms by a few percent, never a fixed step.
    const wheel = async (deltaY: number, ctrl: boolean, times: number) =>
      svg.evaluate((el, { deltaY, ctrl, times }) => {
        const r = el.getBoundingClientRect()
        for (let i = 0; i < times; i++) el.dispatchEvent(new WheelEvent('wheel', { deltaX: 0, deltaY, ctrlKey: ctrl, clientX: r.left + r.width / 2, clientY: r.top + r.height / 2, bubbles: true, cancelable: true }))
      }, { deltaY, ctrl, times })
    const start = await read()
    await wheel(5, false, 10)
    await page.waitForTimeout(200)
    const scrolled = await read()
    expect(Math.round(start.y - scrolled.y)).toBe(50)
    await wheel(-10, true, 1)
    await page.waitForTimeout(200)
    const zoomed = await read()
    expect(zoomed.k / scrolled.k).toBeGreaterThan(1.09)
    expect(zoomed.k / scrolled.k).toBeLessThan(1.12)
  })
})

test('an admin sees every build, and a handler starter draws no query parse as a sink', async ({ page }) => {
  await page.goto('/v1/auth/dev/login?user=admin')
  await page.goto('/app/atlas')
  await expect(page.getByRole('heading', { name: 'Indexed sources' })).toBeVisible()
  await expect(page.getByText(rows)).toBeVisible({ timeout: 60_000 })
  await expect(page.getByTitle('mattermost/mattermost-plugin-github')).toBeVisible()
  await expect(page.getByTitle('https://openrouter.ai')).toBeVisible()
  // The build badge carries the indexer's version alone.
  await expect(page.getByText(/^vig /)).toHaveCount(0)
  await page.getByTitle('mattermost/mattermost-plugin-github').click()
  await drawn(page, 'a handler and what it reaches', 'calls')
  await expect(page.getByText(/\d+ nodes/)).toBeVisible()
  const titles = await page.locator('svg[aria-label="The result as a graph"] g > title').allTextContents()
  expect(titles.length).toBeGreaterThan(0)
  expect(titles.filter((t) => /URL\.Query/i.test(t))).toEqual([])
})

test('a starter pressed below the fold brings the box back into view', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 700 })
  await open(page)
  // The rail scrolls within the viewport, so every starter is reachable
  // without leaving the box; the page is scrolled past it anyway here.
  const rail = page.locator('main aside')
  expect(await rail.evaluate((el) => getComputedStyle(el).overflowY)).toBe('auto')
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
  await rail.evaluate((el) => el.scrollTo(0, el.scrollHeight))
  await page.getByRole('button', { name: 'hosts the code names' }).click()
  await expect(page.getByRole('textbox', { name: 'SQL' })).toBeInViewport()
  await expect(page.getByText(rows)).toBeVisible({ timeout: 60_000 })
})

test('the picked build and starter live in the URL and survive a reload', async ({ page }) => {
  await open(page)
  // The chip by its exact title: result cells carry titles that start the same.
  const chip = page.locator('main aside button[title="mattermost/mattermost-plugin-github"]')
  await expect(chip).toContainText(/\d+ repos/, { timeout: 60_000 })
  await chip.click()
  await page.getByRole('button', { name: 'routes and handlers' }).click()
  await expect(page.getByText(rows)).toBeVisible({ timeout: 60_000 })
  expect(new URL(page.url()).searchParams.get('build')).toMatch(/^[0-9a-f-]{36}$/)
  expect(new URL(page.url()).searchParams.get('q')).toBe('routes and handlers')
  await page.reload()
  await expect(page.getByText(rows)).toBeVisible({ timeout: 60_000 })
  await expect(chip).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'routes and handlers' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('textbox', { name: 'SQL' })).toHaveValue(/FROM routes/)
  await expect(page.getByText('mattermost/mattermost-plugin-github', { exact: true }).first()).toBeVisible()
})

test('every starter runs on all builds without an engine error', async ({ page }) => {
  // The shortest-way search walks every build's flows, which takes most of a minute.
  test.setTimeout(300_000)
  await open(page)
  for (const s of STARTERS) {
    await page.getByRole('button', { name: s.label, exact: true }).click()
    await expect(page.getByRole('textbox', { name: 'SQL' })).toHaveValue(s.sql)
    await expect(page.getByRole('button', { name: /^run/ })).toHaveAttribute('aria-busy', 'false', { timeout: 90_000 })
    await expect(page.getByText('error', { exact: true }), s.label).toHaveCount(0)
  }
})

test('the run button shows a check when a run ends well, and not on an error', async ({ page }) => {
  await open(page)
  await page.getByRole('textbox', { name: 'SQL' }).fill('SELECT 1 AS one;')
  await page.getByRole('button', { name: /^run/ }).click()
  const check = page.getByRole('button', { name: /^run/ }).locator('svg[data-done="true"]')
  await expect(check).toBeVisible({ timeout: 10_000 })
  await expect(check).toHaveCount(0, { timeout: 5_000 })
  await expect(page.getByRole('button', { name: /^run/ }).locator('kbd')).toBeAttached()
  await page.getByRole('textbox', { name: 'SQL' }).fill('SELECT nope FROM functions;')
  await page.getByRole('button', { name: /^run/ }).click()
  await expect(page.getByText('error', { exact: true })).toBeVisible({ timeout: 30_000 })
  await expect(check).toHaveCount(0)
})

test('a picked node colours its first relations and the edges to them', async ({ page }) => {
  await open(page)
  await page.locator('main aside button[title="mattermost/mattermost-plugin-github"]').click()
  await page.getByRole('button', { name: 'a handler and what it reaches', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'graph' })).toBeVisible({ timeout: 60_000 })
  await page.getByRole('tab', { name: 'graph' }).click()
  await expect(page.getByText(/around/)).toBeVisible({ timeout: 30_000 })
  const svg = page.locator('svg[aria-label="The result as a graph"]')
  await node(page, 'fn:mattermost/mattermost-plugin-github:server/plugin/api.go:getConnected:592').dispatchEvent('click')
  // Every node one relation away is coloured, and no other.
  const expected = await svg.evaluate((el) => {
    const id = 'fn:mattermost/mattermost-plugin-github:server/plugin/api.go:getConnected:592'
    const out = new Set<string>()
    for (const p of el.querySelectorAll('path > title')) {
      const m = (p.textContent ?? '').match(/^(?:\S+ )?(\S+) → (\S+)$/)
      if (m && m[1] === id) out.add(m[2]!)
      if (m && m[2] === id) out.add(m[1]!)
    }
    return [...out].sort()
  })
  const coloured = await svg.locator('g[role=button]:has(circle[data-touched]) > title').allTextContents()
  expect(expected.length).toBeGreaterThan(0)
  expect(coloured.sort()).toEqual(expected)
  await expect(svg.locator('path[data-touched]')).toHaveCount(expected.length)
})

test.describe('a tap on a phone', () => {
  test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true })
  test('picks the node under the finger, not one the pane puts there after', async ({ page }) => {
    await open(page)
    await page.locator('main aside button[title="https://openrouter.ai"]').click()
    await page.getByRole('button', { name: 'a value that travels', exact: true }).click()
    // The opening result draws too, so wait for this starter's own rows.
    await expect(page.getByRole('textbox', { name: 'SQL' })).toHaveValue(/sent_in/)
    await expect(page.getByRole('button', { name: /^run/ })).toHaveAttribute('aria-busy', 'false', { timeout: 60_000 })
    await page.getByRole('tab', { name: 'graph' }).click()
    await expect(page.getByText(/around/)).toBeVisible({ timeout: 30_000 })
    await page.waitForTimeout(1500)
    const svg = page.locator('svg[aria-label="The result as a graph"]')
    await expect(svg.locator('g[role=button] > title', { hasText: /^value:/ }).first()).toBeAttached()
    await svg.scrollIntoViewIfNeeded()
    const target = await svg.evaluate((s) => {
      const r = s.getBoundingClientRect()
      let best: { id: string; x: number; y: number; w: number } | null = null
      for (const g of s.querySelectorAll('g[role=button]')) {
        const b = g.querySelector('circle:last-of-type')!.getBoundingClientRect()
        const x = b.x + b.width / 2
        const y = b.y + b.height / 2
        if (x < r.left + 10 || x > r.right - 10 || y < r.top + 10 || y > r.bottom - 10) continue
        if (!best || b.width > best.w) best = { id: g.querySelector('title')!.textContent!, x, y, w: b.width }
      }
      return best!
    })
    const top = await page.evaluate(() => window.scrollY)
    await page.touchscreen.tap(target.x, target.y)
    await expect(svg.locator('g[role=button][aria-pressed="true"] > title')).toHaveText(target.id)
    await page.waitForTimeout(600)
    await expect(svg.locator('g[role=button][aria-pressed="true"] > title')).toHaveText(target.id)
    // The picture stays where it was, with its colour.
    expect(Math.abs((await page.evaluate(() => window.scrollY)) - top)).toBeLessThan(40)
    await expect(svg.locator('circle[data-touched]').first()).toBeVisible()
  })
})

test('a picked node with few relations has them all inside the frame, a result of nodes too', async ({ page }) => {
  await open(page)
  await page.locator('main aside button[title="mattermost/mattermost-plugin-github"]').click()
  await page.getByRole('button', { name: 'routes and handlers', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'graph' })).toBeVisible({ timeout: 60_000 })
  await page.getByRole('tab', { name: 'graph' }).click()
  await expect(page.getByText(/around/)).toBeVisible({ timeout: 30_000 })
  await page.waitForTimeout(1000)
  const svg = page.locator('svg[aria-label="The result as a graph"]')
  await svg.locator('g[role=button]', { has: page.locator('title', { hasText: /getCurrentUser:21$/ }) }).first().dispatchEvent('click')
  await page.waitForTimeout(1200)
  const m = await svg.evaluate((s) => {
    const fr = s.parentElement!.getBoundingClientRect()
    const touched = [...s.querySelectorAll('g[role=button]:has(circle[data-touched])')].map((g) => g.querySelector('circle:last-of-type')!.getBoundingClientRect())
    return { touched: touched.length, outside: touched.filter((b) => b.left < fr.left || b.right > fr.right || b.top < fr.top || b.bottom > fr.bottom).length }
  })
  expect(m.touched).toBeGreaterThan(0)
  expect(m.outside).toBe(0)
})

test('a journey counts every carrier, and fit frames everything drawn', async ({ page }) => {
  await open(page)
  await page.locator('main aside button[title="https://openrouter.ai"]').click()
  await page.getByRole('button', { name: 'parameters endpoints share', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'SQL' })).toHaveValue(/'sends'/)
  await expect(page.getByRole('button', { name: /^run/ })).toHaveAttribute('aria-busy', 'false', { timeout: 60_000 })
  await page.getByRole('tab', { name: 'graph' }).click()
  await expect(page.getByText(/around/)).toBeVisible({ timeout: 30_000 })
  const svg = page.locator('svg[aria-label="The result as a graph"]')
  await svg.locator('g[role=button]', { has: page.locator('title', { hasText: /^param:id$/ }) }).first().dispatchEvent('click')
  // Before "more" is pressed the count already says how many carry it.
  const more = page.getByRole('button', { name: /\d+ more/ })
  await expect(more).toBeVisible({ timeout: 30_000 })
  const before = await page.getByText(/\d+ yield · \d+ carry/).innerText()
  await more.click()
  await expect(page.getByRole('button', { name: /\d+ more/ })).toHaveCount(0)
  await expect(page.getByText(/\d+ yield · \d+ carry/)).toHaveText(before)
  await svg.locator('g[role=button][aria-pressed="true"]').dispatchEvent('click')
  await page.getByRole('button', { name: 'fit' }).click()
  await page.waitForTimeout(400)
  const outside = await svg.evaluate((s) => {
    const fr = s.parentElement!.getBoundingClientRect()
    return [...s.querySelectorAll('g[role=button] circle:last-of-type')].map((c) => c.getBoundingClientRect()).filter((b) => b.left < fr.left - 1 || b.right > fr.right + 1 || b.top < fr.top - 1 || b.bottom > fr.bottom + 1).length
  })
  expect(outside).toBe(0)
})

test('fit pressed before the relations around a result arrive frames them when they do', async ({ page }) => {
  await open(page)
  await page.locator('main aside button[title="mattermost/mattermost-plugin-github"]').click()
  await page.getByRole('button', { name: 'a handler and what it reaches', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'SQL' })).toHaveValue(/getConnected/)
  await expect(page.getByRole('button', { name: /^run/ })).toHaveAttribute('aria-busy', 'false', { timeout: 60_000 })
  await page.getByRole('tab', { name: 'graph' }).click()
  // Pressed at once, before the neighbourhood is in.
  await page.getByRole('button', { name: 'fit' }).click()
  const svg = page.locator('svg[aria-label="The result as a graph"]')
  // The legend gains "around N" once the neighbourhood is drawn.
  await expect.poll(() => svg.evaluate((s) => /around\s*\d+/.test(s.closest('.flex.flex-col')?.querySelector('.flex-wrap')?.textContent ?? '')), { timeout: 30_000 }).toBe(true)
  await page.waitForTimeout(800)
  const outside = await svg.evaluate((s) => {
    const fr = s.parentElement!.getBoundingClientRect()
    return [...s.querySelectorAll('g[role=button] circle:last-of-type')].map((c) => c.getBoundingClientRect()).filter((b) => b.left < fr.left - 1 || b.right > fr.right + 1 || b.top < fr.top - 1 || b.bottom > fr.bottom + 1).length
  })
  expect(outside).toBe(0)
})
