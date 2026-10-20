import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { StateBadge, Copy } from '@/components/primitives'
import { cn, relTime, short } from '@/lib/utils'
import { OutcomeBar } from './OutcomeBar'
import { calleeTip } from './trust'
import { extraOf, statsOf } from './types'
import { access, SIGNERS } from './model'
import type { Ix, MapEdge, Model, Outside, ProgramBox } from './model'
import { DepthMark, OutsideNode, Port, SignersNode, Wires, endLabel, interfaceWord, programName, Anchor } from './parts'
import type { Act, EdgePick, Lens, Wire } from './parts'
import { usePorts } from './usePorts'
import { LivePulses, systemRoute } from './LivePulses'
import type { LiveMap as Motion } from './LivePulses'
import type { Total } from './live'
import { reducedMotion } from '@/app/a11y'

const matchState = { verified: 'ok', self_built: 'ok', mismatch: 'bad', unverified: 'neutral' } as const
const matchWord = { verified: 'verified build', self_built: 'self-built', mismatch: 'mismatch', unverified: 'unverified' } as const

// The whole system: who enters, the audited programs with every
// instruction as a row with ports, and what they reach. Wires are calls:
// width is traffic, dashed exists only in source, amber is signed.
export type LiveMap = Motion & { totals: Map<string, Total> }

// foldAt is how many outside programs a side shows before the rest fold
// into one node.
const foldAt = 10
const MORE_IN = 'more:in'
const MORE_OUT = 'more:out'

