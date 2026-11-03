import { useEffect, useMemo, useRef, useState } from 'react'
import { Helmet } from 'react-helmet-async'
import { useSearchParams } from 'react-router'
import { Download } from 'lucide-react'
import { $api } from '@/api/hooks'
import { Page, PageHeader } from '@/app/Shell'
import { StateBadge, Empty } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { SkeletonRows, ProblemBadge } from '@/components/ui/states'
import { relTime, cn } from '@/lib/utils'
import { Session, cellText, csv, errorText, lit } from './db'
import { SqlEditor } from './SqlEditor'
import { Graph } from './GraphView'
import { PlaceView } from './Place'
import { drawColumns, idsOf } from './graph'
import type { Column, IndexBuild, Result } from './db'
import { STARTERS, defaultSql } from './starters'

// The plainest view over the indexed sources: the owner's builds, the
// tables with their columns and row counts as the parts declare them, a
// SQL box, and the result as a table or, for edges, a graph. A statement
// runs over every build; a picked build narrows the views underneath it,
// so the statement never changes. DuckDB reads the parts in the browser.

const ROW_CAP = 500
// No shadows yet: one list for every render, so the picture is not laid
// out again while it waits.
const NO_ROWS: Record<string, unknown>[] = []
// A run longer than this shows its clock.
const SLOW_MS = 1000

const fmtSecs = (ms: number) => `${(ms / 1000).toFixed(ms < 1000 ? 3 : ms < 10000 ? 2 : 1)} s`

// shortName is a build by what tells it apart: a repository by its name,
// a site by its host; ownerOf is the rest, the owner or the scheme.
const shortName = (repo: string) => (repo.includes('://') ? repo.slice(repo.indexOf('://') + 3) : repo.slice(repo.lastIndexOf('/') + 1))
const ownerOf = (repo: string) => (repo.includes('://') ? repo.slice(0, repo.indexOf('://') + 3) : repo.slice(0, repo.lastIndexOf('/') + 1))

// A rail: a column on a wide screen, a row of chips that scrolls sideways
// on a narrow one.
const rail = 'flex min-w-0 gap-1 overflow-x-auto lg:flex-col lg:overflow-visible'
const chip = 'shrink-0 rounded-sm border border-transparent text-left hover:bg-accent/40 lg:shrink'

// Sizes in decimal units, as the labels say.
const fmtBytes = (n: number) => (n < 1e3 ? `${n} B` : n < 1e6 ? `${(n / 1e3).toFixed(1)} KB` : n < 1e9 ? `${(n / 1e6).toFixed(1)} MB` : `${(n / 1e9).toFixed(2)} GB`)

