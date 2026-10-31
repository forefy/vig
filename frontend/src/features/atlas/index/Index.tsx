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
import { PathGraph } from './PathGraph'

// Ledger archetype, second view of Atlas: the owner's indexes read in the
// browser. The tiles are answers, each a question's count for the picked
// build, shown only when the build holds the kinds of data the question
// reads. The rail holds the builds (the pick is every question's target)
// and the questions; the main column the SQL and its result, drawn as a
// graph when the result is a set of edges.

const ROW_CAP = 500

type Pillar = 'code' | 'web' | 'infra'
type Source = { question: string } | { sql: (target: string) => string }
type Answer = { key: string; label: string; needs: Pillar[]; tone?: 'bad' | 'warn'; count: Source; open: Source }

const ANSWERS: Answer[] = [
  { key: 'paths', label: 'handler → sink', needs: ['code'], tone: 'bad', count: { question: 'handlers_to_sinks' }, open: { question: 'handler_sink_paths' } },
  { key: 'matches', label: 'sink matches', needs: ['code'], tone: 'warn', count: { sql: (t) => `SELECT * FROM findings WHERE target = ${t}` }, open: { question: 'findings_by_repo' } },
  {
    key: 'secrets',
    label: 'secret-like strings',
    needs: ['code'],
    tone: 'bad',
    count: { sql: (t) => `SELECT * FROM literals WHERE target = ${t} AND kind = 'secret_like'` },
    open: { sql: (t) => `SELECT repo, path, line, value FROM literals WHERE target = ${t} AND kind = 'secret_like' ORDER BY repo, path, line;` },
  },
  {
    key: 'endpoints',
    label: 'endpoints in code',
    needs: ['code'],
    count: { sql: (t) => `SELECT DISTINCT value FROM literals WHERE target = ${t} AND kind IN ('url', 'host', 'ip')` },
    open: { sql: (t) => `SELECT value, kind, count(DISTINCT repo) AS repos, count(*) AS places, min(repo || ' ' || path || ':' || line) AS example\nFROM literals WHERE target = ${t} AND kind IN ('url', 'host', 'ip')\nGROUP BY value, kind ORDER BY places DESC, value;` },
  },
  { key: 'public', label: 'public resources', needs: ['infra'], tone: 'bad', count: { question: 'public_resources' }, open: { question: 'public_resources' } },
  { key: 'dangling', label: 'dangling cnames', needs: ['infra'], tone: 'bad', count: { question: 'dangling_cnames' }, open: { question: 'dangling_cnames' } },
  { key: 'on_cloud', label: 'subdomains on cloud', needs: ['infra'], count: { question: 'subdomains_to_cloud' }, open: { question: 'subdomains_to_cloud' } },
  { key: 'named', label: 'resources named in code', needs: ['code', 'infra'], tone: 'warn', count: { question: 'code_mentions_resources' }, open: { question: 'code_resource_subdomain' } },
  { key: 'served', label: 'hosts serving repo code', needs: ['code', 'web'], count: { question: 'bodies_from_repos' }, open: { question: 'host_repo_paths' } },
  {
    key: 'errors',
    label: 'server errors',
    needs: ['web'],
    tone: 'warn',
    count: { sql: (t) => `SELECT * FROM har_entries WHERE target = ${t} AND status >= 500` },
    open: { sql: (t) => `SELECT host, path, method, status, count(*) AS n FROM har_entries WHERE target = ${t} AND status >= 500 GROUP BY ALL ORDER BY n DESC;` },
  },
]

