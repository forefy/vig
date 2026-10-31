import { useCallback, useEffect, useMemo, useState } from 'react'
import { $api } from '@/api/hooks'
import { StateBadge, Empty, Copy } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { SkeletonRows, ProblemBadge } from '@/components/ui/states'
import { relTime, cn } from '@/lib/utils'
import { Session, errorText, lit } from './db'
import type { IndexBuild, Result } from './db'
import { QUESTIONS, renderQuestion, RenderError } from './questions'
import type { Question } from './questions'
import { cellLink, cellText, isSha256, pathColumns } from './cells'

// Ledger archetype, second view of Atlas: the owner's indexes read in the
// browser. Tiles count each table for the picked build, the rail holds the
// builds (the pick is every question's target) and the questions, the main
// column the SQL and its result.

const ROW_CAP = 500

const TILES: { table: string; label: string; tone?: 'bad' | 'warn'; where?: string }[] = [
  { table: 'repos', label: 'repos' },
  { table: 'functions', label: 'functions' },
  { table: 'calls', label: 'calls' },
  { table: 'flows', label: 'flows' },
  { table: 'findings', label: 'findings', tone: 'warn' },
  { table: 'literals', label: 'literals' },
  { table: 'hosts', label: 'hosts' },
  { table: 'har_entries', label: 'requests' },
  { table: 'subdomains', label: 'subdomains' },
  { table: 'cloud_resources', label: 'resources' },
  { table: 'cloud_resources', label: 'public', tone: 'bad', where: 'public' },
  { table: 'edges', label: 'edges' },
]