export function Atlas() {
  const q = $api.useQuery('get', '/v1/atlas/index')
  const builds = useMemo(() => q.data?.audits ?? [], [q.data])
  const [session, setSession] = useState<Session | null>(null)
  const [openError, setOpenError] = useState<string | null>(null)
  // The picked build lives in the URL, so a link carries it and a reload
  // keeps it.
  const [params, setParams] = useSearchParams()
  const pick = params.get('build') ?? ''
  const setPick = (id: string) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (id) n.set('build', id)
        else n.delete('build')
        return n
      },
      { replace: true },
    )

  useEffect(() => {
    if (!builds.length) return
    let live = true
    let s: Session | null = null
    Session.open(builds)
      .then((x) => {
        s = x
        if (live) setSession(x)
        else void x.close()
      })
      .catch((e) => live && setOpenError(errorText(e)))
    return () => {
      live = false
      setSession(null)
      if (s) void s.close()
    }
  }, [builds])

  const scope = builds.find((b) => b.audit_id === pick) ?? null
  // A build named in the URL that the list lacks (a link from another
  // account, a build since dropped) falls back to every build, and the URL
  // says so.
  useEffect(() => {
    if (pick && builds.length && !scope) setPick('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pick, builds, scope])
  return (
    <Page wide>
      <Helmet>
        <title>Atlas</title>
      </Helmet>
      <PageHeader
        eyebrow="atlas"
        title="Indexed sources"
        aside={
          <div className="flex flex-wrap items-center gap-2">
            <StateBadge state="neutral">
              <span className="num">{builds.length}</span> builds
            </StateBadge>
            {session && <StateBadge state="neutral">duckdb {session.version}</StateBadge>}
          </div>
        }
      />
      {q.isLoading && <SkeletonRows rows={6} />}
      {q.isError && <ProblemBadge error={q.error} onRetry={() => void q.refetch()} />}
      {q.data && !builds.length && <Empty title="no index yet" />}
      {openError && (
        <StateBadge state="bad" className="h-auto max-w-full py-1 whitespace-pre-wrap">
          {openError}
        </StateBadge>
      )}
      {builds.length > 0 && !openError && <Workbench builds={builds} scope={scope} session={session} onPick={setPick} />}
    </Page>
  )
}

// The bench: the rail and the box. The box opens on the first statement
// and runs it once the engine is up; a picked build narrows the session's
// views and runs the statement again, so the box keeps what was typed.
function Workbench({ builds, scope, session, onPick }: { builds: IndexBuild[]; scope: IndexBuild | null; session: Session | null; onPick: (id: string) => void }) {
  // The statement in the box, a counter the rail bumps to run one, and a
  // counter that moves when the views were narrowed or widened.
  // A starter named in the URL opens in the box; the one the box holds is
  // written back, so a link carries it too.
  const [params, setParams] = useSearchParams()
  const [sql, setSql] = useState(STARTERS.find((s) => s.label === params.get('q'))?.sql ?? defaultSql)
  useEffect(() => {
    const label = STARTERS.find((s) => s.sql === sql)?.label
    const want = label && label !== STARTERS[0]!.label ? label : null
    if ((params.get('q') ?? null) === want) return
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (want) n.set('q', want)
        else n.delete('q')
        return n
      },
      { replace: true },
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sql])
  // How many repositories each build holds: an engagement indexes several
  // under one name.
  const [repos, setRepos] = useState<Map<string, number>>(new Map())
  useEffect(() => {
    if (!session || !session.tables.includes('repos')) return
    let live = true
    session
      .run(`SELECT target, count(*) AS n FROM ${session.across('repos')} GROUP BY target`)
      .then((r) => live && setRepos(new Map(r.rows.map((x) => [String(x.target), Number(x.n)]))))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [session])
  const [tick, setTick] = useState(1)
  const [scoped, setScoped] = useState(0)
  // The tick as of now, for the scope effect to see whether a statement
  // already ran after it asked for the new scope.
  const ticks = useRef(tick)
  useEffect(() => {
    ticks.current = tick
  }, [tick])
  // The box is what a press fills and runs; when the page was scrolled
  // past it to reach the rail, it comes back into view.
  const bench = useRef<HTMLDivElement>(null)
  const pickQuery = (text: string) => {
    setSql(text)
    setTick((t) => t + 1)
    const box = bench.current
    if (box && box.getBoundingClientRect().top < 0) box.scrollIntoView({ block: 'start' })
  }
  const target = scope?.audit_id ?? null
  useEffect(() => {
    if (!session) return
    let live = true
    const before = ticks.current
    session
      .scope(target)
      .then((changed) => {
        if (!live || !changed) return
        setScoped((v) => v + 1)
        // A statement pressed meanwhile is queued behind the scope and
        // already ran on it; running it again would only redraw the same.
        if (ticks.current === before) setTick((t) => t + 1)
      })
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [session, target])
  return (
    <div className="grid items-start gap-4 lg:grid-cols-[280px_minmax(0,1fr)] lg:gap-7">
      <aside className="flex min-w-0 flex-col gap-2 lg:sticky lg:top-4 lg:max-h-[calc(100vh-2rem)] lg:gap-3.5 lg:overflow-y-auto">
        <Queries sql={sql} onPick={pickQuery} />
        <Builds builds={builds} repos={repos} picked={target} onPick={onPick} />
        {session ? <Tables session={session} scoped={scoped} /> : <SkeletonRows rows={4} />}
      </aside>
      <div ref={bench} className="min-w-0 scroll-mt-4">
        <Console session={session} scope={scope} sql={sql} onSql={setSql} tick={tick} />
      </div>
    </div>
  )
}

// Every build, under a row for all of them at once, which is the default.
function Builds({ builds, repos, picked, onPick }: { builds: IndexBuild[]; repos: Map<string, number>; picked: string | null; onPick: (id: string) => void }) {
  const bytes = builds.reduce((n, b) => n + b.bytes, 0)
  return (
    <div className="min-w-0 rounded-md border border-border bg-card/50 px-3.5 py-2.5 lg:py-3">
      <span className="label-caps">builds · {builds.length}</span>
      <div className={cn(rail, 'mt-2')}>
        <button type="button" onClick={() => onPick('')} aria-pressed={picked === null} className={cn(chip, 'flex items-baseline gap-2 px-2 py-1.5', picked === null && 'border-border bg-accent/60')}>
          <span className="font-mono text-[12.5px]">all builds</span>
          <span className="font-mono text-[10.5px] text-muted-foreground">{fmtBytes(bytes)}</span>
        </button>
        {builds.map((b) => (
          <button key={b.audit_id} type="button" onClick={() => onPick(b.audit_id)} aria-pressed={b.audit_id === picked} title={b.repo} className={cn(chip, 'flex min-w-0 flex-col gap-1 px-2 py-1.5', b.audit_id === picked && 'border-border bg-accent/60')}>
            <span className="truncate font-mono text-[12.5px]">{shortName(b.repo)}</span>
            <span className="flex items-center gap-1.5 font-mono text-[10.5px] text-muted-foreground lg:flex-wrap">
              <span className="hidden truncate text-faint lg:inline">{ownerOf(b.repo)}</span>
              {b.head_sha.slice(0, 7)}
              <span className="whitespace-nowrap">
                <span className="text-faint">·</span> {fmtBytes(b.bytes)}
              </span>
              {(repos.get(b.audit_id) ?? 1) > 1 && (
                <span className="whitespace-nowrap">
                  <span className="text-faint">·</span> <span className="num">{repos.get(b.audit_id)}</span> repos
                </span>
              )}
              <span className="hidden whitespace-nowrap lg:inline">
                <span className="text-faint">·</span> {relTime(b.built_at)}
              </span>
              <StateBadge state="neutral" className="hidden h-5 px-1.5 text-[10px] lg:inline-flex">
                {b.tool_version}
              </StateBadge>
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

// The statements to start from, one line each; a press fills the box and
// runs it, and the one the box holds stays pressed.
function Queries({ sql, onPick }: { sql: string; onPick: (sql: string) => void }) {
  return (
    <div className="min-w-0 rounded-md border border-border bg-card/50 px-3.5 py-2.5 lg:py-3">
      <span className="label-caps">queries · {STARTERS.length}</span>
      <div className={cn(rail, 'mt-2 lg:gap-0.5')}>
        {STARTERS.map((s) => (
          <button key={s.label} type="button" onClick={() => onPick(s.sql)} aria-pressed={s.sql === sql} className={cn(chip, 'px-2 py-1 font-mono text-[12px] whitespace-nowrap text-muted-foreground hover:text-foreground', s.sql === sql && 'border-border bg-accent/60 text-foreground')}>
            {s.label}
          </button>
        ))}
      </div>
    </div>
  )
}

type TableInfo = { name: string; rows: number; columns: Column[] }

// The tables in scope, as the parts declare them: a count per table and
// its columns under a click. The counts follow the scope.
function Tables({ session, scoped }: { session: Session; scoped: number }) {
  const [info, setInfo] = useState<{ scoped: number; tables: TableInfo[] } | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  // On a narrow screen the list sits behind its heading.
  const [shown, setShown] = useState(false)
  const names = session.tables
  // The columns do not move with the scope, so they are read once.
  const columns = useRef(new Map<string, Column[]>())

  useEffect(() => {
    let live = true
    // A tick later, so a statement the same change set running goes first.
    const t = setTimeout(() => {
      Promise.all(
        names.map(async (name) => {
          const cols = columns.current.get(name) ?? (await session.columns(name))
          columns.current.set(name, cols)
          return { name, rows: await session.count(name), columns: cols }
        }),
      )
        .then((tables) => live && setInfo({ scoped, tables }))
        .catch(() => live && setInfo({ scoped, tables: names.map((name) => ({ name, rows: 0, columns: [] })) }))
    }, 0)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [session, names, scoped])

  const tables = info?.scoped === scoped ? info.tables : null
  return (
    <div className="min-w-0 rounded-md border border-border bg-card/50 px-3.5 py-2.5 lg:py-3">
      <button type="button" onClick={() => setShown((v) => !v)} aria-expanded={shown} className="label-caps flex w-full items-center gap-2 text-left lg:pointer-events-none">
        tables · {names.length}
        <span aria-hidden="true" className="ml-auto text-faint lg:hidden">
          {shown ? '−' : '+'}
        </span>
      </button>
      {!tables && <SkeletonRows rows={names.length || 3} className={cn(!shown && 'hidden lg:block')} />}
      {tables && (
        <div className={cn('mt-2 flex-col font-mono text-[12px]', shown ? 'flex' : 'hidden lg:flex')}>
          {tables.map((t) => (
            <div key={t.name}>
              <button type="button" onClick={() => setOpen(open === t.name ? null : t.name)} aria-expanded={open === t.name} className="flex w-full items-center gap-2 rounded-sm px-2 py-1 text-left hover:bg-accent/40">
                <span className="min-w-0 flex-1 truncate">{t.name}</span>
                <span className="num text-muted-foreground">{t.rows.toLocaleString()}</span>
              </button>
              {open === t.name && (
                <div className="mb-1 ml-2 border-l border-border pl-3 text-[11px] text-muted-foreground">
                  {t.columns.map((c) => (
                    <div key={c.name} className="flex gap-2 py-0.5">
                      <span className="text-foreground">{c.name}</span>
                      <span className="text-faint">{c.type}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function Console({ session, scope, sql, onSql, tick }: { session: Session | null; scope: IndexBuild | null; sql: string; onSql: (v: string) => void; tick: number }) {
  const [result, setResult] = useState<Result | null>(null)
  const [error, setError] = useState<string | null>(null)
  // How many statements are in flight, the clock since the first started,
  // and whether the wait is long enough to show.
  const inflight = useRef(0)
  const [running, setRunning] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  // A run that ended well shows a check on the button for a moment.
  const [done, setDone] = useState(false)
  const doneTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => clearTimeout(doneTimer.current ?? undefined), [])
  // The scope a statement started on, shown while it runs even if the rail
  // moved on, so the label and the result agree.
  const [ranOn, setRanOn] = useState<string | null>(null)
  useEffect(() => {
    if (!running) return
    const started = performance.now()
    const i = setInterval(() => setElapsed(performance.now() - started), 100)
    return () => clearInterval(i)
  }, [running])
  const slow = running && elapsed >= SLOW_MS
  // A result of edges can be drawn; the table is the default. The column
  // descriptor is kept with the result: a new object every render would
  // lay the graph out again and drop the reader's pan and pick.
  const [view, setView] = useState<'table' | 'graph'>('table')
  const cols = useMemo(() => {
    const c = result ? drawColumns(result.columns) : null
    return c && result!.rows.some((r) => idsOf(r, c).length === ('node' in c ? 1 : 2)) ? c : null
  }, [result])
  // Rows the picture grew by, beyond what the statement returned.
  const [expanded, setExpanded] = useState(0)

  // touching asks the edges table for the resolved relations that touch
  // the given nodes. A call drawn by its place (`callee@repo:path:line`)
  // is found by that place, and the calls that come back are named the
  // same way, so the picture stays one; a sink drawn by its place is the
  // same call named `call:`, and what comes back takes the sink's name.
  const touching = async (ids: string[], limit: number) => {
    if (!session) return []
    const plain = ids.filter((id) => id.indexOf('@') < 0)
    const placed = ids.filter((id) => id.indexOf('@') > 0).slice(0, 40)
    const tests: string[] = []
    if (plain.length) tests.push(`e.src_id IN (${plain.map(lit).join(', ')}) OR e.dst_id IN (${plain.map(lit).join(', ')})`)
    for (const id of placed) {
      const like = lit(`call:${id.slice(id.indexOf('@') + 1)}:%`)
      tests.push(`e.src_id LIKE ${like} OR e.dst_id LIKE ${like}`)
    }
    if (!tests.length) return []
    const place = (side: string, alias: string) => `CASE WHEN ${alias}.call_id IS NOT NULL THEN 'call:' || ${alias}.callee_name || '@' || ${alias}.repo || ':' || ${alias}.path || ':' || ${alias}.line ELSE e.${side} END`
    // A capture entry is named by its request, as the result names it.
    const har = session.tables.includes('har_entries')
    const named = har ? `, hs.method || ' ' || hs.path AS src_label, hd.method || ' ' || hd.path AS dst_label` : ''
    const joinHar = har ? `LEFT JOIN har_entries hs ON hs.target = e.target AND 'entry:' || hs.entry_id = e.src_id\nLEFT JOIN har_entries hd ON hd.target = e.target AND 'entry:' || hd.entry_id = e.dst_id\n` : ''
    const more = await session.run(
      `SELECT ${place('src_id', 'cs')} AS src_id, e.src_kind, ${place('dst_id', 'cd')} AS dst_id, e.dst_kind, e.rel, e.confidence, e.evidence${named}\n` +
        `FROM edges e\nLEFT JOIN calls cs ON cs.target = e.target AND 'call:' || cs.call_id = e.src_id\nLEFT JOIN calls cd ON cd.target = e.target AND 'call:' || cd.call_id = e.dst_id\n` +
        joinHar +
        `WHERE (${tests.map((t) => `(${t})`).join(' OR ')}) AND e.confidence >= 1 LIMIT ${limit}`,
    )
    const sinks = new Map(ids.filter((id) => id.startsWith('sink:')).map((id) => [`call:${id.slice('sink:'.length)}`, id]))
    for (const r of more.rows) {
      const s = sinks.get(String(r.src_id))
      const d = sinks.get(String(r.dst_id))
      if (s) r.src_id = s
      if (d) r.dst_id = d
    }
    return more.rows
  }

  // The shadows around a result: every relation that touches its nodes,
  // read once the result is in, so the picture grows its neighbourhood.
  const [shadows, setShadows] = useState<{ of: Result; rows: Record<string, unknown>[] } | null>(null)
  useEffect(() => {
    if (!session || !result || !cols) return
    let live = true
    const ids = [...new Set(result.rows.flatMap((r) => idsOf(r, cols)))].slice(0, 160)
    touching(ids, 1500)
      .then((rows) => live && setShadows({ of: result, rows }))
      .catch(() => live && setShadows({ of: result, rows: [] }))
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, result, cols])

  // A pressed node's relations join the result, so the picture grows from
  // the query.
  const expand = async (id: string) => {
    if (!session || !result || !cols || 'node' in cols) return
    try {
      const started = performance.now()
      const more = await touching([id], 200)
      const seen = new Set(result.rows.map((r) => `${String(r[cols.src])}\u0000${String(r[cols.dst])}\u0000${cols.rel ? String(r[cols.rel]) : ''}`))
      const rows = [...result.rows]
      for (const r of more) {
        const key = `${String(r.src_id)}\u0000${String(r.dst_id)}\u0000${cols.rel ? String(r.rel) : ''}`
        if (seen.has(key)) continue
        seen.add(key)
        rows.push({ ...r, [cols.src]: r.src_id, [cols.dst]: r.dst_id, ...(cols.rel ? { [cols.rel]: r.rel } : {}) })
      }
      setExpanded((n) => n + rows.length - result.rows.length)
      setResult({ ...result, rows, ms: result.ms + (performance.now() - started) })
    } catch (e) {
      setError(errorText(e))
    }
  }

  const run = async (text: string) => {
    if (!session || !text.trim()) return
    if (inflight.current++ === 0) {
      setElapsed(0)
      setRanOn(scope ? scope.repo : 'all builds')
    }
    setRunning(true)
    setError(null)
    setExpanded(0)
    setDone(false)
    let ok = false
    try {
      setResult(await session.run(text))
      ok = true
    } catch (e) {
      setResult(null)
      setError(errorText(e))
    } finally {
      if (--inflight.current === 0) {
        setRunning(false)
        if (ok) {
          setDone(true)
          clearTimeout(doneTimer.current ?? undefined)
          doneTimer.current = setTimeout(() => setDone(false), 1400)
        }
      }
    }
  }

  // A statement the page put in the box runs as soon as the engine is up.
  useEffect(() => {
    if (session && tick > 0) void run(sql)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, tick])

  const save = () => {
    if (!result) return
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([csv(result)], { type: 'text/csv' }))
    a.download = `atlas-${scope ? shortName(scope.repo) : 'all-builds'}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const rows = result?.rows.slice(0, ROW_CAP) ?? []
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="overflow-hidden rounded-md border border-border bg-card/50">
        <div className="relative flex flex-wrap items-center gap-2 border-b border-border bg-muted px-3 py-1.5">
          <span className="label-caps">sql</span>
          <span className="font-mono text-[11px] text-faint">{running && ranOn !== null ? ranOn : scope ? scope.repo : 'all builds'}</span>
          <Button size="sm" onClick={() => void run(sql)} disabled={!session || !sql.trim()} aria-busy={running} className="ml-auto gap-2">
            run
            <span className="inline-flex w-7 justify-center">
              {running ? (
                <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-3.5 animate-spin">
                  <circle cx="8" cy="8" r="6" strokeWidth="2" className="stroke-primary-foreground/25" />
                  <path d="M14 8a6 6 0 0 0-6-6" strokeWidth="2" strokeLinecap="round" className="stroke-primary-foreground" />
                </svg>
              ) : done ? (
                <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" data-done="true" className="atlas-check size-3.5">
                  <path d="M3.5 8.5l3 3 6-6" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" className="stroke-primary-foreground" />
                </svg>
              ) : (
                <kbd className="hidden rounded-sm bg-primary-foreground/15 px-1 font-mono text-[10px] text-primary-foreground/80 [@media(hover:hover)]:inline">⌘↵</kbd>
              )}
            </span>
          </Button>
          {slow && (
            <div aria-hidden="true" className="absolute inset-x-0 -bottom-px h-[2px] overflow-hidden">
              <div className="scan h-full w-1/3" />
            </div>
          )}
        </div>
        <SqlEditor value={sql} onChange={onSql} onRun={() => void run(sql)} />
      </div>

      {slow && !result && !error && <SkeletonRows rows={6} />}

      {error && (
        <div className="overflow-hidden rounded-md border border-bad/40">
          <div className="flex items-center gap-2 bg-bad-soft px-3 py-1.5 text-[12.5px] text-bad">
            <StateBadge state="bad" className="h-5 px-1.5 text-[10.5px]">
              {/(failed|could not) allocate|out of memory/i.test(error) ? 'out of memory' : 'error'}
            </StateBadge>
            <span className="min-w-0 truncate" title={error}>
              {/(failed|could not) allocate|out of memory/i.test(error) ? (error.match(/\(([^)]*used)\)/)?.[1] ?? error.split('\n')[0]) : error.split('\n')[0]}
            </span>
          </div>
          {error.includes('\n') && <pre className="m-0 overflow-x-auto bg-card/50 px-3 py-2 font-mono text-[11.5px] leading-relaxed text-muted-foreground">{error.split('\n').slice(1).join('\n')}</pre>}
        </div>
      )}

      {!error && result && (
        <div className={cn('flex flex-col gap-2.5 transition-opacity', slow && 'opacity-40')}>
          <div className="flex flex-wrap items-center gap-2">
            {slow ? (
              <StateBadge state="neutral" className="animate-pulse">
                running · <span className="num">{(elapsed / 1000).toFixed(1)}</span> s
              </StateBadge>
            ) : (
              <StateBadge state="neutral">
                <span>
                  <span className="num">{result.rows.length.toLocaleString()}</span> rows · <span className="num">{fmtSecs(result.ms)}</span>
                </span>
              </StateBadge>
            )}
            {expanded > 0 && (
              <StateBadge state="neutral">
                +<span className="num">{expanded}</span> expanded
              </StateBadge>
            )}
            {result.rows.length > ROW_CAP && (
              <StateBadge state="warn">
                <span className="num">{ROW_CAP}</span> shown
              </StateBadge>
            )}
            <div className="ml-auto flex items-center gap-2">
              {result.rows.length > 0 && (
                <Button variant="outline" size="sm" onClick={save} disabled={running} className="h-7">
                  <Download className="size-3.5" /> csv
                </Button>
              )}
              {cols && (
                <div role="tablist" aria-label="Result view" className="inline-flex h-7 overflow-hidden rounded-md border border-border font-mono text-[11.5px]">
                  {(['table', 'graph'] as const).map((v) => (
                    <button key={v} type="button" role="tab" aria-selected={view === v} aria-controls="atlas-result" onClick={() => setView(v)} className={cn('px-2.5 text-muted-foreground hover:text-foreground', view === v && 'bg-accent text-foreground')}>
                      {v}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
          {rows.length === 0 ? (
            <Empty title="no rows" />
          ) : cols && view === 'graph' ? (
            <div id="atlas-result" role="tabpanel">
              <Graph rows={result.rows} cols={cols} shadows={shadows?.of === result ? shadows.rows : NO_ROWS} onExpand={'node' in cols ? undefined : (id) => void expand(id)} place={(id) => (session ? <PlaceView key={id} session={session} id={id} /> : null)} />
            </div>
          ) : (
            <div id="atlas-result" role="tabpanel" className="rounded-md border border-border [&_[data-slot=table-container]]:max-h-[70vh] [&_[data-slot=table-container]]:overflow-auto">
              <Table>
                <TableHeader className="sticky top-0 z-[1] bg-muted">
                  <TableRow>
                    {result.columns.map((c, i) => (
                      <TableHead key={i} title={result.types[i]} className="font-mono text-[11px] tracking-[0.04em] whitespace-nowrap text-muted-foreground">
                        {c}
                      </TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((r, i) => (
                    <TableRow key={i}>
                      {result.columns.map((c, ci) => {
                        const text = cellText(r[c], result.types[ci])
                        return (
                          <TableCell key={ci} className="max-w-[420px] truncate py-1.5 font-mono text-[12px]" title={text.length > 60 ? text : undefined}>
                            {text === '' ? <span className="text-faint">·</span> : text}
                          </TableCell>
                        )
                      })}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
