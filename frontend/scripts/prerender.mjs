// Renders the public routes to static HTML after `vite build`, and writes
// robots.txt and sitemap.xml. Usage: node scripts/prerender.mjs (run by
// `npm run build`). SITE_URL (or VITE_SITE_URL) makes sitemap and canonical
// URLs absolute; without it the sitemap is written with relative paths and a
// warning, which is fine for local builds only.
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const template = readFileSync(resolve(root, 'dist/index.html'), 'utf8')
const { render } = await import(resolve(root, 'dist/server/entry-server.js'))

// Public, indexable routes. /r/:slug pages are dynamic: the API server injects
// their head tags at request time from the same template.
const routes = ['/', '/scan', '/terms', '/privacy']

// React 19 renders <title>, <meta> and <link> where the component put them
// (inside #root) when there is no <head> in the tree; react-helmet-async may
// also report them through its context. Collect both, dedupe, and move
// everything into <head>.
const HEAD_TAG = /<title>[\s\S]*?<\/title>|<meta\b[^>]*\/?>|<link\b[^>]*\/?>/g
function splitHead(html, head) {
  const tags = []
  const seen = new Set()
  const push = (t) => {
    const key = t.replace(/\s+/g, ' ').trim()
    if (!seen.has(key)) {
      seen.add(key)
      tags.push(key)
    }
  }
  for (const m of (head ?? '').match(HEAD_TAG) ?? []) push(m)
  const body = html.replace(HEAD_TAG, (m) => {
    push(m)
    return ''
  })
  return { body, tags }
}

for (const url of routes) {
  const { html, head } = await render(url)
  const { body, tags } = splitHead(html, head)
  const out = template
    .replace('<title>Program audits</title>', '')
    .replace('</head>', `    ${tags.join('\n    ')}\n  </head>`)
    .replace('<div id="root"></div>', `<div id="root">${body}</div>`)
  const file = url === '/' ? 'dist/index.html' : `dist${url}/index.html`
  mkdirSync(dirname(resolve(root, file)), { recursive: true })
  writeFileSync(resolve(root, file), out)
  console.log('prerendered', url, `(${tags.length} head tags)`)
}

// robots and sitemap for the public surface
const site = (process.env.SITE_URL ?? process.env.VITE_SITE_URL ?? '').replace(/\/$/, '')
if (!site) console.warn('prerender: SITE_URL is not set; sitemap URLs will be relative')
writeFileSync(
  resolve(root, 'dist/robots.txt'),
  ['User-agent: *', 'Disallow: /app', 'Disallow: /admin', 'Disallow: /login', 'Disallow: /s/', 'Disallow: /docs', site ? `Sitemap: ${site}/sitemap.xml` : '', ''].filter((l) => l !== undefined).join('\n'),
)
const today = new Date().toISOString().slice(0, 10)
writeFileSync(
  resolve(root, 'dist/sitemap.xml'),
  `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${routes
    .map((r) => `  <url><loc>${site}${r}</loc><lastmod>${today}</lastmod></url>`)
    .join('\n')}\n</urlset>\n`,
)
