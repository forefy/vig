import { Link } from 'react-router'
import { StateBadge } from '@/components/primitives'
import { cn, relTime, short } from '@/lib/utils'
import { OutcomeBar } from './OutcomeBar'
import { UnitsRange } from './UnitsRange'
import { VolumeStrip } from './Moves'
import { access, changeWord, checks, keyFor, replayAccount, SIGNERS, isSide } from './model'
import type { Ix, MapEdge, Model, Words } from './model'
import { Anchor, DepthMark, OutsideNode, Port, Wires, baselineReplay, endLabel, ixLabel, sampleEmpty } from './parts'
import type { Act, EdgePick, Lens, Wire } from './parts'
import { usePorts } from './usePorts'
import { useMemo, useState } from 'react'
import { LivePulses, instructionRoute } from './LivePulses'
import type { LiveMap } from './SystemMap'
import { reducedMotion } from '@/app/a11y'

const attentionState = { unseen: 'pending', observed: 'neutral', reviewed: 'ok', finding: 'bad' } as const

// foldAt is how many callers show before the rest fold into one node.
const foldAt = 10

const staticKeys: [string, string, 'warn' | 'bad' | 'neutral'][] = [
  ['reach', 'fns', 'neutral'],
  ['raw', 'raw', 'warn'],
  ['cpi', 'cpi', 'neutral'],
  ['signed_cpi', 'signed', 'warn'],
  ['pda', 'pda', 'neutral'],
  ['token', 'token', 'neutral'],
  ['lamports', 'lamports', 'warn'],
  ['unsafe', 'unsafe', 'bad'],
  ['realloc', 'realloc', 'warn'],
  ['serde', 'serde', 'neutral'],
]