const fmtBytes = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`)

export function Index() {
  const q = $api.useQuery('get', '/v1/atlas/index')
  const builds = useMemo(() => q.data?.audits ?? [], [q.data])
  const [session, setSession] = useState<Session | null>(null)
  const [openError, setOpenError] = useState<string | null>(null)
  const [pick, setPick] = useState<string>('')

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

  if (q.isLoading) return <SkeletonRows rows={8} />
  if (q.isError) return <ProblemBadge error={q.error} onRetry={() => void q.refetch()} />
  if (!builds.length) return <Empty title="no index yet" />
  if (openError) return <StateBadge state="bad" className="h-auto max-w-full py-1 whitespace-pre-wrap">{openError}</StateBadge>
  if (!session) return <SkeletonRows rows={8} />

  const build = builds.find((b) => b.audit_id === pick) ?? builds[0]!
  return <Ledger session={session} builds={builds} build={build} onPick={setPick} />
}

function Ledger({ session, builds, build, onPick }: { session: Session; builds: IndexBuild[]; build: IndexBuild; onPick: (id: string) => void }) {
  const [question, setQuestion] = useState<Question | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [sql, setSql] = useState('')
  const [result, setResult] = useState<Result | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [running, setRunning] = useState(false)

  const run = useCallback(
    async (text: string) => {
      if (!text.trim()) return
      setRunning(true)
      setError(null)
      try {
        setResult(await session.run(text))
      } catch (e) {
        setResult(null)
        setError(errorText(e))
      } finally {
        setRunning(false)
      }
    },
    [session],
  )

  // A question renders against the picked build; its other params start at their defaults.
  const render = useCallback(
    (qn: Question, vals: Record<string, string>) => {
      try {
        return renderQuestion(qn.body, qn.params, { ...vals, target: build.audit_id })
      } catch (e) {
        if (e instanceof RenderError) setError(e.message)
        return null
      }
    },
    [build.audit_id],
  )

  const choose = (qn: Question) => {
    const vals: Record<string, string> = {}
    for (const p of qn.params) if (p.name !== 'target' && p.default !== null) vals[p.name] = p.default
    setQuestion(qn)
    setValues(vals)
    const text = render(qn, vals)
    if (text !== null) {
      setSql(text)
      void run(text)
    }
  }

  // Picking another build re-renders the open question against it.
  useEffect(() => {
    if (!question) return
    const text = render(question, values)
    if (text !== null) {
      setSql(text)
      void run(text)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [build.audit_id])

  const setParam = (name: string, v: string) => {
    const vals = { ...values, [name]: v }
    setValues(vals)
    if (question) {
      const text = render(question, vals)
      if (text !== null) setSql(text)
    }
  }

  const params = question?.params.filter((p) => p.name !== 'target') ?? []

  return (
    <div className="grid items-start gap-7 lg:grid-cols-[minmax(0,1fr)_300px]">
      <div className="flex min-w-0 flex-col gap-5">
        <Tiles session={session} target={build.audit_id} onPick={(table, where) => {
          setQuestion(null)
          const text = `SELECT * FROM ${table} WHERE target = ${lit(build.audit_id)}${where ? ` AND ${where}` : ''} LIMIT ${ROW_CAP};`
          setSql(text)
          void run(text)
        }} />

        <div className="overflow-hidden rounded-md border border-border bg-card/50">
          <div className="flex flex-wrap items-center gap-2.5 border-b border-border bg-muted px-3 py-1.5">
            <span className="label-caps">{question ? question.title : 'sql'}</span>
            {params.map((p) => (
              <label key={p.name} className="inline-flex items-center gap-1.5 font-mono text-[11.5px] text-muted-foreground">
                {p.name}
                <input
                  value={values[p.name] ?? ''}
                  placeholder={p.default ?? ''}
                  inputMode={p.type === 'int' ? 'numeric' : undefined}
                  spellCheck={false}
                  onChange={(e) => setParam(p.name, e.target.value)}
                  className={cn('h-6 rounded-sm border border-input bg-background px-1.5 font-mono text-[11.5px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring', p.type === 'int' ? 'w-14' : 'w-44')}
                />
              </label>
            ))}
            <span className="ml-auto font-mono text-[11px] text-faint">duckdb {session.version}</span>
            <Button size="sm" onClick={() => void run(sql)} disabled={running || !sql.trim()}>
              {running ? 'running' : 'run'}
            </Button>
          </div>
          <Textarea
            value={sql}
            onChange={(e) => setSql(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                e.preventDefault()
                void run(sql)
              }
            }}
            spellCheck={false}
            placeholder={`SELECT * FROM calls WHERE target = '${build.audit_id}' LIMIT 50;`}
            className="min-h-36 resize-y rounded-none border-0 bg-transparent font-mono text-[12.5px] leading-relaxed shadow-none focus-visible:ring-0"
          />
        </div>

        {error && <StateBadge state="bad" className="h-auto max-w-full py-1 whitespace-pre-wrap">{error}</StateBadge>}
        {!error && result && <Results result={result} />}
      </div>

      <aside className="flex flex-col gap-3.5 lg:sticky lg:top-4">
        <div className="rounded-md border border-border bg-card/50 px-3.5 py-3">
          <span className="label-caps">builds · {builds.length}</span>
          <div className="mt-2 flex flex-col gap-1">
            {builds.map((b) => (
              <button
                key={b.audit_id}
                type="button"
                onClick={() => onPick(b.audit_id)}
                aria-pressed={b.audit_id === build.audit_id}
                className={cn('flex flex-col gap-1 rounded-sm border border-transparent px-2 py-1.5 text-left hover:bg-accent/40', b.audit_id === build.audit_id && 'border-border bg-accent/60')}
              >
                <span className="truncate font-mono text-[12.5px]">{b.repo}</span>
                <span className="flex flex-wrap items-center gap-1.5 font-mono text-[10.5px] text-muted-foreground">
                  {b.head_sha.slice(0, 7)}
                  <span className="text-faint">·</span>
                  {fmtBytes(b.bytes)}
                  <span className="text-faint">·</span>
                  {relTime(b.built_at)}
                  <StateBadge state="neutral" className="h-5 px-1.5 text-[10px]">{b.tool} {b.tool_version}</StateBadge>
                </span>
              </button>
            ))}
          </div>
        </div>
        <div className="rounded-md border border-border bg-card/50 px-3.5 py-3">
          <span className="label-caps">questions · {QUESTIONS.length}</span>
          <div className="mt-2 flex flex-col">
            {QUESTIONS.map((qn) => (
              <button
                key={qn.name}
                type="button"
                title={qn.description}
                onClick={() => choose(qn)}
                className={cn('rounded-sm px-2 py-1 text-left text-[13px] hover:bg-accent/40 hover:text-foreground', question?.name === qn.name ? 'bg-accent/60 text-foreground' : 'text-muted-foreground')}
              >
                {qn.title}
              </button>
            ))}
          </div>
        </div>
      </aside>
    </div>
  )
}

function Tiles({ session, target, onPick }: { session: Session; target: string; onPick: (table: string, where?: string) => void }) {
  // Counts are kept with the target they belong to; another target reads as not loaded yet.
  const [loaded, setLoaded] = useState<{ target: string; counts: number[] } | null>(null)
  const counts = loaded?.target === target ? loaded.counts : null
  useEffect(() => {
    let live = true
    const sql = TILES.map(
      (t, i) => `SELECT ${i} AS i, count(${t.table === 'repos' ? 'DISTINCT repo' : '*'})::BIGINT AS n FROM ${t.table} WHERE target = ${lit(target)}${t.where ? ` AND ${t.where}` : ''}`,
    ).join(' UNION ALL ')
    session
      .run(sql)
      .then((r) => {
        if (!live) return
        const out = TILES.map(() => 0)
        for (const row of r.rows) out[Number(row.i)] = Number(row.n)
        setLoaded({ target, counts: out })
      })
      .catch(() => live && setLoaded({ target, counts: TILES.map(() => 0) }))
    return () => {
      live = false
    }
  }, [session, target])

  return (
    <div className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-4 xl:grid-cols-6">
      {TILES.map((t, i) => {
        const n = counts?.[i]
        return (
          <button key={t.label} type="button" onClick={() => onPick(t.table, t.where)} className="flex flex-col gap-1 bg-card px-3.5 py-3 text-left hover:bg-accent/40">
            <span className="label-caps">{t.label}</span>
            <span className={cn('num text-[22px] leading-none tracking-tight', n === undefined || n === 0 ? 'text-faint' : t.tone === 'bad' ? 'text-bad' : t.tone === 'warn' ? 'text-warn' : '')}>
              {n === undefined ? '·' : n.toLocaleString()}
            </span>
          </button>
        )
      })}
    </div>
  )
}

function Results({ result }: { result: Result }) {
  const rows = result.rows.slice(0, ROW_CAP)
  const paths = pathColumns(result.columns)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <StateBadge state="neutral">
          <span className="num">{result.rows.length.toLocaleString()}</span> rows · <span className="num">{result.ms.toFixed(0)}</span> ms
        </StateBadge>
        {result.rows.length > ROW_CAP && <StateBadge state="warn"><span className="num">{ROW_CAP}</span> shown</StateBadge>}
      </div>
      {paths && rows.length > 0 && (
        <div className="flex flex-col gap-1 rounded-md border border-border bg-card/50 px-3 py-2.5 font-mono text-[11.5px]">
          {rows.slice(0, 40).map((r, i) => (
            <div key={i} className="flex min-w-0 items-center gap-2">
              <span className="truncate">{cellText(r[paths.src])}</span>
              <StateBadge state="neutral" className="h-5 shrink-0 px-1.5 text-[10px]">{cellText(r[paths.rel])}</StateBadge>
              <span className="truncate text-muted-foreground">{cellText(r[paths.dst])}</span>
            </div>
          ))}
        </div>
      )}
      {rows.length === 0 ? (
        <Empty title="no rows" />
      ) : (
        <div className="overflow-x-auto rounded-md border border-border">
          <Table>
            <TableHeader>
              <TableRow>
                {result.columns.map((c, i) => (
                  <TableHead key={c} title={result.types[i]} className="label-caps whitespace-nowrap">{c}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r, i) => (
                <TableRow key={i}>
                  {result.columns.map((c) => (
                    <TableCell key={c} className="max-w-[360px] py-1.5 font-mono text-[12px]">
                      <Cell column={c} value={r[c]} />
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}

function Cell({ column, value }: { column: string; value: unknown }) {
  if (value === null || value === undefined) return <span className="text-faint">null</span>
  const text = cellText(value)
  const href = cellLink(column, value)
  if (href)
    return (
      <a href={href} target="_blank" rel="noreferrer noopener" className="block truncate hover:underline" title={text}>
        {text}
      </a>
    )
  if (isSha256(value))
    return (
      <span className="inline-flex items-center gap-1.5">
        <span title={text}>{text.slice(0, 12)}</span>
        <Copy value={text} iconOnly />
      </span>
    )
  return <span className="block truncate" title={text}>{text}</span>
}