export function SystemMap({ model, lens, act, live }: { model: Model; lens: Lens; act: Act; live?: LiveMap }) {
  // At program level the other programs fold to their header and the open
  // one shows every group. An instruction that is not drawn as its own row
  // is drawn on its group's row, or on its program's header.
  const focus = lens.level === 'program' ? lens.prog : ''
  const tx = lens.tx ? model.txs.get(lens.tx) : undefined
  // Either side folds to its busiest few; a lit or picked node stays. The
  // rest gather in one node that carries their count, bundles their wires
  // and is where their calls enter or leave.
  const [showIn, setShowIn] = useState(false)
  const [showOut, setShowOut] = useState(false)
  const pickedEdge = lens.edge?.id ?? ''
  const folds = useMemo(() => {
    const onRoute = (id: string) => Boolean(tx && tx.hops.some(([a, b]) => a === id || b === id))
    const keepIn = (o: Outside, i: number) => showIn || i < foldAt || pickedEdge === o.id || onRoute(o.id)
    const keepOut = (o: Outside, i: number) => showOut || o.fog || i < foldAt || pickedEdge === o.id || onRoute(o.id)
    const callers = model.callers.filter(keepIn)
    const foldedCallers = model.callers.filter((c, i) => !keepIn(c, i))
    const targets = model.targets.filter(keepOut)
    const foldedTargets = model.targets.filter((o, i) => !keepOut(o, i))
    return { callers, foldedCallers, targets, foldedTargets, foldedIn: new Set(foldedCallers.map((c) => c.id)), foldedOut: new Set(foldedTargets.map((c) => c.id)) }
  }, [model, tx, showIn, showOut, pickedEdge])
  const { callers, foldedCallers, targets, foldedTargets } = folds
  const anchor = useMemo(() => {
    const m = new Map<string, string>()
    for (const id of folds.foldedIn) m.set(id, MORE_IN)
    for (const id of folds.foldedOut) m.set(id, MORE_OUT)
    for (const p of model.programs) {
      if (focus && p.pid !== focus) {
        for (const ix of p.ixs) m.set(ix.id, `h:${p.pid}`)
        continue
      }
      if (focus) continue
      for (const g of p.groups ?? []) if (!lens.open.has(`${p.pid}:${g.key}`)) for (const ix of g.ixs) m.set(ix.id, `g:${p.pid}:${g.key}`)
    }
    return (id: string) => m.get(id) ?? id
  }, [model, lens.open, focus, folds])

  const lit = useMemo(() => {
    const m = new Map<string, number>()
    for (const [a, b] of tx?.hops ?? []) {
      const id = `${anchor(a)}>${anchor(b)}`
      if (!m.has(id)) m.set(id, m.size + 1)
    }
    return m
  }, [tx, anchor])

  // Wires, bundled per drawn endpoint pair.
  const bundles = useMemo(() => {
    const m = new Map<string, { id: string; from: string; to: string; kind: MapEdge['kind']; count: number; signed: boolean; members: MapEdge[] }>()
    for (const e of model.edges) {
      const from = anchor(e.from), to = anchor(e.to)
      if (from === to) continue
      const id = `${from}>${to}`
      const b = m.get(id) ?? { id, from, to, kind: e.kind, count: 0, signed: false, members: [] }
      b.count += e.count
      b.signed = b.signed || e.signed
      b.members.push(e)
      m.set(id, b)
    }
    return m
  }, [model.edges, anchor])

  const wires: Wire[] = []
  for (const b of bundles.values()) {
    const selOn = b.members.some((e) => e.from === lens.sel || e.to === lens.sel)
    const step = lit.get(b.id)
    wires.push({
      id: b.id,
      from: `o:${b.from}`,
      to: b.kind === 'loop' ? `o:${b.to}` : `i:${b.to}`,
      kind: b.kind === 'loop' ? 'loop' : 'across',
      count: b.count,
      signed: b.signed,
      dashed: b.count === 0,
      state: lens.edge && (lens.edge.id === b.id || lens.edge.id === b.from || lens.edge.id === b.to) ? 'picked' : lens.acct ? 'dim' : tx ? (step ? 'lit' : 'dim') : selOn ? 'on' : 'idle',
      step,
      title: `${endLabel(model, b.from)} → ${endLabel(model, b.to)}`,
    })
  }
  // A program that calls in is itself entered by a signer.
  for (const c of model.callers) {
    const id = `${SIGNERS}>${anchor(c.id)}`
    const step = lit.get(id)
    if (step) wires.push({ id, from: `s:${SIGNERS}`, to: `n:${anchor(c.id)}`, kind: 'down', count: 1, state: 'lit', step })
  }

  const layoutKey = `${focus}|${[...lens.open].sort().join(',')}|${model.programs.length}|${model.ixs.size}|${callers.length}|${targets.length}|${lens.acct ?? ''}`
  const { ref, ports, size } = usePorts(layoutKey)
  const audited = useMemo(() => new Set(model.programs.map((p) => p.pid)), [model.programs])
  const route = useMemo(() => systemRoute(ports, anchor, audited), [ports, anchor, audited])
  const entered = model.edges.filter((e) => e.from === SIGNERS).reduce((n, e) => n + e.count, 0)
  const countOf = (id: string) => model.edges.filter((e) => e.from === id || e.to === id).reduce((n, e) => n + e.count, 0)
  // A node picks every wire that touches it, as one filter.
  const pickNode = (id: string) => {
    if (lens.edge?.id === id) return act.pickEdge(null)
    const touching = [...bundles.values()].filter((b) => b.from === id || b.to === id)
    const members = touching.flatMap((b) => b.members)
    act.pickEdge({
      id,
      label: endLabel(model, id),
      count: touching.reduce((n, b) => n + b.count, 0),
      signed: touching.some((b) => b.signed),
      sigs: [...new Set(members.flatMap((m) => m.sigs))],
      ops: [...new Set(members.flatMap((m) => (m.facts.ops ?? '').split(', ').filter(Boolean)))],
      sites: [],
      tip: calleeTip(model.byId.get(id.replace(/^in:/, ''))?.external ? model.byId.get(id.replace(/^in:/, ''))?.facts : undefined),
    })
  }
  const pick = (id: string) => {
    const b = bundles.get(id)
    if (!b || lens.edge?.id === id) return act.pickEdge(null)
    const target = model.byId.get(b.to)
    const e: EdgePick = {
      id,
      label: `${endLabel(model, b.from)} → ${endLabel(model, b.to)}`,
      count: b.count,
      signed: b.signed,
      sigs: [...new Set(b.members.flatMap((m) => m.sigs))],
      ops: [...new Set(b.members.flatMap((m) => (m.facts.ops ?? '').split(', ').filter(Boolean)))],
      sites: b.members.filter((m) => m.facts.file).slice(0, 3).map((m) => `${m.facts.file.split('/').slice(-3).join('/')}:${m.facts.line}`),
      tip: calleeTip(target?.external ? target.facts : undefined),
    }
    act.pickEdge(e)
  }

  return (
    <div className="overflow-x-auto rounded-md border border-border bg-card">
        <div ref={ref} className="relative grid min-w-[728px] grid-cols-[148px_minmax(44px,1fr)_minmax(280px,320px)_minmax(60px,1.2fr)_164px] px-3.5 pt-3 pb-4">
          <div className="flex flex-col gap-3 pt-7">
            <span className="label-caps">enters</span>
            <SignersNode count={entered} word={model.words.signers} dim={Boolean(lens.acct)} on={lens.edge?.id === SIGNERS} onClick={() => pickNode(SIGNERS)} />
            {callers.map((c) => (
              <div key={c.id} className="relative">
                <Anchor id={`n:${c.id}`} className="top-0 left-1/2" />
                <OutsideNode o={c} port={`o:${c.id}`} side="left" count={countOf(c.id)} dim={Boolean(lens.acct)} on={lens.edge?.id === c.id} onClick={() => pickNode(c.id)} word={model.words.program} />
              </div>
            ))}
            {foldedCallers.length > 0 && (
              <div className="relative">
                <Anchor id={`n:${MORE_IN}`} className="top-0 left-1/2" />
                <FoldNode n={foldedCallers.length} count={foldedCallers.reduce((n, c) => n + countOf(c.id), 0)} port={`o:${MORE_IN}`} side="left" dim={Boolean(lens.acct)} onClick={() => setShowIn(true)}  word={model.words.programs} />
              </div>
            )}
            {showIn && model.callers.length > foldAt && <button type="button" onClick={() => setShowIn(false)} className="self-start font-mono text-[10.5px] text-muted-foreground hover:text-foreground">fold to {foldAt}</button>}
          </div>
          <div />
          <div className="flex flex-col gap-5">
            {model.programs.map((p) => <Box key={p.id} p={p} model={model} lens={lens} act={act} folded={Boolean(focus) && p.pid !== focus} totals={live?.totals} />)}
          </div>
          <div />
          <div className="flex flex-col gap-3 pt-7">
            <span className="label-caps">reached</span>
            {targets.map((t) => <OutsideNode key={t.id} o={t} port={`i:${t.id}`} side="right" count={countOf(t.id)} dim={Boolean(lens.acct)} on={lens.edge?.id === t.id} onClick={() => pickNode(t.id)} word={model.words.program} />)}
            {foldedTargets.length > 0 && <FoldNode n={foldedTargets.length} count={foldedTargets.reduce((n, t) => n + countOf(t.id), 0)} port={`i:${MORE_OUT}`} side="right" dim={Boolean(lens.acct)} onClick={() => setShowOut(true)}  word={model.words.programs} />}
            {showOut && model.targets.length > foldAt && <button type="button" onClick={() => setShowOut(false)} className="self-end font-mono text-[10.5px] text-muted-foreground hover:text-foreground">fold to {foldAt}</button>}
          </div>
          <Wires size={size} ports={ports} wires={wires} onPick={pick} />
          {live && <LivePulses size={size} live={live} route={route} enabled={!reducedMotion()} />}
        </div>
        <Legend evm={model.words.evm} web={model.words.web} />
    </div>
  )
}

