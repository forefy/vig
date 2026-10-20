import { useState } from 'react'
import { ExternalLink } from 'lucide-react'
import { StateBadge, Copy } from '@/components/primitives'
import { cn, relTime, short } from '@/lib/utils'
import { Moves } from './Moves'
import { Args, ReplayDetail, argTypes } from './ReplayDetail'
import { access, keysOf, keysSeen, nowOf, thenOf } from './model'
import type { DriftRow, Ix, Model, Outcome, Tx, TxRow } from './model'
import { OutcomeMark, baselineReplay, endLabel, ixLabel, programName, sampleEmpty } from './parts'
import type { Act, Lens } from './parts'
import { time } from './types'
import type { Surface, TokenMove } from './types'

type Sampling = NonNullable<Surface['sampling']>
const STAGES = ['enters', 'loads', 'validates', 'runs', 'calls', 'commits'] as const

// The evidence for wherever the map stands: the sampled transactions of
// the system, of one program or of one instruction, and the accounts in
// that scope. A picked wire or account filters it. A transaction opens in
// place, and is the one the map lights.
export function Dock({ model, lens, act, sampling, at, loading }: { model: Model; lens: Lens; act: Act; sampling?: Sampling; at?: number | null; loading?: boolean }) {
  // Only the transactions the watch read in full carry inputs and
  // accounts; on a busy program they are a few in hundreds, so the log
  // can be narrowed to them.
  const [fullOnly, setFullOnly] = useState(false)
  const scope: Ix[] = lens.level === 'instruction' ? [model.ixs.get(lens.sel)].filter((x): x is Ix => Boolean(x)) : lens.level === 'program' ? (model.programs.find((p) => p.pid === lens.prog)?.ixs ?? []) : [...model.ixs.values()]
  const inScope = new Set(scope.map((x) => x.id))
  const where = lens.level === 'instruction' ? (scope[0]?.node.name ?? '') : lens.level === 'program' ? programName(model, lens.prog) : `${model.programs.length} ${model.programs.length === 1 ? model.words.program : model.words.programs}`

  const txs = [...model.txs.values()]
    .filter((t) => t.rows.some((r) => inScope.has(r.ix.id)))
    .filter((t) => !lens.edge || lens.edge.sigs.includes(t.sig))
    .filter((t) => !lens.acct || t.rows.some((r) => inScope.has(r.ix.id) && r.ix.accounts.some((a) => a.name === lens.acct)))
    .filter((t) => at === null || at === undefined || new Date(t.at ?? 0).getTime() <= at)
    .sort((a, b) => new Date(b.at ?? 0).getTime() - new Date(a.at ?? 0).getTime())
  const full = txs.filter((t) => t.rows.some((r) => inScope.has(r.ix.id) && keysOf(r.s).length > 0))
  const shown = fullOnly ? full : txs

  const names = [...model.accounts.entries()]
    .map(([name, uses]) => ({ name, uses: uses.filter((u) => inScope.has(u.ix.id)) }))
    .filter((x) => x.uses.length > 0)
    .sort((a, b) => b.uses.length - a.uses.length || a.name.localeCompare(b.name))
  const web = model.words.web

  return (
    <div className="flex max-h-[680px] flex-col overflow-hidden rounded-md border border-border bg-card @[1100px]:sticky @[1100px]:top-4 @[1100px]:max-h-[calc(100dvh/var(--text-scale,1)-2rem)]">
      <div className="flex flex-col gap-2 border-b border-border px-3.5 pt-3 pb-2">
        <div className="flex items-baseline justify-between gap-2">
          <span className="label-caps">{at ? 'what had happened in' : 'what happened in'}</span>
          <span className="truncate font-mono text-[13px]">{where}{loading ? <span className="ml-2 text-faint">…</span> : null}</span>
        </div>
        {(lens.edge || lens.acct) && (
          <div className="flex flex-wrap gap-1.5">
            {lens.edge && (
              <Filter kind="line" onClear={() => act.pickEdge(null)}>
                {lens.edge.label}
                {lens.edge.signed && <span className="text-warn">signed</span>}
                {lens.edge.ops.map((o) => <span key={o} className="text-muted-foreground">{o}</span>)}
                <span className="text-faint">{lens.edge.count ? `×${lens.edge.count}` : '0 in sample'}</span>
              </Filter>
            )}
            {lens.acct && <Filter kind="account" onClear={() => act.pickAcct(null)}>{lens.acct}</Filter>}
          </div>
        )}
        {lens.edge && (lens.edge.sites.length > 0 || lens.edge.tip) && (
          <div className="flex flex-wrap gap-x-2.5 font-mono text-[10.5px] text-faint">
            {lens.edge.sites.map((s) => <span key={s}>{s}</span>)}
            {lens.edge.tip && <span>{lens.edge.tip}</span>}
          </div>
        )}
        <div className="flex items-end gap-4 font-mono text-[11px]">
          {(['tx', 'acct'] as const).map((t) => (
            <button key={t} type="button" onClick={() => act.tab(t)} className={cn('-mb-2 border-b-2 pb-1.5', lens.tab === t ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground')}>
              {t === 'tx' ? model.words.txs : model.words.accounts} <span className="num text-faint">{t === 'tx' ? shown.length : names.length}</span>
            </button>
          ))}
          {lens.tab === 'tx' && txs.some((t) => t.live) && (
            <button type="button" onClick={() => setFullOnly((v) => !v)} aria-pressed={fullOnly} title="only transactions read in full: inputs and accounts" className={cn('ml-auto -mb-2 rounded-sm border px-1.5 pb-px', fullOnly ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:text-foreground')}>
              with inputs <span className="num text-faint">{full.length}</span>
            </button>
          )}
        </div>
      </div>

      <div className="overflow-y-auto">
        {lens.tab === 'tx' ? (
          <>
            <Strip txs={txs.filter((t) => !t.live)} inScope={inScope} sampling={sampling} lens={lens} act={act} />
            {model.drift.filter((d) => at === null || at === undefined || time(d.at) <= at).slice(0, 6).map((d) => <DriftItem key={d.ev.ref ?? d.at} d={d} model={model} />)}
            {shown.length === 0 && <div className="px-3.5 py-3"><StateBadge state="pending" className="h-5 px-1.5 text-[10.5px]">{fullOnly ? 'none read in full' : sampleEmpty(sampling, web)}</StateBadge></div>}
            {shown.slice(0, 60).map((t) => <TxItem key={t.sig} tx={t} inScope={inScope} model={model} lens={lens} act={act} />)}
          </>
        ) : (
          names.map(({ name, uses }) => {
            const on = lens.acct === name
            const raw = uses.filter((u) => u.node.facts?.raw).length
            const writes = uses.filter((u) => u.node.facts?.writable).length
            // A parameter travels in the query or the body; the chips say
            // which, where an account's row says writes and raw reads.
            const ins = [...new Set(uses.map((u) => u.node.facts?.in).filter(Boolean))].join(' · ')
            return (
              <div key={name} className="border-t border-border first:border-t-0">
                <button type="button" onClick={() => act.pickAcct(on ? null : name)} className={cn('grid w-full grid-cols-[minmax(0,1fr)_auto_auto_auto] items-center gap-2.5 px-3.5 py-1.5 text-left font-mono text-[11.5px] hover:bg-accent/50', on && 'bg-accent shadow-[inset_2px_0_0_var(--primary)]')}>
                  <span className="truncate">{name}</span>
                  <span className="num text-faint">×{uses.length}</span>
                  {web ? (
                    <span className="col-span-2 text-right text-[10.5px] text-muted-foreground">{ins}</span>
                  ) : (
                    <>
                      <span className="num w-6 text-right text-ok">{writes ? `${writes}w` : ''}</span>
                      <span className="w-10 text-right text-warn">{raw ? `raw ${raw}` : ''}</span>
                    </>
                  )}
                </button>
                {on && (
                  <div className="flex flex-col gap-2.5 bg-muted/50 px-3.5 py-2.5">
                    <KeysSeen list={keysSeen(uses)} />
                  <div className="flex flex-wrap gap-1.5">
                    {uses.map((u) => (
                      <button key={u.node.id} type="button" onClick={() => act.select(u.ix.id)} title={u.node.facts?.type} className={cn('inline-flex items-center gap-1.5 rounded-sm border bg-card px-1.5 py-0.5 font-mono text-[11px] hover:border-muted-foreground', u.node.facts?.raw ? 'border-warn/60' : 'border-border')}>
                        {ixLabel(model, u.ix.id)}
                        <span className={u.node.facts?.raw ? 'text-warn' : u.node.facts?.writable ? 'text-ok' : 'text-faint'}>{access(u.node.facts)}</span>
                      </button>
                    ))}
                  </div>
                  </div>
                )}
              </div>
            )
          })
        )}
      </div>
    </div>
  )
}

// The accounts one call touched: every declared account with the address
// that stood in for it, and whether it was written or signed. A row the
// watch placed from logs alone carries no keys, and says so.
function Touched({ row }: { row: TxRow }) {
  const keys = keysOf(row.s)
  const signers = Array.isArray(row.s.signer_keys) ? (row.s.signer_keys as string[]) : []
  if (row.ix.accounts.length === 0) return null
  if (keys.length === 0)
    return (
      <div className="flex flex-col gap-1">
        <span className="label-caps">accounts</span>
        <StateBadge state="pending" className="h-5 w-fit px-1.5 text-[10.5px]">{row.s.source === 'logs' ? 'not read in full' : 'no keys'}</StateBadge>
      </div>
    )
  return (
    <div className="flex flex-col gap-1">
      <span className="label-caps">accounts <span className="num text-faint">{keys.length}</span></span>
      <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] gap-x-2 gap-y-0.5 font-mono text-[11px]">
        {row.ix.accounts.map((a, i) => {
          const k = keys[i]
          if (!k) return null
          const f = a.facts ?? {}
          const signed = signers.includes(k)
          return (
            <div key={a.id} className="contents">
              <span className="truncate text-muted-foreground" title={a.name}>{a.name}</span>
              <Copy value={k} className="text-[11px] text-foreground">{short(k, 4, 4)}</Copy>
              <span className={cn('text-[10px]', signed ? 'text-warn' : f.writable ? 'text-ok' : 'text-faint')}>{signed ? 'signer' : f.writable ? 'write' : 'read'}</span>
            </div>
          )
        })}
        {keys.length > row.ix.accounts.length && <span className="col-span-3 text-[10px] text-faint">+{keys.length - row.ix.accounts.length} remaining accounts</span>}
      </div>
    </div>
  )
}

// The addresses that stood in for one account across the sampled calls.
// One address every time is a fixed account; many is user-supplied.
function KeysSeen({ list }: { list: ReturnType<typeof keysSeen> }) {
  if (list.length === 0) return null
  const total = list.reduce((n, k) => n + k.count, 0)
  return (
    <div className="flex flex-col gap-1">
      <span className="label-caps">
        keys seen <span className="num text-faint">{list.length} distinct · {total} calls</span>
      </span>
      <div className="flex flex-wrap gap-1.5">
        {list.slice(0, 8).map((k) => (
          <span key={k.key} className="inline-flex items-center gap-1.5 rounded-sm border border-border bg-card px-1.5 py-0.5 font-mono text-[11px]">
            <Copy value={k.key} className="text-[11px] text-foreground">{short(k.key, 4, 4)}</Copy>
            {k.signer && <span className="text-warn">signer</span>}
            <span className="num text-faint">×{k.count}</span>
          </span>
        ))}
        {list.length > 8 && <span className="font-mono text-[11px] text-faint">+{list.length - 8}</span>}
      </div>
    </div>
  )
}

function Filter({ kind, onClear, children }: { kind: string; onClear: () => void; children: React.ReactNode }) {
  return (
    <span className="inline-flex min-h-6 max-w-full flex-wrap items-center gap-x-2 rounded-sm bg-accent py-0.5 pr-1 pl-2 font-mono text-[11px]">
      <span className="label-caps text-[9.5px]">{kind}</span>
      {children}
      <button type="button" onClick={onClear} aria-label={`Clear ${kind}`} className="flex size-[18px] items-center justify-center rounded-sm text-muted-foreground hover:bg-border hover:text-foreground">✕</button>
    </span>
  )
}

// The row that speaks for a transaction here: the open instruction's when
// it has one, else the first call in scope.
function focusRow(tx: Tx, inScope: Set<string>, sel: string): TxRow {
  return tx.rows.find((r) => r.ix.id === sel && inScope.has(r.ix.id)) ?? tx.rows.find((r) => inScope.has(r.ix.id)) ?? tx.rows[0]
}

// A request's own line: the method and the path with its query, read
// from the row, else from the ref ("METHOD url").
function requestLine(row: TxRow, sig: string): string {
  const method = typeof row.s.method === 'string' ? row.s.method : sig.slice(0, sig.indexOf(' '))
  let path = typeof row.s.path === 'string' ? row.s.path : ''
  if (!path) {
    try {
      const u = new URL(sig.slice(sig.indexOf(' ') + 1))
      path = `${u.pathname}${u.search}`
    } catch {
      path = sig
    }
  }
  return `${method} ${path}`
}

function TxItem({ tx, inScope, model, lens, act }: { tx: Tx; inScope: Set<string>; model: Model; lens: Lens; act: Act }) {
  const on = lens.tx === tx.sig
  const row = focusRow(tx, inScope, lens.sel)
  const then = thenOf(row)
  const replay = baselineReplay(row.ix.replays, tx.sig)
  const now = replay ? nowOf(replay, row.ix.pid) : undefined
  const touched = tx.rows.filter((r) => inScope.has(r.ix.id))
  const web = model.words.web
  return (
    <div className="border-t border-border first:border-t-0">
      <button type="button" aria-expanded={on} onClick={() => act.pickTx(on ? null : tx.sig)} className={cn('grid w-full grid-cols-[minmax(0,1fr)_44px_18px_18px] items-center gap-2.5 px-3.5 py-1.5 text-left font-mono text-[11px] text-muted-foreground hover:bg-accent/50', on && 'bg-accent text-foreground shadow-[inset_2px_0_0_var(--primary)]')}>
        <span className="flex min-w-0 flex-col gap-0.5">
          {lens.level !== 'instruction' && !web && (
            <span className="truncate text-foreground">{touched.slice(0, 2).map((r) => (lens.level === 'system' ? ixLabel(model, r.ix.id) : r.ix.node.name)).join(' · ')}{touched.length > 2 ? ` +${touched.length - 2}` : ''}</span>
          )}
          {web && lens.level === 'system' && model.programs.length > 1 && <span className="truncate text-faint">{programName(model, row.ix.pid)}</span>}
          <span className="flex min-w-0 items-center gap-2">
            {web ? <span className="truncate text-foreground" title={tx.sig}>{requestLine(row, tx.sig)}</span> : <span className="shrink-0">{short(tx.sig, 8, 6)}</span>}
            {typeof row.s.args === 'object' && row.s.args !== null && <span className="min-w-0 overflow-hidden"><Args args={row.s.args as Record<string, string>} types={argTypes(row.ix.node.facts)} max={3} inline /></span>}
          </span>
        </span>
        <span className="num flex items-center justify-end gap-1 text-right">{tx.live && <span className="size-1.5 rounded-full bg-primary" title="seen by the watch" />}{relTime(tx.at).replace(' ago', '')}</span>
        <span className={cn('size-[7px] justify-self-center rounded-full', then.ok ? 'bg-ok' : 'bg-bad')} title={`${web ? 'status' : 'then'} · ${then.word}`} />
        {web ? <span className="num justify-self-end text-[10px] text-faint">{then.word}</span> : <span className={cn('size-[7px] justify-self-center rounded-full', now ? (now.ok ? 'bg-ok' : 'bg-bad') : 'border border-dashed border-input')} title={now ? `now · ${now.word}` : 'not replayed'} />}
      </button>
      {on && (web ? <RequestDetail tx={tx} row={row} then={then} model={model} /> : <TxDetail tx={tx} row={row} then={then} model={model} />)}
    </div>
  )
}

// One request, opened in place: the URL, how the server answered and how
// long it took, what came back, and the hop that reached the route.
function RequestDetail({ tx, row, then, model }: { tx: Tx; row: TxRow; then: Outcome; model: Model }) {
  const str = (v: unknown): string => (typeof v === 'string' ? v : '')
  const num = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined)
  const url = model.words.txHref(tx.sig, row.ix.pid)
  const mime = str(row.s.mime)
  const bytes = num(row.s.bytes)
  return (
    <div className="flex flex-col gap-3 border-t border-border bg-muted/50 px-3.5 py-3">
      <a href={url} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 font-mono text-[11px] text-muted-foreground hover:text-foreground">
        <span className="truncate">{url}</span> <ExternalLink className="size-3 shrink-0" />
      </a>
      <div className="flex flex-wrap items-center gap-2 font-mono text-[11px]">
        <StateBadge state={then.ok ? 'ok' : 'bad'} className="h-5 px-1.5 text-[10.5px]">{then.word}</StateBadge>
        {then.units !== undefined && <span className="num text-muted-foreground">{then.units.toLocaleString('en-US')} ms</span>}
        {mime && <span className="text-faint">{mime.split(';')[0]}</span>}
        {bytes !== undefined && bytes > 0 && <span className="num text-faint">{bytes >= 1024 ? `${(bytes / 1024).toFixed(bytes >= 10240 ? 0 : 1)} kB` : `${bytes} B`}</span>}
      </div>
      <div className="flex flex-col gap-1">
        <span className="label-caps">route</span>
        {tx.hops.map(([a, b], i) => (
          <span key={`${a}>${b}`} className="grid grid-cols-[16px_minmax(0,1fr)] items-center gap-2 font-mono text-[11px]">
            <span className="flex size-4 items-center justify-center rounded-full bg-primary text-[9.5px] font-semibold text-primary-foreground">{i + 1}</span>
            <span className="truncate"><span className="text-muted-foreground">{endLabel(model, a)}</span> → {endLabel(model, b)}</span>
          </span>
        ))}
      </div>
    </div>
  )
}

// One transaction, opened in place: what the chain recorded, what the
// replay gives today, how far each got, the route the map is lighting, and
// the replay itself.
function TxDetail({ tx, row, then, model }: { tx: Tx; row: TxRow; then: Outcome; model: Model }) {
  const [pick, setPick] = useState('')
  const sigReplays = row.ix.replays.filter((r) => r.replay === tx.sig)
  const replay = sigReplays.find((r) => r.key === pick) ?? baselineReplay(row.ix.replays, tx.sig)
  const now = replay ? nowOf(replay, row.ix.pid) : undefined
  const keys = Array.isArray(row.s.signer_keys) ? (row.s.signer_keys as string[]) : []
  const moves = Array.isArray(row.s.moves) ? (row.s.moves as TokenMove[]) : undefined
  const track = (o?: Outcome) =>
    STAGES.map((_, i) => <span key={i} className={!o ? 'h-0 border-t border-dashed border-input' : o.ok || i + 1 < o.stage ? 'h-1 rounded-full bg-ok' : i + 1 === o.stage ? 'h-1 rounded-full bg-bad' : 'h-1 rounded-full bg-border'} />)
  return (
    <div className="flex flex-col gap-3 border-t border-border bg-muted/50 px-3.5 py-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] text-faint">
        <a href={model.words.txHref(tx.sig, row.ix.pid)} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-muted-foreground hover:text-foreground">{short(tx.sig, 10, 8)} <ExternalLink className="size-3" /></a>
        {typeof row.s.slot === 'number' && <span className="num">{model.words.height} {row.s.slot.toLocaleString('en-US')}</span>}
        {keys.length > 0 && <Copy value={keys[0]} className="text-[11px] text-faint">{short(keys[0], 4, 4)}{keys.length > 1 ? ` +${keys.length - 1}` : ''}</Copy>}
      </div>
      <div className="flex flex-col gap-1.5">
        <div className="grid grid-cols-[34px_minmax(0,1fr)] items-center gap-2"><span className="label-caps text-[9.5px]">then</span><OutcomeMark o={then} none="unknown" unit={model.words.units} /></div>
        <div className="grid grid-cols-[34px_minmax(0,1fr)] items-center gap-2"><span className="label-caps text-[9.5px]">now</span><OutcomeMark o={now} none="not replayed" unit={model.words.units} /></div>
      </div>
      <div className="grid grid-cols-[34px_repeat(6,minmax(0,1fr))] items-center gap-x-1 gap-y-1 font-mono text-[9.5px] text-muted-foreground">
        <span />{STAGES.map((s, i) => <span key={s} className="truncate">{i + 1} {s}</span>)}
        <span className="label-caps text-[9.5px]">then</span>{track(then)}
        <span className="label-caps text-[9.5px]">now</span>{track(now)}
      </div>
      <div className="flex flex-col gap-1">
        <span className="label-caps">route</span>
        {tx.hops.map(([a, b], i) => (
          <span key={`${a}>${b}`} className="grid grid-cols-[16px_minmax(0,1fr)] items-center gap-2 font-mono text-[11px]">
            <span className="flex size-4 items-center justify-center rounded-full bg-primary text-[9.5px] font-semibold text-primary-foreground">{i + 1}</span>
            <span className="truncate"><span className="text-muted-foreground">{endLabel(model, a)}</span> → {endLabel(model, b)}</span>
          </span>
        ))}
      </div>
      {typeof row.s.args === 'object' && row.s.args !== null && <div className="flex flex-col gap-1"><span className="label-caps">args</span><Args args={row.s.args as Record<string, string>} name={row.ix.node.name} types={argTypes(row.ix.node.facts)} max={12} /></div>}
      <Touched row={row} />
      {moves && moves.length > 0 && <div className="flex flex-col gap-1"><span className="label-caps">moved · then</span><Moves moves={moves} max={6} /></div>}
      {replay ? (
        <ReplayDetail r={replay} siblings={sigReplays} onPick={setPick} byId={model.byId} accounts={row.ix.accounts} facts={row.ix.node.facts ?? {}} href={model.words.txHref(replay.replay, row.ix.pid)} unit={model.words.units} compact />
      ) : (
        <StateBadge state="pending" className="h-5 w-fit px-1.5 text-[10.5px]">not replayed</StateBadge>
      )}
    </div>
  )
}

// The scoped log on the sample's own time axis: failed in red, replayed
// ringed, the open one larger.
function Strip({ txs, inScope, sampling, lens, act }: { txs: Tx[]; inScope: Set<string>; sampling?: Sampling; lens: Lens; act: Act }) {
  if (txs.length === 0) return null
  const times = txs.map((t) => new Date(t.at ?? 0).getTime())
  const from = sampling?.from ? new Date(sampling.from).getTime() : Math.min(...times)
  const to = sampling?.to ? new Date(sampling.to).getTime() : Math.max(...times)
  const span = Math.max(1, to - from)
  return (
    <div className="border-b border-border px-3.5 pt-2.5 pb-1.5">
      <div className="relative h-6 border-y border-border">
        {txs.map((t, i) => {
          const row = focusRow(t, inScope, lens.sel)
          const x = 2 + ((times[i] - from) / span) * 96
          return (
            <button key={t.sig} type="button" aria-label={t.sig} onClick={() => act.pickTx(lens.tx === t.sig ? null : t.sig)} className="absolute top-1/2 flex size-3.5 -translate-x-1/2 -translate-y-1/2 items-center justify-center" style={{ left: `${Math.max(1, Math.min(99, x))}%` }}>
              <span className={cn('block size-[7px] rounded-full', row.s.failed ? 'bg-bad' : 'bg-ok', baselineReplay(row.ix.replays, t.sig) && 'ring-1 ring-foreground ring-offset-2 ring-offset-card', lens.tx === t.sig && 'size-2.5 ring-2 ring-primary')} />
            </button>
          )
        })}
      </div>
      <div className="flex justify-between pt-1 font-mono text-[10px] text-faint">
        <span>{relTime(new Date(from).toISOString())}</span>
        <span>{relTime(new Date(to).toISOString())}</span>
      </div>
    </div>
  )
}

// A program that changed under the audit: the newest facts the watch read,
// dated when it landed. It sits above the transactions because it changes
// what every later row ran against.
function DriftItem({ d, model }: { d: DriftRow; model: Model }) {
  const str = (v: unknown): string => (typeof v === 'string' ? v : '')
  const code = str(d.s.bytecode_now)
  const auth = str(d.s.authority_now)
  const impl = str(d.s.implementation)
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_44px] items-center gap-2.5 border-t border-border bg-warn-soft/40 px-3.5 py-1.5 font-mono text-[11px] first:border-t-0">
      <span className="flex min-w-0 flex-wrap items-center gap-2">
        <StateBadge state={d.kind === 'idl' ? 'warn' : 'bad'} className="h-5 px-1.5 text-[10.5px]">{d.kind === 'idl' ? 'interface changed' : 'upgraded'}</StateBadge>
        <span className="truncate text-foreground">{programName(model, d.pid)}</span>
        {code && <span className="text-faint" title={code}>code {short(code, 6, 4)}</span>}
        {impl && <span className="text-faint" title={impl}>impl {short(impl, 4, 4)}</span>}
        {auth && <span className="text-faint" title={auth}>authority {short(auth, 4, 4)}</span>}
        {typeof d.s.slot === 'number' && <span className="num text-faint">{model.words.height} {d.s.slot.toLocaleString('en-US')}</span>}
      </span>
      <span className="num text-right text-faint">{relTime(d.at).replace(' ago', '')}</span>
    </div>
  )
}
