import { useEffect, useRef, useState } from 'react'
import { ExternalLink } from 'lucide-react'
import { Copy, StateBadge } from '@/components/primitives'
import { tint } from '@/components/primitives/CodeSnippet'
import { SkeletonRows } from '@/components/ui/states'
import { cn } from '@/lib/utils'
import { cellText, errorText, lit } from './db'
import type { Session } from './db'

// The place a node names, beside the graph. A function, a parameter, a
// call, a sink or a literal is a span of its file from the `sources`
// table, the lines that carry it marked: green for the place itself,
// red for a sink. A capture entry is its request beside its response.
// A value on the wire is the response that yielded it and the requests
// it was sent in, each on the wire with the line that carries it marked
// and the rest folded; a parameter the same, by its name. A host, a
// name, a resource, a body or a repository is its row.

export type Place =
  | { kind: 'code'; repo?: string; path: string; from: number; to: number; marks: number[]; tone: 'ok' | 'bad'; fn?: string; flowsOf?: string }
  | { kind: 'entry'; entry: string }
  | { kind: 'edges'; id: string }
  | { kind: 'row'; table: string; column: string; value: string }

// Lines of a file shown around a point, and at most of a function.
const AROUND = 8
const FN_MAX = 80

// placeOf reads a node id into the place it names, or null for one with
// no place to show.
export function placeOf(id: string): Place | null {
  const cut = id.indexOf(':')
  if (cut < 0) return null
  const prefix = id.slice(0, cut + 1)
  const rest = id.slice(cut + 1)
  const fnPlace = (fnId: string, extra: Partial<Extract<Place, { kind: 'code' }>>): Place | null => {
    const parts = fnId.split(':')
    if (parts.length < 4) return null
    const line = Number(parts[parts.length - 1])
    if (!Number.isFinite(line)) return null
    return { kind: 'code', repo: parts[0]!, path: parts[1]!, from: line, to: line + FN_MAX - 1, marks: [line], tone: 'ok', fn: fnId, ...extra }
  }
  const at = (text: string, tone: 'ok' | 'bad'): Place | null => {
    // `callee@repo:path:line` (or `callee@path:line` from an older
    // statement), or `repo:path:line[:col]`. A callee carries no `@`; a
    // path may (`@scope/pkg`), so the first one is the cut.
    const a = text.indexOf('@')
    if (a > 0) {
      const parts = text.slice(a + 1).split(':')
      const line = Number(parts[parts.length - 1])
      if (parts.length < 2 || !Number.isFinite(line)) return null
      const repo = parts.length >= 3 ? parts[0] : undefined
      const path = parts.slice(repo ? 1 : 0, -1).join(':')
      return { kind: 'code', ...(repo ? { repo } : {}), path, from: Math.max(1, line - AROUND), to: line + AROUND, marks: [line], tone }
    }
    const parts = text.split(':')
    if (parts.length < 3) return null
    const line = Number(parts[2])
    if (!Number.isFinite(line)) return null
    return { kind: 'code', repo: parts[0]!, path: parts[1]!, from: Math.max(1, line - AROUND), to: line + AROUND, marks: [line], tone }
  }
  switch (prefix) {
    case 'fn:':
      return fnPlace(rest, {})
    case 'var:': {
      // `var:<fn id>#name` inside a function, `var:<repo>:<path>#name` at
      // file level: the latter is placed by the lines its flows touch.
      const hash = rest.lastIndexOf('#')
      const owner = hash > 0 ? rest.slice(0, hash) : rest
      const parts = owner.split(':')
      if (parts.length === 2) return { kind: 'code', repo: parts[0]!, path: parts[1]!, from: 0, to: 0, marks: [], tone: 'ok', flowsOf: id }
      return fnPlace(owner, { flowsOf: id })
    }
    case 'ret:':
      return fnPlace(rest, { flowsOf: id })
    case 'call:':
      return at(rest, 'ok')
    case 'sink:':
      return at(rest, 'bad')
    case 'literal:':
      return at(rest, 'ok')
    case 'entry:':
      return { kind: 'entry', entry: rest }
    case 'value:':
    case 'param:':
      return { kind: 'edges', id }
    case 'host:':
      return { kind: 'row', table: 'hosts', column: 'host', value: rest }
    case 'subdomain:':
      return { kind: 'row', table: 'subdomains', column: 'fqdn', value: rest }
    case 'resource:':
      return { kind: 'row', table: 'cloud_resources', column: 'rid', value: rest }
    case 'body:':
      return { kind: 'row', table: 'bodies', column: 'sha256', value: rest }
    case 'repo:':
      return { kind: 'row', table: 'repos', column: 'repo', value: rest }
    default:
      return null
  }
}