const byName = new Map(QUESTIONS.map((q) => [q.name, q]))
const defaultsOf = (q: Question) => Object.fromEntries(q.params.filter((p) => p.name !== 'target' && p.default !== null).map((p) => [p.name, p.default!]))
const strip = (sql: string) => sql.trim().replace(/;+\s*$/, '')

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

  const render = useCallback(
    (qn: Question, vals: Record<string, string>) => {
      try {
        return renderQuestion(qn.body, qn.params, { ...vals, target: build.audit_id })
      } catch (e) {
        if (e instanceof RenderError) {
          setResult(null)
          setError(e.message)
        }
        return null
      }
    },
    [build.audit_id],
  )

  const ask = (qn: Question, overrides: Record<string, string> = {}) => {
    const vals = { ...defaultsOf(qn), ...overrides }
    setQuestion(qn)
    setValues(vals)
    const text = render(qn, vals)
    setSql(text ?? qn.body)
    if (text !== null) void run(text)
  }

  const runSql = (text: string) => {
    setQuestion(null)
    setValues({})
    setSql(text)
    void run(text)
  }

  const open = (s: Source) => {
    if ('question' in s) {
      const qn = byName.get(s.question)
      if (qn) ask(qn)
    } else runSql(s.sql(lit(build.audit_id)))
  }

  // A node opens its neighbourhood; a sink is reached through the function that calls it.
  const onNode = (id: string) => {
    const qn = byName.get('neighbors')
    if (!qn) return
    const node = id.startsWith('sink:') ? `fn:${id.slice(5, id.indexOf('#'))}` : id
    ask(qn, { node, hops: values.hops && question?.name === 'neighbors' ? values.hops : '1' })
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
      if (text !== null) {
        setError(null)
        setSql(text)
      }
    }
  }

  const params = question?.params.filter((p) => p.name !== 'target') ?? []

  return (
    <div className="grid items-start gap-7 lg:grid-cols-[minmax(0,1fr)_300px]">
      <div className="flex min-w-0 flex-col gap-5">
        <Answers session={session} build={build} active={question?.name} onOpen={open} />

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
                  className={cn('h-6 rounded-sm border border-input bg-background px-1.5 font-mono text-[11.5px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring', p.type === 'int' ? 'w-14' : p.name === 'node' ? 'w-72' : 'w-44')}
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
            className={cn('min-h-24 resize-y rounded-none border-0 bg-transparent font-mono text-[12.5px] leading-relaxed shadow-none focus-visible:ring-0', question ? 'max-h-40' : 'max-h-72')}
          />
        </div>

        {error && <StateBadge state="bad" className="h-auto max-w-full py-1 whitespace-pre-wrap">{error}</StateBadge>}
        {!error && result && <Results key={sql} result={result} graphFirst={question?.view === 'graph'} focus={question?.name === 'neighbors' ? values.node : undefined} onNode={onNode} />}
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
        {(['graph', 'table'] as const).map((view) => (
          <div key={view} className="rounded-md border border-border bg-card/50 px-3.5 py-3">
            <span className="label-caps">{view === 'graph' ? 'paths' : 'lists'}</span>
            <div className="mt-2 flex flex-col">
              {QUESTIONS.filter((qn) => qn.view === view).map((qn) => (
                <button
                  key={qn.name}
                  type="button"
                  title={qn.description}
                  onClick={() => ask(qn)}
                  className={cn('rounded-sm px-2 py-1 text-left text-[13px] hover:bg-accent/40 hover:text-foreground', question?.name === qn.name ? 'bg-accent/60 text-foreground' : 'text-muted-foreground')}
                >
                  {qn.title}
                </button>
              ))}
            </div>
          </div>
        ))}
      </aside>
    </div>
  )
}

