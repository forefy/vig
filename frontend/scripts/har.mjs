// Capture a site's traffic as a HAR the operator can index: the origin,
// then the same-host links its pages offer, each a real page load in a
// headless Chromium, every response body embedded.
//
//   node scripts/har.mjs https://host OUT.har [pages]
import { mkdir } from 'node:fs/promises'
import { dirname } from 'node:path'
import { chromium } from 'playwright'

const [origin, out, pagesArg] = process.argv.slice(2)
if (!origin || !out) {
  console.error('usage: node scripts/har.mjs https://host OUT.har [pages]')
  process.exit(2)
}
const limit = Number(pagesArg ?? 12)
const host = new URL(origin).host
await mkdir(dirname(out), { recursive: true })

const browser = await chromium.launch()
const context = await browser.newContext({ recordHar: { path: out, content: 'embed' }, viewport: { width: 1280, height: 900 } })
const page = await context.newPage()
const seen = new Set([origin])
const queue = [origin]
let n = 0
while (queue.length && n < limit) {
  const url = queue.shift()
  try {
    await page.goto(url, { waitUntil: 'load', timeout: 30_000 })
    await page.waitForTimeout(1500)
  } catch (e) {
    console.error(`${url}: ${String(e.message).split('\n')[0]}`)
    continue
  }
  n++
  console.log(`${n} ${url}`)
  const links = await page.$$eval('a[href]', (as) => as.map((a) => a.href))
  for (const l of links) {
    let u
    try {
      u = new URL(l)
    } catch {
      continue
    }
    if (u.host !== host || u.protocol !== 'https:') continue
    u.hash = ''
    const s = u.toString()
    if (!seen.has(s)) {
      seen.add(s)
      queue.push(s)
    }
  }
}
await context.close()
await browser.close()
console.log(`wrote ${out}: ${n} pages`)