// How many entries a wire journey shows before counting the rest.
const LEGS_MAX = 8

type Bodies = Map<string, Record<string, unknown>>
type Leg = { entry: Record<string, unknown>; side: 'request' | 'response'; where: string }

type Loaded =
  | { kind: 'code'; repo: string; path: string; commit: string; lines: { n: number; text: string }[]; marks: Set<number>; tone: 'ok' | 'bad' }
  | { kind: 'entry'; entry: Record<string, unknown>; bodies: Bodies }
  | { kind: 'wire'; needle: string; byName: boolean; legs: Leg[]; more: number; yields: number; carries: number; bodies: Bodies }
  | { kind: 'row'; table: string; row: Record<string, unknown> | null; types: Record<string, string> }
  | { kind: 'none' }

export function PlaceView({ session, id }: { session: Session; id: string }) {
  const [state, setState] = useState<{ id: string; loaded?: Loaded; error?: string } | null>(null)
  // How many legs a journey shows; a press on the rest shows more.
  const [legsMax, setLegsMax] = useState(LEGS_MAX)
  const place = placeOf(id)
  useEffect(() => {
    if (!place) return
    let live = true
    const done = (loaded: Loaded) => live && setState({ id, loaded })
    const fail = (e: unknown) => live && setState({ id, error: errorText(e) })
    const load = async () => {
      if (place.kind === 'code') {
        let { from, to } = place
        const marks = new Set(place.marks)
        if (place.fn) {
          const f = await session.run(`SELECT line_start, line_end FROM functions WHERE fn_id = ${lit(place.fn)} LIMIT 1`)
          const row = f.rows[0]
          if (row) {
            from = Number(row.line_start)
            to = Math.min(Number(row.line_end), from + FN_MAX - 1)
          }
        }
        if (place.flowsOf) {
          const f = await session.run(`SELECT DISTINCT line FROM flows WHERE src = ${lit(place.flowsOf)} OR dst = ${lit(place.flowsOf)}`)
          for (const r of f.rows) marks.add(Number(r.line))
          if (from === 0) {
            // No function to span: the lines the flows touch, with room around.
            const lines = [...marks].filter((n) => n > 0)
            if (!lines.length) return done({ kind: 'none' })
            from = Math.max(1, Math.min(...lines) - AROUND)
            to = Math.min(Math.max(...lines) + AROUND, from + FN_MAX - 1)
          }
        }
        // Two builds can index one repository (an audit of its own and an
        // engagement that holds it): the lines of the first build only.
        const where = `path = ${lit(place.path)}${place.repo ? ` AND repo = ${lit(place.repo)}` : ''} AND line BETWEEN ${from} AND ${to}`
        const s = await session.run(`SELECT target, repo, line, text FROM sources WHERE ${where} ORDER BY target, repo, line`)
        const first = s.rows[0]
        const repo = first ? String(first.repo) : (place.repo ?? '')
        const lines = s.rows.filter((r) => String(r.repo) === repo && r.target === first!.target).map((r) => ({ n: Number(r.line), text: String(r.text ?? '') }))
        // The commit the build indexed this repository at, for a link to the
        // same lines at their source.
        let commit = ''
        if (first && session.tables.includes('repos')) {
          const c = await session.run(`SELECT commit FROM repos WHERE target = ${lit(String(first.target))} AND repo = ${lit(repo)} LIMIT 1`)
          commit = String(c.rows[0]?.commit ?? '')
        }
        done({ kind: 'code', repo, path: place.path, commit, lines, marks, tone: place.tone })
      } else if (place.kind === 'entry') {
        const e = await session.run(`SELECT * FROM har_entries WHERE entry_id = ${lit(place.entry)} LIMIT 1`)
        const entry = e.rows[0]
        if (!entry) return done({ kind: 'none' })
        done({ kind: 'entry', entry, bodies: await bodiesOf(session, [entry]) })
      } else if (place.kind === 'edges') {
        // The edges that touch the value or the name: a `yields` edge names
        // the response and carries the value after its path, a `sent_in`
        // or `sends` edge names the request and where the value sat.
        const me = lit(place.id)
        const r = await session.run(`SELECT rel, src_id, dst_id, evidence FROM edges WHERE src_id = ${me} OR dst_id = ${me} LIMIT 400`)
        const byName = place.id.startsWith('param:')
        let needle = byName ? place.id.slice('param:'.length) : ''
        const wanted: { id: string; side: Leg['side']; where: string }[] = []
        for (const x of r.rows) {
          const rel = String(x.rel)
          const ev = String(x.evidence ?? '')
          const sep = ev.indexOf(': ')
          if (rel === 'yields') {
            if (!needle && sep >= 0) needle = ev.slice(sep + 2)
            wanted.push({ id: String(x.src_id).slice('entry:'.length), side: 'response', where: sep >= 0 ? ev.slice(0, sep) : ev })
          } else if (rel === 'sent_in') wanted.push({ id: String(x.dst_id).slice('entry:'.length), side: 'request', where: ev })
          else if (rel === 'sends') wanted.push({ id: String(x.src_id).slice('entry:'.length), side: 'request', where: ev })
        }
        const seen = new Set<string>()
        const distinct = wanted.filter((w) => !seen.has(`${w.id}|${w.side}`) && seen.add(`${w.id}|${w.side}`))
        const shown = distinct.slice(0, legsMax)
        const entries = new Map<string, Record<string, unknown>>()
        const ids = [...new Set(shown.map((w) => w.id))]
        if (ids.length) {
          const e = await session.run(`SELECT * FROM har_entries WHERE entry_id IN (${ids.map(lit).join(', ')})`)
          for (const row of e.rows) entries.set(String(row.entry_id), row)
        }
        const bodies = await bodiesOf(session, [...entries.values()])
        const legs = shown
          .flatMap((w) => {
            const entry = entries.get(w.id)
            return entry ? [{ entry, side: w.side, where: w.where }] : []
          })
          .sort((a, b) => (a.side === b.side ? Number(a.entry.started_at) - Number(b.entry.started_at) : a.side === 'response' ? -1 : 1))
        // The counts are of every leg, not only the ones shown.
        const yields = distinct.filter((w) => w.side === 'response').length
        done({ kind: 'wire', needle, byName, legs, more: distinct.length - shown.length, yields, carries: distinct.length - yields, bodies })
      } else {
        if (!session.tables.includes(place.table)) return done({ kind: 'none' })
        const r = await session.run(`SELECT * FROM "${place.table}" WHERE ${place.column} = ${lit(place.value)} LIMIT 1`)
        done({ kind: 'row', table: place.table, row: r.rows[0] ?? null, types: Object.fromEntries(r.columns.map((c, i) => [c, r.types[i] ?? ''])) })
      }
    }
    load().catch(fail)
    return () => {
      live = false
    }
    // The place is read off the id, so the id is the dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, id, legsMax])

  if (!place) return null
  if (!state || state.id !== id) return <SkeletonRows rows={4} />
  if (state.error) {
    return (
      <StateBadge state="warn" className="h-auto max-w-full py-1 whitespace-pre-wrap">
        {state.error}
      </StateBadge>
    )
  }
  const loaded = state.loaded!
  if (loaded.kind === 'none') return null
  if (loaded.kind === 'code') return <Code loaded={loaded} />
  if (loaded.kind === 'entry') return <Entry entry={loaded.entry} bodies={loaded.bodies} />
  if (loaded.kind === 'wire') return <Journey loaded={loaded} onMore={() => setLegsMax((n) => n + 2 * LEGS_MAX)} />
  return <Row table={loaded.table} row={loaded.row} types={loaded.types} />
}

// bodiesOf reads the text of the bodies the entries name.
async function bodiesOf(session: Session, entries: Record<string, unknown>[]): Promise<Bodies> {
  const shas = [...new Set(entries.flatMap((e) => [e.req_body_sha256, e.resp_body_sha256]).filter((x): x is string => typeof x === 'string' && x !== ''))]
  const bodies: Bodies = new Map()
  if (!shas.length) return bodies
  const b = await session.run(`SELECT sha256, content_type, bytes, is_text, text FROM bodies WHERE sha256 IN (${shas.map(lit).join(', ')})`)
  for (const r of b.rows) bodies.set(String(r.sha256), r)
  return bodies
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// needleOf is what to look for on the wire: a value as itself or as a
// query encodes it, a name as a query, form or JSON field.
export function needleOf(text: string, byName: boolean): RegExp {
  const e = escapeRe(text)
  if (byName) return new RegExp(`(^|[?&\\s"'{,])${e}\\s*(=|"\\s*:)`)
  const encoded = encodeURIComponent(text)
  return encoded === text ? new RegExp(e) : new RegExp(`${e}|${escapeRe(encoded)}`)
}

// markLines is the set of lines a needle is on.
export function markLines(lines: string[], needle: RegExp): Set<number> {
  const out = new Set<number>()
  lines.forEach((l, i) => {
    if (needle.test(l)) out.add(i)
  })
  return out
}

// windowOf is a long line cut to a window around the needle, so a one
// line body still shows where the value sits.
export function windowOf(line: string, needle: RegExp, radius = 80): string {
  if (line.length <= radius * 2 + 20) return line
  const m = needle.exec(line)
  if (!m) return `${line.slice(0, radius * 2)}…`
  const from = Math.max(0, m.index - radius)
  const to = Math.min(line.length, m.index + m[0].length + radius)
  return `${from > 0 ? '…' : ''}${line.slice(from, to)}${to < line.length ? '…' : ''}`
}

// A value's or a name's journey: the responses that yielded it, then the
// requests that carried it, each on the wire with its line marked.
function Journey({ loaded, onMore }: { loaded: Extract<Loaded, { kind: 'wire' }>; onMore: () => void }) {
  const { needle, byName, legs, more, bodies } = loaded
  const re = needle ? needleOf(needle, byName) : null
  const { yields, carries } = loaded
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-card/50 px-3 py-2 font-mono text-[11.5px]">
        <span className={cn('text-[10px] tracking-[0.1em] uppercase', byName ? 'text-muted-foreground' : 'text-foreground')}>{byName ? 'name' : 'value'}</span>
        <span className="min-w-0 break-all">{needle || '·'}</span>
        {needle && <Copy value={needle} iconOnly />}
        <span className="ml-auto text-muted-foreground">
          <span className="num text-foreground">{yields}</span> yield · <span className="num text-foreground">{carries}</span> carry
        </span>
        {more > 0 && (
          <button type="button" onClick={onMore} className="h-6 rounded-sm border border-border px-2 text-muted-foreground hover:border-primary hover:text-foreground">
            <span className="num text-foreground">{more}</span> more
          </button>
        )}
      </div>
      {legs.map((l, i) => (
        <Entry key={`${String(l.entry.entry_id)}-${l.side}`} entry={l.entry} bodies={bodies} needle={re ?? undefined} side={l.side} caption={`${l.side === 'response' ? 'yields' : 'carries'} · ${l.where}`} index={i} />
      ))}
    </div>
  )
}

// reveal brings a marked row into the pane's own scroll, a third of the
// way down, or into the page's when the pane has none.
function reveal(el: HTMLElement) {
  let p = el.parentElement
  while (p && !(/(auto|scroll)/.test(getComputedStyle(p).overflowY) && p.scrollHeight > p.clientHeight)) p = p.parentElement
  if (!p) {
    el.scrollIntoView({ block: 'nearest' })
    return
  }
  const top = el.getBoundingClientRect().top - p.getBoundingClientRect().top + p.scrollTop
  if (top < p.scrollTop || top > p.scrollTop + p.clientHeight - 40) p.scrollTop = Math.max(0, top - p.clientHeight / 3)
}

// sourceLink is the place on GitHub, at the indexed commit: the marked
// lines, or the span shown. Only a repository named owner/name at a full
// commit has one.
export function sourceLink(repo: string, commit: string, path: string, from?: number, to?: number): string | null {
  if (!/^[\w.-]+\/[\w.-]+$/.test(repo) || !/^[0-9a-f]{7,40}$/i.test(commit) || !path) return null
  const file = path.split('/').map(encodeURIComponent).join('/')
  const lines = from === undefined ? '' : to !== undefined && to > from ? `#L${from}-L${to}` : `#L${from}`
  return `https://github.com/${repo}/blob/${commit}/${file}${lines}`
}

function Code({ loaded }: { loaded: Extract<Loaded, { kind: 'code' }> }) {
  const { repo, path, commit, lines, marks, tone } = loaded
  const first = [...marks].sort((a, b) => a - b)[0]
  const last = [...marks].sort((a, b) => b - a)[0]
  const href = sourceLink(repo, commit, path, first ?? lines[0]?.n, first !== undefined ? (last !== undefined && last - first <= 40 ? last : first) : lines[lines.length - 1]?.n)
  const width = String(lines[lines.length - 1]?.n ?? 0).length
  const mark = useRef<HTMLSpanElement>(null)
  useEffect(() => {
    if (mark.current) reveal(mark.current)
  }, [loaded])
  return (
    <div className="overflow-hidden rounded-md border border-border">
      <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted px-3 py-1.5 font-mono text-[11px]">
        <span className="text-faint">{repo}</span>
        <span className="min-w-0 truncate text-foreground">{path}</span>
        {first !== undefined && <span className="num text-muted-foreground">:{first}</span>}
        <Copy value={first !== undefined ? `${path}:${first}` : path} iconOnly />
        {href && (
          <a href={href} target="_blank" rel="noreferrer" aria-label="Open at the source" title={href} className="inline-flex text-muted-foreground hover:text-foreground">
            <ExternalLink className="size-3.5" />
          </a>
        )}
        {lines.length > 0 && (
          <span className="num ml-auto text-faint">
            {lines[0]!.n}–{lines[lines.length - 1]!.n}
          </span>
        )}
      </div>
      {lines.length === 0 ? (
        <StateBadge state="neutral" className="m-2">
          no lines
        </StateBadge>
      ) : (
        <pre className="m-0 overflow-x-auto bg-background font-mono text-[12px] leading-[1.6]">
          <code className="block min-w-max py-1.5">
            {lines.map((l) => {
              const on = marks.has(l.n)
              return (
                <span key={l.n} ref={on && l.n === first ? mark : undefined} className={cn('flex', on && (tone === 'bad' ? 'bg-bad-soft/40' : 'bg-accent/70'))}>
                  <span className={cn('num w-[calc(var(--w)*1ch+2.25rem)] shrink-0 pr-3 text-right select-none', on ? (tone === 'bad' ? 'text-bad' : 'text-foreground') : 'text-faint')} style={{ ['--w' as string]: width }}>
                    {l.n}
                  </span>
                  <span className={cn('border-l-2 pr-5 pl-3', on ? (tone === 'bad' ? 'border-bad' : 'border-foreground/70') : 'border-transparent')}>{tint(l.text)}</span>
                </span>
              )
            })}
          </code>
        </pre>
      )}
    </div>
  )
}

// A body's lines as the pane shows them, at most this many.
const BODY_LINES = 400

const decode = (s: string) => {
  try {
    return decodeURIComponent(s.replace(/\+/g, ' '))
  } catch {
    return s
  }
}

// queryLines is a query string one parameter per line, decoded, so a mark
// lands on the parameter that carries the value.
export function queryLines(query: string): string[] {
  if (!query) return []
  return query.split('&').map((kv, i) => {
    const eq = kv.indexOf('=')
    const name = eq < 0 ? kv : kv.slice(0, eq)
    const value = eq < 0 ? '' : kv.slice(eq + 1)
    return `${i === 0 ? '?' : '&'}${decode(name)}${eq < 0 ? '' : ` = ${decode(value)}`}`
  })
}

// bodyLines is a body as lines: JSON laid out one leaf per line, a form
// one field per line, anything else as it came, cut at BODY_LINES.
export function bodyLines(text: string, contentType: string): string[] {
  const ct = contentType.toLowerCase()
  const trimmed = text.trim()
  if (ct.includes('json') || /^[[{]/.test(trimmed)) {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2).split('\n').slice(0, BODY_LINES)
    } catch {
      /* not JSON after all */
    }
  }
  if (ct.includes('x-www-form-urlencoded')) return queryLines(trimmed).map((l) => l.slice(1))
  return text.split('\n').slice(0, BODY_LINES)
}

// A capture entry: the request on the left, the response on the right,
// each as the lines on the wire, the first line of each marked by the
// status, the cookie names and the query under the request. With a
// needle, the lines that carry it on the given side are marked instead
// and the rest fold away.
function Entry({ entry, bodies, needle, side, caption, index }: { entry: Record<string, unknown>; bodies: Bodies; needle?: RegExp; side?: Leg['side']; caption?: string; index?: number }) {
  const status = Number(entry.status)
  const tone: 'ok' | 'bad' = status >= 400 ? 'bad' : 'ok'
  const headers = (v: unknown) => (v && typeof v === 'object' ? Object.entries(v as Record<string, unknown>).map(([k, x]) => `${k}: ${cellText(x)}`) : [])
  const body = (sha: unknown) => {
    const b = typeof sha === 'string' ? bodies.get(sha) : undefined
    if (!b) return []
    const text = typeof b.text === 'string' ? b.text : ''
    if (!text) return [`${cellText(b.bytes)} bytes · ${cellText(b.content_type)}`]
    return bodyLines(text, cellText(b.content_type))
  }
  const request = [`${cellText(entry.method)} ${cellText(entry.path)}`, ...queryLines(typeof entry.query === 'string' ? entry.query : ''), `host: ${cellText(entry.host)}`, ...headers(entry.req_headers), '', ...body(entry.req_body_sha256)]
  const response = [`${status}`, ...headers(entry.resp_headers), '', ...body(entry.resp_body_sha256)]
  const cookies = Array.isArray(entry.cookie_names) ? (entry.cookie_names as unknown[]).map(String) : []
  const reqMarks = needle && side !== 'response' ? markLines(request, needle) : undefined
  const respMarks = needle && side !== 'request' ? markLines(response, needle) : undefined
  return (
    <div className="overflow-hidden rounded-md border border-border">
      <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted px-3 py-1.5 font-mono text-[11px]">
        {index !== undefined && <span className="num text-faint">{index + 1}</span>}
        {caption && <span className={cn('text-[10px] tracking-[0.1em] uppercase', side === 'response' ? 'text-foreground' : 'text-muted-foreground')}>{caption}</span>}
        <span className="truncate text-foreground">
          {cellText(entry.method)} {cellText(entry.path)}
        </span>
        <Copy value={cellText(entry.entry_id)} iconOnly />
        <StateBadge state={tone === 'bad' ? 'bad' : 'ok'} className="h-5 px-1.5 text-[10.5px]">
          {status}
        </StateBadge>
        <span className="num text-muted-foreground">{Number(entry.time_ms).toFixed(0)} ms</span>
        {cookies.map((c) => (
          <StateBadge key={c} state="neutral" className="h-5 px-1.5 text-[10.5px]">
            {c}
          </StateBadge>
        ))}
        <span className="ml-auto text-faint">{when(entry.started_at)}</span>
      </div>
      <div className="@container">
        <div className="grid grid-cols-1 divide-y divide-border @xl:grid-cols-2 @xl:divide-x @xl:divide-y-0">
          <Wire lines={request} tone={tone} marks={reqMarks} needle={needle} dim={side === 'response'} />
          <Wire lines={response} tone={tone} marks={respMarks} needle={needle} dim={side === 'request'} />
        </div>
      </div>
    </div>
  )
}

// when is a timestamp as the engine hands it back: a date, or epoch ms.
function when(v: unknown): string {
  if (v instanceof Date) return v.toISOString()
  if (typeof v === 'number' || typeof v === 'bigint') return new Date(Number(v)).toISOString()
  return cellText(v)
}

// One side of an entry as lines. Without marks the first line carries the
// status tone. With marks, the first line and the marked lines with one
// line of context stay, the rest folds behind a count a press opens, and
// a marked line too long to read is cut to a window around the needle.
function Wire({ lines, tone, marks, needle, dim }: { lines: string[]; tone: 'ok' | 'bad'; marks?: Set<number>; needle?: RegExp; dim?: boolean }) {
  // The folds a press has opened, by the line each starts on; a press on
  // the opened one folds it again.
  const [opened, setOpened] = useState<Set<number>>(new Set())
  const toggle = (from: number) =>
    setOpened((s) => {
      const n = new Set(s)
      if (n.has(from)) n.delete(from)
      else n.add(from)
      return n
    })
  // The side the value is not on keeps its first line only. With nothing
  // marked, the headers and the first lines of the body are kept and the
  // rest folded, so a page's whole markup does not run the pane long.
  const head = lines.indexOf('')
  const cap = head < 0 ? 40 : head + 13
  const keep = (i: number) => i === 0 || (!dim && (marks ? marks.has(i) || marks.has(i - 1) || marks.has(i + 1) : i < cap))
  const rows: React.ReactNode[] = []
  const line = (i: number, on: boolean) => {
    // The request line stays whole; a marked body line is cut to its window.
    const text = on && needle && i > 0 ? windowOf(lines[i]!, needle) : lines[i]!.length > 400 && i > 0 ? `${lines[i]!.slice(0, 400)}…` : lines[i]!
    return (
      <span key={i} className={cn('flex', on && (tone === 'bad' ? 'bg-bad-soft/40' : 'bg-accent/70'))}>
        <span className={cn('border-l-2 pr-5 pl-3', on ? (tone === 'bad' ? 'border-bad' : 'border-foreground/70') : 'border-transparent')}>{text === '' ? ' ' : text}</span>
      </span>
    )
  }
  let i = 0
  while (i < lines.length) {
    if (keep(i)) {
      rows.push(line(i, marks ? marks.has(i) : i === 0))
      i++
      continue
    }
    let j = i
    while (j < lines.length && !keep(j)) j++
    const from = i
    const open = opened.has(from)
    if (open) for (let k = from; k < j; k++) rows.push(line(k, marks ? marks.has(k) : false))
    rows.push(
      <button key={`fold-${from}`} type="button" onClick={() => toggle(from)} className="flex w-full items-center text-left hover:bg-secondary/60" aria-label={open ? `Fold ${j - from} lines` : `Show ${j - from} folded lines`}>
        <span className="num border-l-2 border-transparent pr-5 pl-3 text-[11px] text-faint">{open ? `fold ${j - from} lines` : `⋯ ${j - from} lines`}</span>
      </button>,
    )
    i = j
  }
  return (
    <pre className={cn('m-0 min-w-0 overflow-x-auto bg-background font-mono text-[12px] leading-[1.6]', dim && 'opacity-60')}>
      <code className="block min-w-max py-1.5">{rows}</code>
    </pre>
  )
}

// A row's value is cut here; a body's text is read on the wire, not here.
const cut = (s: string) => (s.length > 400 ? `${s.slice(0, 400)}…` : s)
// The index's own bookkeeping columns, not the row's facts.
const INTERNAL = new Set(['target', 'ingested_at', 'blob_pack'])

function Row({ table, row, types }: { table: string; row: Record<string, unknown> | null; types: Record<string, string> }) {
  return (
    <div className="overflow-hidden rounded-md border border-border">
      <div className="flex items-center gap-2 border-b border-border bg-muted px-3 py-1.5 font-mono text-[11px]">
        <span className="text-foreground">{table}</span>
      </div>
      {!row ? (
        <StateBadge state="neutral" className="m-2">
          no row
        </StateBadge>
      ) : (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-0.5 px-3 py-2 font-mono text-[11.5px]">
          {Object.entries(row).filter(([k]) => !INTERNAL.has(k)).map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="label-caps text-faint">{k}</dt>
              <dd className="min-w-0 break-all">{cut(cellText(v, types[k])) || <span className="text-faint">·</span>}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}
