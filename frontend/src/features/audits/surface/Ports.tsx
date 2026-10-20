import { useEffect, useMemo, useRef, useState } from 'react'
import { StateBadge } from '@/components/primitives'
import { cn, relTime } from '@/lib/utils'
import { coverageOf } from './types'
import type { Evidence, Program, Surface } from './types'
import { buildModel } from './model'
import { SystemMap } from './SystemMap'
import { InstructionView } from './InstructionView'
import { Dock } from './Dock'
import { Strip } from './Strip'
import type { LiveState } from './live'
import { usePlayer } from './usePlayer'
import { time } from './types'
import { baseLife } from './LivePulses'
import { programName } from './parts'
import type { Act, EdgePick, Lens, Level } from './parts'

// windowMarks bounds the transaction marks drawn on the strip.
const windowMarks = 200

// The surface in two fixed regions. The map is the place: structure nests,
// so one breadcrumb walks it, system to program to instruction. The dock is
// what happened there: the sampled transactions and the accounts of
// wherever the map stands. A picked wire or account filters the dock; a
// picked transaction opens in it and lights its route on the map.
export function Ports({ surface, evidence, programs, reportHref, live, auditId, admin }: { surface: Surface; evidence: Evidence[]; programs: Program[]; reportHref?: string; live: LiveState; auditId: string; admin: boolean }) {
  const sampled = useMemo(() => buildModel(surface, evidence, programs), [surface, evidence, programs])
  // Where the reader stands in time: null is now. Back in time, the rows
  // around that moment, or the ones played through, replace the live ring.
  const player = usePlayer(auditId, admin)
  const at = player.at
  // Live, the dock reads the ring plus the rows read in full, which a
  // busy program pushes out of the ring in seconds.
  const rows = useMemo(() => {
    const base = at === null ? live.rows : player.rows
    if (live.full.length === 0) return base
    const seen = new Set(base.map((e) => e.id ?? `${e.node_id}:${e.ref}`))
    const full = live.full.filter((e) => !seen.has(e.id ?? `${e.node_id}:${e.ref}`) && (at === null || time(e.at ?? e.created_at) <= at))
    return full.length === 0 ? base : [...full, ...base]
  }, [at, player.rows, live.rows, live.full])
  // The page opens on a short replay of what just happened, at speed,
  // that catches up with now and goes live; once.
  const opened = useRef(false)
  useEffect(() => {
    if (opened.current || live.reads === 0) return
    opened.current = true
    const newest = live.rows.length ? time(live.rows[live.rows.length - 1].at ?? live.rows[live.rows.length - 1].created_at) : 0
    if (newest > 0 && Date.now() - newest < 60 * 60 * 1000) player.playFrom(Date.now() - 20_000 * player.speed)
  }, [live.reads, live.rows, player])
  // What the watch saw joins the sample in the dock and lights routes
  // the same way; the rows and bars of the map stay the sample's.
  const model = useMemo(() => {
    if (rows.length === 0) return sampled
    const m = buildModel(surface, rows, programs)
    const txs = new Map(sampled.txs)
    for (const t of m.txs.values()) if (!txs.has(t.sig)) txs.set(t.sig, { ...t, live: true })
    const refs = new Set(sampled.drift.map((d) => d.ev.ref))
    const drift = [...sampled.drift, ...m.drift.filter((d) => !refs.has(d.ev.ref))].sort((a, b) => time(b.at) - time(a.at))
    return { ...sampled, txs, drift }
  }, [sampled, surface, rows, programs])
  // Live rows pulse as they arrive; the first read replays its newest rows
  // once, muted, so the map moves on arrival even when the chain is quiet.
  const liveMap = useMemo(() => ({ rows: at === null ? live.rows : player.flight, at, life: at === null ? baseLife : player.life, totals: live.totals }), [at, live.rows, live.totals, player.flight, player.life])
  // The transactions in view, in time order: what the arrow keys step through.
  const inTime = useMemo(() => [...model.txs.values()].filter((t) => at === null || time(t.at) <= at).sort((a, b) => time(a.at) - time(b.at)), [model.txs, at])

  const hashed = decodeURIComponent(window.location.hash).startsWith('#n-') ? decodeURIComponent(window.location.hash).slice(3) : ''
  const first = useMemo(() => {
    const all = [...model.ixs.values()]
    return (model.ixs.get(hashed) ?? all.find((x) => x.replays.length > 0) ?? all.sort((a, b) => b.txs.length - a.txs.length)[0])?.id ?? ''
  }, [model, hashed])
  const defaultOpen = useMemo(() => {
    const open = new Set<string>()
    for (const p of model.programs) for (const g of p.groups ?? []) if (g.ixs.some((x) => x.txs.length > 0)) open.add(`${p.pid}:${g.key}`)
    return open
  }, [model])

  const [state, setState] = useState<{ level: Level; prog: string; sel: string; tx: string | null; acct: string | null; edge: EdgePick | null; tab: 'tx' | 'acct'; open: Set<string> | null }>({
    level: hashed && model.ixs.has(hashed) ? 'instruction' : 'system', prog: '', sel: '', tx: null, acct: null, edge: null, tab: 'tx', open: null,
  })
  const sel = model.ixs.has(state.sel) ? state.sel : first
  const selPid = model.ixs.get(sel)?.pid ?? model.programs[0]?.pid ?? ''
  const lens: Lens = {
    level: state.level,
    prog: state.level === 'instruction' ? selPid : model.programs.some((p) => p.pid === state.prog) ? state.prog : selPid,
    sel,
    tx: state.tx && model.txs.has(state.tx) ? state.tx : null,
    acct: state.acct && model.accounts.has(state.acct) ? state.acct : null,
    edge: state.edge,
    tab: state.tab,
    open: state.open ?? defaultOpen,
  }
  const act: Act = {
    level: (level) => setState((s) => ({ ...s, level, prog: lens.prog, edge: null })),
    program: (prog) => setState((s) => ({ ...s, level: 'program', prog, edge: null })),
    select: (id) => setState((s) => ({ ...s, level: 'instruction', sel: id, edge: null })),
    pickTx: (tx) => setState((s) => ({ ...s, tx, tab: 'tx' })),
    pickAcct: (acct) => setState((s) => ({ ...s, acct, tab: acct ? 'acct' : s.tab })),
    pickEdge: (edge) => setState((s) => ({ ...s, edge, tab: edge ? 'tx' : s.tab })),
    tab: (tab) => setState((s) => ({ ...s, tab })),
    toggle: (g) => setState((s) => {
      const open = new Set(s.open ?? defaultOpen)
      if (open.has(g)) open.delete(g)
      else open.add(g)
      return { ...s, open }
    }),
  }

  // Left and right step through the transactions in view; a step from
  // now leaves live and stands at the newest.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null
      if (el && /^(INPUT|TEXTAREA|SELECT|BUTTON)$/.test(el.tagName)) return
      if (e.key === ' ') {
        e.preventDefault()
        player.toggle()
        return
      }
      if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
      if (inTime.length === 0) return
      e.preventDefault()
      const i = inTime.findIndex((t) => t.sig === lens.tx)
      const next = e.key === 'ArrowLeft' ? (i < 0 ? inTime.length - 1 : Math.max(0, i - 1)) : i < 0 ? inTime.length - 1 : Math.min(inTime.length - 1, i + 1)
      const t = inTime[next]
      setState((s) => ({ ...s, tx: t.sig, tab: 'tx' }))
      if (at === null) player.seek(time(t.at) || Date.now())
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [inTime, lens.tx, at, player])
  const marks = useMemo(() => inTime.filter((t) => t.live).slice(-windowMarks).map((t) => ({ sig: t.sig, at: time(t.at) })), [inTime])

  const ix = model.ixs.get(lens.sel)
  const c = coverageOf(surface)
  const s = surface.sampling
  // When the sampled transactions happened, oldest to newest: the scanned
  // span reaches the head, the transactions may not.
  const sampledSpan = useMemo(() => {
    const ts = evidence.filter((e) => e.kind === 'tx' && e.at && !(e.summary as { live?: boolean } | undefined)?.live).map((e) => time(e.at as string))
    return ts.length > 0 ? [Math.min(...ts), Math.max(...ts)] : null
  }, [evidence])
  const all = [...model.ixs.values()]
  const counts = { observed: all.filter((x) => x.depth[1]).length, reviewed: all.filter((x) => x.depth[3]).length, finding: all.filter((x) => x.node.attention === 'finding').length }
  const web = model.words.web
  // A web map has a capture where a chain map has a sample.
  const requests = web ? evidence.filter((e) => e.kind === 'request').length : 0
  const hasDiff = (c?.changed ?? 0) + (c?.added ?? 0) > 0
  const crumb = (on: boolean) => cn('rounded-sm border px-2 py-0.5', on ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:text-foreground')

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge state="neutral">{all.length} {model.words.ixs}</StateBadge>
        <StateBadge state={counts.observed > 0 ? 'neutral' : 'pending'}>{counts.observed} observed</StateBadge>
        {!web && <StateBadge state={(c?.instructions_replayed ?? 0) > 0 ? 'neutral' : 'pending'}>{c?.instructions_replayed ?? all.filter((x) => x.depth[2]).length} replayed</StateBadge>}
        <StateBadge state={counts.reviewed > 0 ? 'ok' : 'pending'}>{counts.reviewed} reviewed</StateBadge>
        <StateBadge state={counts.finding > 0 ? 'bad' : 'pending'}>{counts.finding} with findings</StateBadge>
        {c && c.edges > 0 && <StateBadge state={c.edges_observed >= c.edges ? 'ok' : 'warn'}>{c.edges_observed} of {c.edges} {model.words.calls} seen</StateBadge>}
        {!web && c && c.signers > 0 && <StateBadge state="neutral">{c.signers} {c.signers === 1 ? model.words.signers.replace(/s$/, '') : model.words.signers}</StateBadge>}
        {hasDiff && <StateBadge state="warn">{c?.changed ?? 0} changed · {c?.added ?? 0} added</StateBadge>}
        {web && <StateBadge state={requests > 0 ? 'neutral' : 'pending'}>capture · {requests} {requests === 1 ? 'request' : 'requests'}</StateBadge>}
        {s && s.transactions > 0 && (
          <StateBadge state="neutral" title={s.from && s.to ? `scanned ${s.from} to ${s.to}` : undefined}>
            sample · {s.transactions} tx
            {sampledSpan ? ` · ${relTime(new Date(sampledSpan[0]).toISOString()).replace(' ago', '')} to ${relTime(new Date(sampledSpan[1]).toISOString())}` : s.from && s.to ? ` · ${relTime(s.from).replace(' ago', '')} to ${relTime(s.to)}` : ''}
          </StateBadge>
        )}
      </div>

      {!web && <Strip live={live} surface={surface} programs={programs} player={player} marks={marks} picked={lens.tx} words={model.words} />}

      <nav aria-label="Surface path" className="flex flex-wrap items-center gap-1.5 font-mono text-[12px]">
        <button type="button" onClick={() => act.level('system')} className={crumb(lens.level === 'system')}>{model.programs.length} {model.programs.length === 1 ? model.words.program : model.words.programs}</button>
        {lens.level !== 'system' && (
          <>
            <span className="text-faint">›</span>
            <button type="button" onClick={() => act.level('program')} className={crumb(lens.level === 'program')}>{programName(model, lens.prog)}</button>
          </>
        )}
        {lens.level === 'instruction' && ix && (
          <>
            <span className="text-faint">›</span>
            <span className={crumb(true)}>{ix.node.name}</span>
          </>
        )}
      </nav>

      {/* Side by side only when the container has room for both; the page
          around this tab differs between the customer and the operator. */}
      <div className="@container">
      <div className="grid grid-cols-1 items-start gap-3 @[1100px]:grid-cols-[minmax(0,1fr)_360px]">
        {lens.level === 'instruction' && ix ? <InstructionView key={ix.id} ix={ix} model={model} lens={lens} act={act} reportHref={reportHref} live={liveMap} sampling={s} /> : <SystemMap model={model} lens={lens} act={act} live={liveMap} />}
        <Dock model={model} lens={lens} act={act} sampling={s ?? undefined} at={at} loading={player.loading} />
      </div>
      </div>
    </div>
  )
}