// One instruction in the order the runtime executes it: who enters, the
// accounts it loads and how each is validated, the handler, the calls out,
// and what was committed. Calls run left to right; state sits underneath.
export function InstructionView({ ix, model, lens, act, reportHref, live, sampling }: { ix: Ix; model: Model; lens: Lens; act: Act; reportHref?: string; live?: LiveMap; sampling?: { horizon?: string; blocks?: number } | null }) {
  const tx = lens.tx ? model.txs.get(lens.tx) : undefined
  const mine = tx?.rows.find((r) => r.ix.id === ix.id)
  const passes = Boolean(tx?.hops.some(([a, b]) => a === ix.id || b === ix.id))
  const replay = mine && tx ? baselineReplay(ix.replays, tx.sig) : undefined

  // Callers fold to the busiest few so the handler stays on one screen;
  // a lit or picked caller is always shown, the rest gather in one node
  // that expands in place.
  const allIn = useMemo(() => model.edges.filter((e) => e.to === ix.id).sort((a, b) => b.count - a.count), [model.edges, ix.id])
  const [showAll, setShowAll] = useState(false)
  const pickedEdge = lens.edge?.id ?? ''
  const keep = (e: MapEdge, i: number) => showAll || i < foldAt || pickedEdge === e.id || Boolean(tx && tx.hops.some(([a, b]) => a === e.from && b === ix.id))
  const inbound = allIn.filter(keep)
  const folded = allIn.filter((e, i) => !keep(e, i))
  const foldedCount = folded.reduce((n, e) => n + e.count, 0)
  const outbound = model.edges.filter((e) => e.from === ix.id)
  const f = ix.node.facts ?? {}
  const unresolved = f.unresolved && f.unresolved !== '0' ? f.unresolved : ''

  const litIn = new Set((tx?.hops ?? []).filter(([, b]) => b === ix.id).map(([a]) => a))
  const litOut = new Set((tx?.hops ?? []).filter(([a]) => a === ix.id).map(([, b]) => b))
  const order = new Map((tx?.hops ?? []).map(([a, b], i) => [`${a}>${b}`, i + 1]))

  const pickWire = (e: MapEdge) => {
    if (lens.edge?.id === e.id) return act.pickEdge(null)
    const p: EdgePick = { id: e.id, label: `${endLabel(model, e.from)} → ${endLabel(model, e.to)}`, count: e.count, signed: e.signed, sigs: e.sigs, ops: (e.facts.ops ?? '').split(', ').filter(Boolean), sites: e.facts.file ? [`${e.facts.file.split('/').slice(-3).join('/')}:${e.facts.line}`] : [], tip: undefined }
    act.pickEdge(p)
  }
  const words = model.words
  // The stages in the target's words, numbered as execution order on a
  // chain; a route has no such order.
  const stage = (i: number) => (words.web ? words.stages[i] : `${i + 1} ${words.stages[i]}`)
  const layoutKey = `${ix.id}|${inbound.length}|${folded.length}|${outbound.length}|${ix.accounts.length}|${mine ? 1 : 0}|${replay?.key ?? ''}`
  const { ref, ports, size } = usePorts(layoutKey)
  const route = useMemo(() => instructionRoute(ports, ix, model, allIn), [ports, ix, model, allIn])

  const wires: Wire[] = []
  const wireState = (e: MapEdge, lit: boolean): Wire['state'] => (lens.edge?.id === e.id ? 'picked' : tx ? (lit ? 'lit' : 'dim') : 'on')
  for (const e of inbound) wires.push({ id: e.id, from: `o:${e.from}`, to: 'i:comp', kind: 'across', count: e.count, signed: e.signed, dashed: e.count === 0, state: wireState(e, passes && litIn.has(e.from)), step: passes && litIn.has(e.from) ? order.get(e.id) : undefined })
  if (folded.length > 0) wires.push({ id: 'more:in', from: 'o:more:in', to: 'i:comp', kind: 'across', count: foldedCount, signed: folded.some((e) => e.signed), dashed: foldedCount === 0, state: lens.edge ? 'dim' : tx ? 'dim' : 'on', title: `${folded.length} more callers` })
  for (const e of outbound) wires.push({ id: e.id, from: 'o:comp', to: `i:${e.to}`, kind: 'across', count: e.count, signed: e.signed, dashed: e.count === 0, state: wireState(e, passes && litOut.has(e.to)), step: passes && litOut.has(e.to) ? order.get(e.id) : undefined })
  if (unresolved) wires.push({ id: 'fog', from: 'o:comp', to: 'i:fog', kind: 'across', count: 0, dashed: true, state: tx ? 'dim' : 'idle' })
  const bl = ports['bl:comp'], br = ports['br:comp']
  for (const a of ix.accounts) {
    const top = ports[`n:${a.id}`]
    if (!bl || !br || !top) continue
    const x = Math.max(bl.x + 18, Math.min(br.x - 18, top.x))
    wires.push({ id: `w:${a.id}`, from: '', to: `n:${a.id}`, fromPt: { x, y: bl.y }, kind: 'down', count: a.facts?.writable ? 3 : 0, dashed: Boolean(a.facts?.optional), state: lens.acct === a.name ? 'lit' : a.facts?.writable ? 'on' : 'idle' })
  }

  return (
      <div className="overflow-x-auto rounded-md border border-border bg-card">
        <div ref={ref} className="relative min-w-[728px] px-3.5 pt-3 pb-4">
          <div className="grid grid-cols-[148px_minmax(32px,1fr)_minmax(320px,400px)_minmax(32px,1fr)_180px] items-center">
            <div className="flex flex-col gap-2.5 self-center">
              <span className="label-caps">{stage(0)}</span>
              {inbound.length === 0 && <div className="rounded-md border border-dashed border-border px-2.5 py-1.5 font-mono text-[12px] text-muted-foreground">{sampleEmpty(sampling, words.web)}</div>}
              {inbound.map((e) => <EndNode key={e.id} e={e} end={e.from} side="left" model={model} act={act} on={lens.edge?.id === e.id} onPick={() => pickWire(e)} dim={Boolean(tx) && !(passes && litIn.has(e.from))} />)}
              {folded.length > 0 && (
                <div className={cn('flex items-center', tx && 'opacity-35')}>
                  <button type="button" onClick={() => setShowAll(true)} className="flex min-w-0 flex-1 items-center justify-between gap-2 rounded-md border border-dashed border-border px-2.5 py-1.5 text-left font-mono text-[12px] text-muted-foreground hover:border-muted-foreground hover:text-foreground" title="show every caller">
                    <span>+{folded.length} callers</span>
                    <span className="num text-[11px] text-faint">×{foldedCount}</span>
                  </button>
                  <span className="-ml-[3.5px]"><Port id="o:more:in" tone={foldedCount ? 'fg' : 'muted'} hollow={!foldedCount} /></span>
                </div>
              )}
              {showAll && allIn.length > foldAt && (
                <button type="button" onClick={() => setShowAll(false)} className="self-start font-mono text-[10.5px] text-muted-foreground hover:text-foreground">fold to {foldAt}</button>
              )}
            </div>
            <div />
            <Component ix={ix} reportHref={reportHref} words={model.words} />
            <div />
            <div className="flex flex-col gap-2.5 self-center">
              <span className="label-caps">{stage(4)}</span>
              {outbound.length === 0 && !unresolved && <div className="rounded-md border border-dashed border-border px-2.5 py-1.5 font-mono text-[12px] text-muted-foreground">{words.web ? 'no nav out' : 'no calls out'}</div>}
              {outbound.map((e) => <EndNode key={e.id} e={e} end={e.to} side="right" model={model} act={act} on={lens.edge?.id === e.id} onPick={() => pickWire(e)} dim={Boolean(tx) && !(passes && litOut.has(e.to))} />)}
              {unresolved && (
                <div className={cn('flex flex-row-reverse items-center', tx && 'opacity-35')}>
                  <div className="flex min-w-0 flex-1 items-center justify-between gap-2 rounded-md border border-dashed border-input bg-[repeating-linear-gradient(45deg,transparent_0_4px,var(--border)_4px_5px)] px-2.5 py-1.5 font-mono text-[12px] text-muted-foreground">
                    <span>unresolved calls</span><span className="num text-[11px]">×{unresolved}</span>
                  </div>
                  <span className="-ml-[3.5px]"><Port id="i:fog" hollow /></span>
                </div>
              )}
            </div>
          </div>

          {ix.accounts.length > 0 && (
            <div className="mt-14 grid grid-cols-[84px_minmax(0,1fr)] gap-3">
              <div className="flex flex-col font-mono text-[9.5px] tracking-[0.08em] text-muted-foreground uppercase">
                <span className="flex h-[26px] items-center">{stage(1)}</span>
                <span className="flex h-5 items-center" />
                <span className="flex h-5 items-center">{stage(2)}</span>
                {mine && !words.web && <span className="flex h-5 items-center">{stage(5)}</span>}
              </div>
              <div className="flex flex-wrap justify-center gap-2">
                {ix.accounts.map((a, n) => {
                  const af = a.facts ?? {}
                  const ck = checks(af)
                  const ch = mine && replay ? changeWord(replay, a.name, n) : undefined
                  const ra = replayAccount(replay, a.name, n)
                  const passed = mine ? keyFor(mine.s, n) : undefined
                  const signedBy = mine && passed && Array.isArray(mine.s.signer_keys) && (mine.s.signer_keys as string[]).includes(passed)
                  return (
                    <button
                      key={a.id}
                      type="button"
                      onClick={() => act.pickAcct(lens.acct === a.name ? null : a.name)}
                      title={[af.type, af.seeds ? `seeds: ${af.seeds}` : '', af.has_one ? `has_one: ${af.has_one}` : '', af.owner ? `owner: ${af.owner}` : '', af.address ? `address: ${af.address}` : '', af.line ? `line ${af.line}` : ''].filter(Boolean).join(' · ') || undefined}
                      className={cn(
                        'relative flex w-[150px] flex-col rounded-md border bg-background px-2.5 pb-1.5 text-left font-mono hover:border-muted-foreground',
                        af.raw ? 'border-dashed border-warn' : 'border-border',
                        lens.acct === a.name && 'border-solid border-primary',
                      )}
                    >
                      <Anchor id={`n:${a.id}`} className="top-0 left-1/2" />
                      <span className="flex h-[26px] items-center justify-between gap-1.5 text-[12px]">
                        <span className="truncate">{a.name}</span>
                        {passed && <span className={cn('num shrink-0 text-[10px]', signedBy ? 'text-warn' : 'text-muted-foreground')} title={passed}>{short(passed, 4, 4)}</span>}
                      </span>
                      <span className="flex h-5 items-center gap-1.5 truncate text-[10px] text-muted-foreground">
                        {words.web ? (af.in === 'body' ? 'request body' : af.in === 'path' ? 'in the path' : 'query string') : `${access(af).replace(' · raw', '')}${af.optional ? ' · optional' : ''}${af.realloc ? ' · realloc' : ''}${af.zero_copy ? ' · zero-copy' : ''}`}
                      </span>
                      <span className={cn('flex h-5 items-center truncate text-[10px]', af.raw ? 'text-warn' : 'text-ok')}>
                        {words.web ? <span className="text-faint">·</span> : af.raw ? 'unchecked' : ck.length ? ck.map((c) => `✓ ${c}`).join(' ') : <span className="text-faint">·</span>}
                      </span>
                      {mine && !words.web && (
                        <span className={cn('flex h-5 items-center gap-1.5 truncate text-[10px]', !ch ? 'text-faint' : ch.tone === 'ok' ? 'text-ok' : ch.tone === 'bad' ? 'text-bad' : ch.tone === 'warn' ? 'text-warn' : 'text-faint')}>
                          {ch ? ch.text : 'not replayed'}
                          {ra?.pda === 'ok' && <span className="text-ok">pda ✓</span>}
                          {ra?.pda === 'mismatch' && <span className="text-bad">pda mismatch</span>}
                          {af.seeds && replay && !ra?.pda && <span className="text-faint">pda ?</span>}
                        </span>
                      )}
                    </button>
                  )
                })}
              </div>
            </div>
          )}
          <Wires size={size} ports={ports} wires={wires} onPick={(id) => { const e = [...inbound, ...outbound].find((x) => x.id === id); if (e) pickWire(e) }} />
          {live && <LivePulses size={size} live={live} route={route} enabled={!reducedMotion()} />}
        </div>
      </div>
  )
}