// One audited program: its identity and life in the header, then a row per
// instruction or folded group.
function Box({ p, model, lens, act, folded, totals }: { p: ProgramBox; model: Model; lens: Lens; act: Act; folded: boolean; totals?: Map<string, Total> }) {
  const x = extraOf(p.info)
  const f = p.node?.facts ?? {}
  const immutable = x.immutable ?? f.immutable === 'true'
  const upgrades = x.upgrades ?? f.upgrades
  const firstDeploy = x.first_deploy_at ?? f.first_deploy
  const lastUpgrade = x.last_upgrade_at ?? f.last_upgrade
  const loader = x.loader ?? f.loader
  const changed = x.bytecode_changed === true
  const info = p.info
  const focused = lens.level !== 'system' && lens.prog === p.pid
  const allOpen = lens.level === 'program' && focused
  const web = model.words.web
  const withFindings = p.ixs.filter((x) => (x.node.findings ?? []).length > 0).length
  const seen = p.ixs.filter((x) => x.txs.length > 0).length
  return (
    <div className={cn('rounded-md border bg-background', focused ? 'border-primary/60' : 'border-border')}>
      <div className={cn('relative flex flex-col gap-1.5 px-3 py-2.5', !folded && 'border-b border-border')}>
        <Anchor id={`o:${p.id}`} className="top-1/2 right-0" />
        <Anchor id={`i:${p.id}`} className="top-1/2 left-0" />
        {folded && (
          <>
            <span className="absolute top-1/2 -left-[4px] -translate-y-1/2"><Port id={`i:h:${p.pid}`} tone="fg" /></span>
            <span className="absolute top-1/2 -right-[4px] -translate-y-1/2"><Port id={`o:h:${p.pid}`} tone="fg" /></span>
          </>
        )}
        <div className="flex items-center justify-between gap-2">
          <button type="button" onClick={() => act.program(p.pid)} className="min-w-0 truncate text-left font-mono text-[13px] hover:text-primary">{programName(model, p.pid)}</button>
          {web ? (
            <span className={cn('rounded-sm border border-border px-1 text-[9.5px] uppercase tracking-wide', p.node?.source === 'static' ? 'text-primary' : p.node?.source === 'operator' ? 'text-ok' : 'text-faint')}>{p.node?.source ?? 'static'}</span>
          ) : (
            <Copy value={p.pid} className="text-[11px]">{short(p.pid, 4, 4)}</Copy>
          )}
        </div>
        {web ? (
          // A host has no deployment to show: what it exposes and how much
          // of that was reached stand in the header.
          <div className="flex flex-wrap items-center gap-1">
            <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">{p.ixs.length} {p.ixs.length === 1 ? 'route' : 'routes'}</StateBadge>
            <StateBadge state={seen > 0 ? 'neutral' : 'pending'} className="h-5 px-1.5 text-[10.5px]">{seen} observed</StateBadge>
            {withFindings > 0 && <StateBadge state="bad" className="h-5 px-1.5 text-[10.5px]">{withFindings} with findings</StateBadge>}
          </div>
        ) : (
        <div className="flex flex-wrap items-center gap-1">
          <StateBadge state={matchState[info.match]} className="h-5 px-1.5 text-[10.5px]" tip={info.bytecode_sha256 ? `sha256 ${info.bytecode_sha256}` : undefined}>
            {matchWord[info.match]}{info.verified_commit ? ` · ${short(info.verified_commit, 7, 0)}` : ''}
          </StateBadge>
          <StateBadge state={info.idl_present ? 'neutral' : 'pending'} className="h-5 px-1.5 text-[10.5px]">{interfaceWord(info.idl_present, info.anchor_version, loader)}</StateBadge>
          {f.match_quality && (
            <StateBadge state={f.match_quality === 'exact' ? 'ok' : 'warn'} className="h-5 px-1.5 text-[10.5px]" tip="how the verified build matches the deployed bytecode">{f.match_quality} match</StateBadge>
          )}
          {f.compiler && (
            <span className="rounded-sm border border-border px-1 font-mono text-[9.5px] text-faint" title={f.evm_version ? `evm ${f.evm_version}` : undefined}>solc {f.compiler}{f.via_ir === 'true' ? ' · ir' : ''}</span>
          )}
          {f.selectors_in_bytecode && (
            <StateBadge state={f.selectors_in_bytecode.split('/')[0] === f.selectors_in_bytecode.split('/')[1] ? 'ok' : f.selectors_in_bytecode.startsWith('0/') ? 'bad' : 'warn'} className="h-5 px-1.5 text-[10.5px]" tip="source selectors the bytecode dispatches">
              {f.selectors_in_bytecode} in bytecode
            </StateBadge>
          )}
          {immutable ? (
            <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">immutable</StateBadge>
          ) : (
            <>
              {info.upgrade_authority && <StateBadge state={info.ownership_proven ? 'ok' : 'neutral'} className="h-5 px-1.5 text-[10.5px]" tip={info.upgrade_authority}>authority {short(info.upgrade_authority, 4, 4)}</StateBadge>}
              {(upgrades || changed) && (
                <StateBadge state={changed ? 'bad' : 'warn'} className="h-5 px-1.5 text-[10.5px]" tip={lastUpgrade ? `last upgrade ${relTime(lastUpgrade)}` : undefined}>
                  {upgrades ? `${upgrades} upgrade${upgrades === '1' ? '' : 's'}` : ''}{upgrades && changed ? ' · ' : ''}{changed ? 'changed since intake' : ''}
                </StateBadge>
              )}
            </>
          )}
          {loader && <span className="rounded-sm border border-border px-1 text-[9.5px] uppercase tracking-wide text-faint">{loader}</span>}
        </div>
        )}
        {f.holdings && (
          <div className="flex flex-wrap items-center gap-1 font-mono text-[10.5px]">
            <span className="label-caps mr-1">holds</span>
            {f.holdings.split(' · ').map((h) => (
              <span key={h} className="rounded-sm border border-border bg-background px-1.5 py-px text-muted-foreground">{h}</span>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 font-mono text-[10.5px] text-faint">
          {typeof info.last_deploy_slot === 'number' && info.last_deploy_slot > 0 && <span className="num">slot {info.last_deploy_slot.toLocaleString('en-US')}</span>}
          {firstDeploy && <span title={firstDeploy}>deployed {relTime(firstDeploy)}</span>}
          {lastUpgrade && !immutable && <span title={lastUpgrade}>upgraded {relTime(lastUpgrade)}</span>}
          {p.init && <span>{p.init.node.name} <span className={p.init.txs.length ? 'text-muted-foreground' : ''}>{p.init.txs.length ? `×${p.init.txs.length}` : '0 in sample'}</span></span>}
          <span className="ml-auto"><OutcomeBar stats={statsOf(p.node)} compact /></span>
        </div>
      </div>
      {!folded && (
      <div className="flex flex-col py-1">
        {p.groups
          ? p.groups.map((g) => {
              const gk = `${p.pid}:${g.key}`
              const open = allOpen || lens.open.has(gk)
              return (
                <div key={gk} className="flex flex-col">
                  <GroupRow gk={gk} label={g.label} ixs={g.ixs} open={open} lens={lens} act={act} />
                  {open && g.ixs.map((ix) => <IxRow key={ix.id} ix={ix} indent lens={lens} act={act} max={model.maxObserved} live={totals?.get(ix.id)} />)}
                </div>
              )
            })
          : p.ixs.map((ix) => <IxRow key={ix.id} ix={ix} lens={lens} act={act} max={model.maxObserved} live={totals?.get(ix.id)} />)}
      </div>
      )}
    </div>
  )
}

const rowGrid = 'grid w-full grid-cols-[minmax(0,1fr)_auto_22px_10px_26px] items-center gap-1.5'

// A route row shows the end of its path, where handlers differ; the
// whole path is its title and its header at route level.
function routeEnd(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length > 3 ? `…/${parts.slice(-3).join('/')}` : path
}

function IxRow({ ix, indent, lens, act, max, live }: { ix: Ix; indent?: boolean; lens: Lens; act: Act; max: number; live?: Total }) {
  const on = lens.level === 'instruction' && lens.sel === ix.id
  const f = ix.node.facts ?? {}
  const acct = lens.acct ? ix.accounts.find((a) => a.name === lens.acct) : undefined
  const signed = f.signed_cpi && f.signed_cpi !== '0'
  const calls = (f.cpi && f.cpi !== '0') || ix.node.source === 'traffic'
  const failing = Boolean(ix.stats && ix.stats.failed > ix.stats.ok)
  return (
    <div id={`n-${ix.id}`} className={cn('flex items-center', lens.acct && !acct && 'opacity-30')}>
      <span className="-ml-[3.5px]"><Port id={`i:${ix.id}`} tone={ix.txs.length ? 'fg' : 'muted'} hollow={!ix.txs.length} /></span>
      <button type="button" onClick={() => act.select(ix.id)} className={cn(rowGrid, 'rounded-sm px-2 py-[3px] text-left font-mono text-[12px]', indent && 'pl-5', on ? 'bg-accent' : 'hover:bg-accent/50')}>
        <span className="flex min-w-0 items-center gap-1.5">
          <span className={cn('truncate', ix.txs.length || on ? 'text-foreground' : 'text-muted-foreground')} title={f.path ? ix.node.name : undefined}>{f.path ? routeEnd(ix.node.name) : ix.node.name}</span>
          {f.methods && <span className="shrink-0 rounded-sm border border-border px-1 text-[9.5px] uppercase tracking-wide text-faint">{f.methods}</span>}
          {ix.node.source === 'traffic' && <span className="shrink-0 text-[10px] text-warn" title={`seen in traffic, not declared in source${f.named_via ? ` · named via ${f.named_via}` : ''}`}>*</span>}
          {(ix.node.findings ?? []).length > 0 && <span className="shrink-0 rounded-sm bg-bad-soft px-1 text-[10px] text-bad">#{String(ix.node.findings![0]).padStart(2, '0')}</span>}
          {f.diff === 'changed' && <span className="shrink-0 text-[10px] text-warn" title="changed since the base commit">Δ</span>}
          {f.diff === 'added' && <span className="shrink-0 text-[10px] text-ok" title="added since the base commit">+</span>}
        </span>
        {acct ? (
          <span className={cn('col-span-4 text-right text-[10.5px]', acct.facts?.raw ? 'text-warn' : acct.facts?.writable ? 'text-ok' : 'text-muted-foreground')}>{access(acct.facts)}</span>
        ) : (
          <>
            <span className="h-[3px] w-7 overflow-hidden rounded-full bg-muted" title={`${ix.txs.length} in sample`}>
              <span className="block h-full rounded-full bg-primary/70" style={{ width: `${Math.round((ix.txs.length / max) * 100)}%` }} />
            </span>
            <DepthMark depth={ix.depth} />
            <span>{f.raw && f.raw !== '0' ? <span className="block size-[7px] border border-warn" title={`${f.raw} raw account${f.raw === '1' ? '' : 's'}`} /> : null}</span>
            {live && live.calls > 0 ? (
              <span className={cn('num text-right text-[11px]', live.failed > live.calls / 2 ? 'text-bad/80' : 'text-primary')} title={`${live.calls.toLocaleString('en-US')} since the watch began${live.failed ? ` · ${live.failed} failed` : ''} · ${ix.txs.length} in sample`}>
                {live.calls >= 10_000 ? `${Math.round(live.calls / 1000)}k` : live.calls}
              </span>
            ) : (
              <span className={cn('num text-right text-[11px]', failing ? 'text-bad/80' : 'text-muted-foreground')}>{ix.txs.length || ''}</span>
            )}
          </>
        )}
      </button>
      <span className="-mr-[3.5px]"><Port id={`o:${ix.id}`} tone={signed ? 'warn' : calls ? 'fg' : 'muted'} hollow={!signed && !calls} /></span>
    </div>
  )
}

function GroupRow({ gk, label: name, ixs, open, lens, act }: { gk: string; label: string; ixs: Ix[]; open: boolean; lens: Lens; act: Act }) {
  const tx = ixs.reduce((n, x) => n + x.txs.length, 0)
  const signed = ixs.some((x) => x.node.facts?.signed_cpi && x.node.facts.signed_cpi !== '0')
  const raw = ixs.some((x) => x.node.facts?.raw && x.node.facts.raw !== '0')
  const touch = lens.acct ? ixs.some((x) => x.accounts.some((a) => a.name === lens.acct)) : true
  const depth: Ix['depth'] = [true, ixs.some((x) => x.depth[1]), ixs.some((x) => x.depth[2]), ixs.some((x) => x.depth[3])]
  return (
    <div className={cn('flex items-center', !touch && !open && 'opacity-30')}>
      <span className="-ml-[3.5px]">{open ? <span className="block size-[7px]" /> : <Port id={`i:g:${gk}`} tone={tx ? 'fg' : 'muted'} hollow={!tx} />}</span>
      <button type="button" aria-expanded={open} onClick={() => act.toggle(gk)} className={cn(rowGrid, 'rounded-sm bg-muted/60 px-2 py-[3px] text-left font-mono text-[12px] hover:bg-accent')}>
        <span className="flex min-w-0 items-center gap-1.5">
          {open ? <ChevronDown className="size-3 shrink-0 text-muted-foreground" /> : <ChevronRight className="size-3 shrink-0 text-muted-foreground" />}
          <span className="truncate">{name}</span>
          <span className="num text-[10.5px] text-faint">×{ixs.length}</span>
        </span>
        <span />
        {open ? <span /> : <DepthMark depth={depth} />}
        <span>{!open && raw ? <span className="block size-[7px] border border-warn" /> : null}</span>
        <span className="num text-right text-[11px] text-muted-foreground">{tx || ''}</span>
      </button>
      <span className="-mr-[3.5px]">{open ? <span className="block size-[7px]" /> : <Port id={`o:g:${gk}`} tone={signed ? 'warn' : 'fg'} />}</span>
    </div>
  )
}

// The programs a side does not show, as one node: how many and how much.
function FoldNode({ n, count, port, side, dim, onClick, word = 'programs' }: { n: number; count: number; port: string; side: 'left' | 'right'; dim?: boolean; onClick: () => void; word?: string }) {
  return (
    <div className={cn('flex items-center', side === 'left' ? 'flex-row' : 'flex-row-reverse', dim && 'opacity-35')}>
      <button type="button" onClick={onClick} title={`show every ${side === 'left' ? 'caller' : 'callee'}`} className="flex min-w-0 flex-1 items-center justify-between gap-2 rounded-md border border-dashed border-border px-2.5 py-1.5 text-left font-mono text-[12px] text-muted-foreground hover:border-muted-foreground hover:text-foreground">
        <span>+{n} {side === 'left' ? 'callers' : word}</span>
        <span className="num shrink-0 text-[11px] text-faint">×{count}</span>
      </button>
      <span className={side === 'left' ? '-mr-[3.5px]' : '-ml-[3.5px]'}><Port id={port} tone={count ? 'fg' : 'muted'} hollow={!count} /></span>
    </div>
  )
}

function Legend({ evm, web }: { evm: boolean; web: boolean }) {
  const line = (cls: string, dash?: boolean) => (
    <svg width="26" height="6" aria-hidden><line x1="0" y1="3" x2="26" y2="3" strokeWidth="2" strokeDasharray={dash ? '4 4' : undefined} className={cls} /></svg>
  )
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border px-4 py-2 font-mono text-[10.5px] text-muted-foreground">
      <span className="inline-flex items-center gap-1.5">{line('stroke-muted-foreground')}seen · width is traffic</span>
      {!web && <span className="inline-flex items-center gap-1.5">{line('stroke-muted-foreground', true)}source only</span>}
      {!evm && !web && <span className="inline-flex items-center gap-1.5 text-warn">{line('stroke-warn')}signed</span>}
      <span className="inline-flex items-center gap-1.5"><DepthMark depth={[true, true, false, false]} />{web ? 'named' : 'declared'} · observed · replayed · reviewed</span>
      {!evm && !web && <span className="inline-flex items-center gap-1.5"><span className="block size-[7px] border border-warn" />raw accounts</span>}
      {!web && <span className="inline-flex items-center gap-1.5"><span className="text-warn">*</span>seen in traffic, not in source</span>}
      {!web && <span>⇄ calls in and is called</span>}
      {!web && <span className="inline-flex items-center gap-1.5"><span className="text-warn">Δ</span>changed<span className="text-ok">+</span>added</span>}
    </div>
  )
}