// What the build holds, then the answers whose data is there.
function Answers({ session, build, active, onOpen }: { session: Session; build: IndexBuild; active?: string; onOpen: (s: Source) => void }) {
  const target = build.audit_id
  const [loaded, setLoaded] = useState<{ target: string; have: Record<Pillar, number>; repos: number; counts: Record<string, number> } | null>(null)
  const state = loaded?.target === target ? loaded : null

  useEffect(() => {
    let live = true
    const t = lit(target)
    ;(async () => {
      const p = await session.run(
        `SELECT (SELECT count(*) FROM files WHERE target = ${t}) AS code, (SELECT count(DISTINCT repo) FROM files WHERE target = ${t}) AS repos,` +
          ` (SELECT count(*) FROM har_entries WHERE target = ${t}) AS web,` +
          ` (SELECT count(*) FROM subdomains WHERE target = ${t}) + (SELECT count(*) FROM cloud_resources WHERE target = ${t}) + (SELECT count(*) FROM domains WHERE target = ${t}) AS infra`,
      )
      const r = p.rows[0] ?? {}
      const have: Record<Pillar, number> = { code: Number(r.code ?? 0), web: Number(r.web ?? 0), infra: Number(r.infra ?? 0) }
      const counts: Record<string, number> = {}
      for (const a of ANSWERS) {
        if (!a.needs.every((n) => have[n] > 0)) continue
        const body = 'question' in a.count ? (() => {
          const qn = byName.get(a.count.question)!
          return renderQuestion(qn.body, qn.params, { ...defaultsOf(qn), target })
        })() : a.count.sql(t)
        try {
          const c = await session.run(`SELECT count(*)::BIGINT AS n FROM (${strip(body)})`)
          counts[a.key] = Number(c.rows[0]?.n ?? 0)
        } catch {
          counts[a.key] = 0
        }
      }
      if (live) setLoaded({ target, have, repos: Number(r.repos ?? 0), counts })
    })().catch(() => live && setLoaded({ target, have: { code: 0, web: 0, infra: 0 }, repos: 0, counts: {} }))
    return () => {
      live = false
    }
  }, [session, target])

  if (!state) return <div className="h-[118px] rounded-md border border-border bg-card/50" />
  const shown = ANSWERS.filter((a) => a.needs.every((n) => state.have[n] > 0))
  const pillar = (p: Pillar, text: string) => (
    <StateBadge key={p} state="neutral" className={cn(state.have[p] === 0 && 'border-dashed text-faint')}>
      {p} · {state.have[p] === 0 ? 'none' : text}
    </StateBadge>
  )

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-2">
        {pillar('code', `${state.repos} repos`)}
        {pillar('web', `${state.have.web.toLocaleString()} requests`)}
        {pillar('infra', `${state.have.infra.toLocaleString()} records`)}
      </div>
      {shown.length > 0 && (
        <div className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-4">
          {shown.map((a) => {
            const n = state.counts[a.key] ?? 0
            const opens = 'question' in a.open ? a.open.question : null
            return (
              <button key={a.key} type="button" onClick={() => onOpen(a.open)} className={cn('flex flex-col gap-1.5 bg-card px-3.5 py-3 text-left hover:bg-accent/40', opens && opens === active && 'bg-accent/60')}>
                <span className="label-caps">{a.label}</span>
                <span className={cn('num text-[24px] leading-none tracking-tight', n === 0 ? 'text-faint' : a.tone === 'bad' ? 'text-bad' : a.tone === 'warn' ? 'text-warn' : '')}>{n.toLocaleString()}</span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Results({ result, graphFirst, focus, onNode }: { result: Result; graphFirst: boolean; focus?: string; onNode: (id: string) => void }) {
  const paths = pathColumns(result.columns)
  const [view, setView] = useState<'graph' | 'table'>(paths && (graphFirst || focus) ? 'graph' : 'table')
  const rows = result.rows.slice(0, ROW_CAP)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <StateBadge state="neutral">
          <span className="num">{result.rows.length.toLocaleString()}</span> rows · <span className="num">{result.ms.toFixed(0)}</span> ms
        </StateBadge>
        {result.rows.length > ROW_CAP && view === 'table' && <StateBadge state="warn"><span className="num">{ROW_CAP}</span> shown</StateBadge>}
        {paths && (
          <div role="tablist" aria-label="Result view" className="ml-auto inline-flex h-7 overflow-hidden rounded-md border border-border font-mono text-[11.5px]">
            {(['graph', 'table'] as const).map((v) => (
              <button key={v} type="button" role="tab" aria-selected={view === v} onClick={() => setView(v)} className={cn('px-2.5 text-muted-foreground hover:text-foreground', view === v && 'bg-accent text-foreground')}>
                {v}
              </button>
            ))}
          </div>
        )}
      </div>
      {rows.length === 0 ? (
        <Empty title="no rows" />
      ) : paths && view === 'graph' ? (
        <PathGraph rows={result.rows} cols={paths} focus={focus} onNode={onNode} />
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