// A caller or a callee of this instruction: an outside program, the signer
// entry, or another audited instruction (which zooms to it).
// Clicking any end filters the log to the transactions that crossed that
// wire; an audited instruction also offers to go there.
function EndNode({ e, end, side, model, act, dim, on, onPick }: { e: MapEdge; end: string; side: 'left' | 'right'; model: Model; act: Act; dim: boolean; on: boolean; onPick: () => void }) {
  const port = side === 'left' ? `o:${end}` : `i:${end}`
  const other = model.ixs.get(end)
  if (other || end === SIGNERS || (end.startsWith('p:') && !isSide(end) && model.programs.some((p) => p.id === end))) {
    const name = end === SIGNERS ? model.words.signers : other ? ixLabel(model, end) : endLabel(model, end)
    return (
      <div className={cn('flex items-center', side === 'left' ? 'flex-row' : 'flex-row-reverse', dim && 'opacity-35')}>
        <button type="button" aria-pressed={on} onClick={onPick} className={cn('flex min-w-0 flex-1 items-center justify-between gap-2 rounded-md border px-2.5 py-1.5 text-left font-mono text-[12px] hover:border-muted-foreground', on ? 'border-primary' : e.count ? 'border-border bg-background' : 'border-dashed border-border text-muted-foreground')}>
          <span className="truncate">{name}</span>
          {other && <span onClick={(ev) => { ev.stopPropagation(); act.select(other.id) }} className="shrink-0 text-[10.5px] text-muted-foreground hover:text-primary" title="open">→</span>}
          <span className="num shrink-0 text-[11px]">
            {e.signed && <span className="mr-1 text-warn">signed</span>}
            <span className="text-faint">{e.count ? `×${e.count}` : 'source'}</span>
          </span>
        </button>
        <span className={side === 'left' ? '-mr-[3.5px]' : '-ml-[3.5px]'}><Port id={port} tone={e.signed ? 'warn' : e.count ? 'fg' : 'muted'} hollow={!e.count && !e.signed} /></span>
      </div>
    )
  }
  const o = [...model.callers, ...model.targets].find((x) => x.id === end)
  if (!o) return null
  return (
    <div className="flex flex-col gap-0.5">
      <OutsideNode o={o} port={port} side={side} count={e.count} dim={dim} on={on} onClick={onPick} word={model.words.program} />
      {(e.signed || e.facts.ops) && (
        <span className={cn('px-1 font-mono text-[10px]', side === 'right' ? 'pl-3' : '')}>
          {e.signed && <span className="mr-1.5 text-warn">signed</span>}
          {e.facts.ops && <span className="text-muted-foreground">{e.facts.ops}</span>}
        </span>
      )}
    </div>
  )
}

