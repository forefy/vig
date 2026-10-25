import { useRef, useState } from 'react'
import { StateBadge } from '@/components/primitives'
import { cn } from '@/lib/utils'

// The one query line of the product: facets drawn from the rows on the
// page, clauses as tokens with and / or between them, a box whose
// suggestions are the clauses that would still leave something on the
// page. The report folds findings with it; the surface folds nodes.

export type Facet<T> = { key: string; label: string; values: { value: string; word: string; n: number }[]; valueOf: (f: T) => string; allOf?: (f: T) => string[]; ranked?: readonly string[] }

// countFacet folds a list by one getter, in a given order or sorted.
export function countFacet<T>(items: T[], key: string, label: string, get: (f: T) => string, word: (v: string) => string, orderOf?: readonly string[], ranked = false): Facet<T> {
  const m = new Map<string, number>()
  for (const f of items) m.set(get(f), (m.get(get(f)) ?? 0) + 1)
  const keys = orderOf ? orderOf.filter((k) => m.has(k)) : [...m.keys()].sort()
  return { key, label, valueOf: get, values: keys.map((v) => ({ value: v, word: word(v), n: m.get(v) ?? 0 })), ranked: ranked ? orderOf : undefined }
}

// countMultiFacet folds a list by a getter that yields several values per
// row (a route's methods); a clause holds when any value matches.
export function countMultiFacet<T>(items: T[], key: string, label: string, get: (f: T) => string[], word: (v: string) => string): Facet<T> {
  const m = new Map<string, number>()
  for (const f of items) for (const v of new Set(get(f))) m.set(v, (m.get(v) ?? 0) + 1)
  const keys = [...m.keys()].sort()
  return { key, label, valueOf: (f) => get(f).join(' '), allOf: get, values: keys.map((v) => ({ value: v, word: word(v), n: m.get(v) ?? 0 })) }
}

export type Clause = { facet: string; op: Op; value: string }
export type Op = '=' | '≠' | '~' | '≥' | '≤'
const opOf: Record<string, Op> = { '!=': '≠', '~': '~', '>=': '≥', '<=': '≤', '=': '=' }
const opText: Record<Op, string> = { '=': '=', '≠': '!=', '~': '~', '≥': '>=', '≤': '<=' }
export type Conn = 'and' | 'or'
export type Query = { items: Clause[]; conns: Conn[] }

export function parseQuery(q: string): Query {
  const items: Clause[] = []
  const conns: Conn[] = []
  for (const orPart of q.split('|')) {
    const andParts = orPart.split(',').filter(Boolean)
    andParts.forEach((part, k) => {
      const m = /^([a-z ]+?)(!=|>=|<=|~|=)(.+)$/.exec(part)
      if (!m) return
      if (items.length) conns.push(k === 0 ? 'or' : 'and')
      items.push({ facet: m[1], op: opOf[m[2]] ?? '=', value: m[3] })
    })
  }
  return { items, conns: conns.slice(0, Math.max(0, items.length - 1)) }
}

export function formatQuery(q: Query): string {
  return q.items.map((c, i) => `${i ? (q.conns[i - 1] === 'or' ? '|' : ',') : ''}${c.facet}${opText[c.op]}${c.value}`).join('')
}

export function holds<T>(f: T, facets: Facet<T>[], c: Clause, textOf: (f: T) => string): boolean {
  if (c.op === '~') return textOf(f).toLowerCase().includes(c.value.toLowerCase())
  const g = facets.find((x) => x.key === c.facet)
  if (!g) return true
  if (g.allOf) {
    const has = g.allOf(f).includes(c.value)
    return c.op === '=' ? has : c.op === '≠' ? !has : false
  }
  const v = g.valueOf(f)
  if (c.op === '≥' || c.op === '≤') {
    // Ranked facets list the top first: at least medium is a rank at or
    // above medium's, which is an index at or below it.
    const r = g.ranked ?? []
    const have = r.indexOf(v)
    const want = r.indexOf(c.value)
    if (have < 0 || want < 0) return false
    return c.op === '≥' ? have <= want : have >= want
  }
  return c.op === '=' ? v === c.value : v !== c.value
}

// Evaluate the query: or splits it into groups, and holds within a group.
export function matches<T>(f: T, facets: Facet<T>[], q: Query, textOf: (f: T) => string): boolean {
  if (q.items.length === 0) return true
  let anyGroup = false
  let group = true
  q.items.forEach((c, i) => {
    if (i > 0 && q.conns[i - 1] === 'or') {
      anyGroup = anyGroup || group
      group = true
    }
    group = group && holds(f, facets, c, textOf)
  })
  return anyGroup || group
}

// The one-line query builder: clauses as tokens with an and / or between
// them, then a box whose suggestions are the clauses that would still leave
// something on the page: values present in the current result (for and) or
// in the whole report (for or), never a clause that empties it or changes
// nothing.
export function QueryLine<T>({ facets, all, query, onChange, shown, total, textOf, textLabel = 'title', placeholder = 'severity = medium', ariaLabel = 'Filter findings' }: { facets: Facet<T>[]; all: T[]; query: Query; onChange: (next: Query) => void; shown: number; total: number; textOf: (f: T) => string; textLabel?: string; placeholder?: string; ariaLabel?: string }) {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [cursor, setCursor] = useState(0)
  // null: not chosen, the line picks and, or or when and has nothing to add.
  const [nextConn, setNextConn] = useState<Conn | null>(null)
  const [pickedOp, setPickedOp] = useState<Op>('=')
  // The box closes a beat after blur so a chip's mousedown lands first; a
  // refocus or a click cancels that close.
  const closing = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const show = () => {
    clearTimeout(closing.current)
    setOpen(true)
  }
  type Option = { facet: string; label: string; op: Clause['op']; value: string; word: string; n: number }
  // The clauses that would change the page from here: against the
  // current result for and, the whole report for or.
  const optionsFor = (conn: Conn): { base: T[]; options: Option[] } => {
    const base = query.items.length === 0 || conn === 'or' ? all : all.filter((f) => matches(f, facets, query, textOf))
    const options = facets.flatMap((g) => {
      const counts = new Map<string, number>()
      for (const f of base) for (const v of g.allOf ? new Set(g.allOf(f)) : [g.valueOf(f)]) counts.set(v, (counts.get(v) ?? 0) + 1)
      const rank = (x: string) => (g.ranked ?? []).indexOf(x)
      return g.values.flatMap((v) => {
        const n = counts.get(v.value) ?? 0
        const out: Option[] = []
        if (n > 0 && n < base.length) {
          out.push({ facet: g.key, label: g.label, op: '=', value: v.value, word: v.word, n }, { facet: g.key, label: g.label, op: '≠', value: v.value, word: v.word, n: base.length - n })
        }
        if (g.ranked) {
          const atLeast = base.filter((f) => rank(g.valueOf(f)) >= 0 && rank(g.valueOf(f)) <= rank(v.value)).length
          const atMost = base.filter((f) => rank(g.valueOf(f)) >= rank(v.value)).length
          if (atLeast > 0 && atLeast < base.length) out.push({ facet: g.key, label: g.label, op: '≥', value: v.value, word: v.word, n: atLeast })
          if (atMost > 0 && atMost < base.length) out.push({ facet: g.key, label: g.label, op: '≤', value: v.value, word: v.word, n: atMost })
        }
        return out
      })
    })
    return { base, options }
  }
  // When and can narrow no further (one finding left), or is the only
  // clause worth offering, so the line switches to it.
  let conn: Conn = nextConn ?? 'and'
  let { base, options } = optionsFor(conn)
  if (nextConn === null && query.items.length > 0 && options.length === 0) {
    const alt = optionsFor('or')
    if (alt.options.length > 0) {
      conn = 'or'
      base = alt.base
      options = alt.options
    }
  }
  const needle = text.trim().toLowerCase()
  // Typed words narrow the same palette, in any order: "can" keeps
  // canFill and cancelOrder; a leading "not" or "!" turns the chips
  // into not-equals. The words search a chip's facet, value and word.
  let words = needle.split(/\s+/).filter(Boolean)
  // A typed operator word wins over the strip: "not", ">=" / "at least" / "min", "<=" / "at most" / "max".
  const typedOp: Op | null = words.length === 0 ? null : /^(not|!|!=)$/.test(words[0]) ? '≠' : /^(>=|≥|min|least)$/.test(words[0]) ? '≥' : /^(<=|≤|max|most)$/.test(words[0]) ? '≤' : null
  if (typedOp) words = words.slice(1)
  if (words[0] === 'at' && (words[1] === 'least' || words[1] === 'most')) words = words.slice(1)
  const op: Op = typedOp ?? pickedOp
  const mentions = (hay: string) => words.every((w) => hay.includes(w))
  const has = (o: Option) => query.items.some((c) => c.facet === o.facet && c.op === o.op && c.value === o.value)
  let suggestions: Option[] = options
    .filter((o) => o.op === op && !has(o))
    .filter((o) => mentions(`${o.label} ${o.word} ${o.value}`.toLowerCase()))
  if (needle && op === '=' && !/^(and|or)$/.test(needle)) {
    const n = base.filter((f) => textOf(f).toLowerCase().includes(needle)).length
    if (n > 0 && n < base.length) suggestions = [...suggestions, { facet: textLabel, label: textLabel, op: '~', value: text.trim(), word: `"${text.trim()}"`, n }]
  }
  const add = (o: Option, op: Op = o.op) => {
    onChange({ items: [...query.items, { facet: o.facet, op, value: o.value }], conns: query.items.length ? [...query.conns, conn] : [] })
    setText('')
    setCursor(0)
    setOpen(false)
    setNextConn(null)
    setPickedOp('=')
  }
  const remove = (i: number) => {
    const items = query.items.filter((_, j) => j !== i)
    const conns = [...query.conns]
    conns.splice(Math.max(0, i - 1), 1)
    onChange({ items, conns: conns.slice(0, Math.max(0, items.length - 1)) })
  }
  const flip = (i: number) => onChange({ ...query, conns: query.conns.map((c, j) => (j === i ? (c === 'and' ? 'or' : 'and') : c)) })
  const wordOf = (c: Clause) => {
    const g = facets.find((x) => x.key === c.facet)
    return { label: g?.label ?? c.facet, word: c.op === '~' ? `"${c.value}"` : (g?.values.find((v) => v.value === c.value)?.word ?? c.value) }
  }
  const connButton = (conn: Conn, onClick: () => void, key: string) => (
    <button key={key} type="button" onClick={onClick} className={cn('h-6 rounded-sm border px-1.5 font-mono text-[10.5px] uppercase tracking-wide', conn === 'or' ? 'border-warn/50 text-warn' : 'border-border text-faint hover:text-foreground')}>{conn}</button>
  )
  return (
    <div className="relative">
      <div className="flex min-h-10 flex-wrap items-center gap-1.5 rounded-md border border-border bg-card/50 px-2.5 py-1.5 focus-within:border-primary">
        {query.items.map((c, i) => {
          const w = wordOf(c)
          return [
            i > 0 ? connButton(query.conns[i - 1], () => flip(i - 1), `conn-${i}`) : null,
            <span key={`${c.facet}${c.op}${c.value}${i}`} className="inline-flex h-6 items-center gap-1 rounded-sm border border-primary/50 bg-background px-1.5 font-mono text-[11px]">
              <span className="text-faint">{w.label}</span>
              <span className={c.op === '≠' ? 'text-warn' : 'text-primary'}>{c.op}</span>
              <span className="text-foreground">{w.word}</span>
              <button type="button" aria-label={`Remove ${w.label} ${c.op} ${w.word}`} onClick={() => remove(i)} className="ml-0.5 text-faint hover:text-foreground">×</button>
            </span>,
          ]
        })}
        {query.items.length > 0 && connButton(conn, () => setNextConn(conn === 'and' ? 'or' : 'and'), 'conn-next')}
        <input
          value={text}
          onChange={(e) => {
            const v = e.target.value
            if (query.items.length && /^(and|or) $/i.test(v)) { setNextConn(v.trim().toLowerCase() as Conn); setText(''); setOpen(true); return }
            setText(v); setOpen(true); setCursor(0)
          }}
          onFocus={show}
          onClick={show}
          onBlur={() => { closing.current = setTimeout(() => setOpen(false), 120) }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); setCursor((c) => Math.min(c + 1, suggestions.length - 1)); setOpen(true) }
            else if (e.key === 'ArrowUp') { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)) }
            else if (e.key === 'Enter' && suggestions[cursor]) { e.preventDefault(); add(suggestions[cursor]) }
            else if (e.key === 'Escape') setOpen(false)
            else if (e.key === 'Backspace' && text === '' && query.items.length) remove(query.items.length - 1)
          }}
          placeholder={query.items.length ? '' : placeholder}
          aria-label={ariaLabel}
          className="h-6 min-w-[12rem] flex-1 bg-transparent font-mono text-[12px] outline-none placeholder:text-faint"
        />
        <StateBadge state={shown === total ? 'neutral' : 'warn'} className="ml-auto h-5 px-1.5 text-[10.5px]">{shown} of {total}</StateBadge>
      </div>
      {open && (
        <div role="listbox" className="absolute left-0 z-20 mt-1 flex w-full max-w-[36rem] flex-col gap-0.5 rounded-md border border-border bg-card p-1.5 shadow-md">
          <div className="mb-1 flex items-center gap-1 px-1">
            {(['=', '≠', '≥', '≤'] as const).map((o) => {
              const usable = options.some((x) => x.op === o && !has(x))
              return (
                <button
                  key={o}
                  type="button"
                  disabled={!usable}
                  aria-pressed={op === o}
                  onMouseDown={(e) => { e.preventDefault(); setPickedOp(o); setCursor(0) }}
                  className={cn('h-6 min-w-7 rounded-sm border px-1.5 font-mono text-[12px]', op === o ? (o === '≠' ? 'border-warn text-warn' : 'border-primary text-primary') : 'border-border text-muted-foreground hover:text-foreground', !usable && 'opacity-30')}
                >
                  {o}
                </button>
              )
            })}
          </div>
          {suggestions.length === 0 && <StateBadge state="pending" className="h-5 w-fit px-1.5 text-[10.5px]">{needle ? `no clause and no finding matches "${text.trim()}"` : query.items.length > 0 && options.length === 0 ? `nothing ${conn} can change` : `nothing to ${op}`}</StateBadge>}
          {[...facets.map((g) => ({ key: g.key, label: g.label })), { key: textLabel, label: textLabel }].map((g) => {
            const row = suggestions.filter((o) => o.facet === g.key)
            if (row.length === 0) return null
            return (
              <div key={g.key} className="grid grid-cols-[6.5rem_minmax(0,1fr)] items-baseline gap-2 px-1 py-0.5 sm:grid-cols-[7.5rem_minmax(0,1fr)]">
                <span className="truncate font-mono text-[11px] text-faint">{g.label}</span>
                <div className="flex flex-wrap gap-1">
                  {row.map((o) => {
                    const i = suggestions.indexOf(o)
                    return (
                      <button
                        key={`${o.op}${o.value}`}
                        type="button"
                        role="option"
                        aria-selected={i === cursor}
                        title={`${g.label} ${o.op} ${o.word}`}
                        onMouseDown={(e) => { e.preventDefault(); add(o) }}
                        onMouseEnter={() => setCursor(i)}
                        className={cn('inline-flex h-6 items-center gap-1 rounded-sm border px-1.5 font-mono text-[11px]', i === cursor ? 'border-primary bg-accent text-foreground' : 'border-border text-muted-foreground hover:text-foreground')}
                      >
                        {o.op !== '=' && <span className={o.op === '≠' ? 'text-warn' : 'text-primary'}>{o.op}</span>}
                        <span>{o.word}</span>
                        <span className="num text-[10px] text-faint">{o.n}</span>
                      </button>
                    )
                  })}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