// The handler: identity, how well it is known, what the source says its
// reachable code does, and how its traffic fared.
function Component({ ix, reportHref, words }: { ix: Ix; reportHref?: string; words: Words }) {
  const f = ix.node.facts ?? {}
  const st = ix.stats
  const items = staticKeys.filter(([k]) => f[k] && f[k] !== '0')
  return (
    <div className="relative flex flex-col gap-2.5 rounded-md border border-input bg-background px-4 py-3.5">
      <Anchor id="i:comp" className="top-1/2 left-0" />
      <Anchor id="o:comp" className="top-1/2 right-0" />
      <Anchor id="bl:comp" className="bottom-0 left-0" />
      <Anchor id="br:comp" className="right-0 bottom-0" />
      <div className="flex flex-wrap items-center gap-2">
        <span className="label-caps">{words.web ? words.stages[3] : `4 ${words.stages[3]}`}</span>
        <span className="font-mono text-[15px]">{ix.node.name}</span>
        {f.methods && <span className="rounded-sm border border-border px-1 text-[9.5px] uppercase tracking-wide text-faint">{f.methods}</span>}
        <DepthMark depth={ix.depth} />
        <StateBadge state={attentionState[ix.node.attention]} className="h-5 px-1.5 text-[10.5px]">{ix.node.attention}</StateBadge>
        {(ix.node.findings ?? []).map((o) =>
          reportHref ? (
            <Link key={o} to={`${reportHref}#f-${o}`} className="rounded-sm bg-bad-soft px-1.5 font-mono text-[11px] text-bad hover:underline">#{String(o).padStart(2, '0')}</Link>
          ) : (
            <span key={o} className="rounded-sm bg-bad-soft px-1.5 font-mono text-[11px] text-bad">#{String(o).padStart(2, '0')}</span>
          ),
        )}
        <span className={cn('rounded-sm border border-border px-1 text-[9.5px] uppercase tracking-wide', ix.node.source === 'traffic' ? 'text-warn' : ix.node.source === 'operator' ? 'text-ok' : ix.node.source === 'static' ? 'text-primary' : 'text-faint')}>{ix.node.source}</span>
        {f.diff === 'changed' && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">changed</StateBadge>}
        {f.diff === 'added' && <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">added</StateBadge>}
      </div>
      {f.file && <span className="font-mono text-[11px] text-muted-foreground">{f.file}{f.line ? `:${f.line}` : ''}</span>}
      {f.host && !f.file && <span className="font-mono text-[11px] text-muted-foreground">{f.host}</span>}
      {f.named_via && <span className="font-mono text-[11px] text-muted-foreground">named via <span className="text-foreground">{f.named_via}</span></span>}
      {f.args && <span className="font-mono text-[11px] break-words text-muted-foreground">{f.args}</span>}
      {(items.length > 0 || f.remaining) && (
        <div className="flex flex-wrap items-center gap-1.5">
          {items.map(([k, label, tone]) => (
            <StateBadge key={k} state={tone} className="h-5 px-1.5 text-[10.5px]"><span className="num">{f[k]}</span> {label}</StateBadge>
          ))}
          {f.remaining && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">remaining accounts</StateBadge>}
        </div>
      )}
      {st && (st.ok + st.failed > 0 || (st.moves ?? []).length > 0) && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-border pt-2.5">
          <OutcomeBar stats={st} className="items-start" />
          <UnitsRange stats={st} unit={words.units} />
          {st.signers > 0 && <span className="font-mono text-[11px] text-muted-foreground">{st.signers} {st.signers === 1 ? words.signers.replace(/s$/, '') : words.signers}</span>}
          {st.first_seen && <span className="font-mono text-[11px] text-faint" title={st.first_seen}>first {relTime(st.first_seen)}</span>}
          <VolumeStrip moves={st.moves} />
        </div>
      )}
    </div>
  )
}
