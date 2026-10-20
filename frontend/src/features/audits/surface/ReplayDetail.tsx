import { ExternalLink } from 'lucide-react'
import { StateBadge, Copy } from '@/components/primitives'
import { relTime, short, cn } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { isWhatIf, outcomeWord, time } from './types'
import type { Node, Replay, ReplayAccount } from './types'
import { Moves } from './Moves'
import { Callee } from './Callee'
import { Drift } from './Drift'

const DUST = 1_000_000 // lamports; 0.001 SOL
const nz = (v?: string) => Boolean(v && v !== '0')

// One re-execution: outcome, units, decoded arguments, every account with
// what changed, the call tree, the logs. Several replays of one signature
// sit behind chips; the chosen one is set against the earliest other, or a
// what-if against its baseline.
export function ReplayDetail({ r, siblings, onPick, byId, accounts, facts, href, unit = 'cu', compact }: { r: Replay; siblings: Replay[]; onPick: (key: string) => void; byId: Map<string, Node>; accounts: Node[]; facts: Record<string, string>; href?: string; unit?: string; compact?: boolean }) {
  const ok = r.outcome === 'ok'
  const cu = r.units
  const whatIf = isWhatIf(r)
  const others = siblings.filter((s) => s.key !== r.key).sort((a, b) => time(a.at) - time(b.at))
  const base = others.find((s) => !isWhatIf(s)) ?? others[0]
  const statics = new Map(accounts.filter((a) => a.name).map((a) => [a.name, a.facts ?? {}]))
  return (
    <div className={cn('flex flex-col gap-3', compact ? '' : 'rounded-md border border-border bg-card p-3.5')}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="label-caps">replay</span>
        <a href={href ?? `https://explorer.solana.com/tx/${r.replay}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 font-mono text-[11.5px] text-muted-foreground hover:text-foreground">{short(r.replay, 8, 6)} <ExternalLink className="size-3" /></a>
        <StateBadge state={ok ? 'ok' : 'bad'} tip={r.error?.msg ?? r.error?.raw}>{outcomeWord(r)}</StateBadge>
        {cu !== undefined && <span className="num font-mono text-[11px] text-muted-foreground">{cu.toLocaleString('en-US')} {unit}</span>}
        {cu !== undefined && <span className="h-1 w-24 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-primary/70" style={{ width: `${Math.min(100, Math.round((cu / 200_000) * 100))}%` }} /></span>}
        {r.via && <span className="font-mono text-[11px] text-faint">via <Callee node={byId.get(`p:${r.via}`)} id={`p:${r.via}`} /></span>}
        <Moves moves={r.moves} />
        {siblings.length > 1 ? (
          siblings.map((s) => (
            <button key={s.key} type="button" onClick={() => onPick(s.key)} title={s.at}>
              <StateBadge state="neutral" className={cn('h-5 px-1.5 text-[10.5px]', s.key === r.key && 'bg-accent text-foreground')}>{s.tag || relTime(s.at) || '·'}</StateBadge>
            </button>
          ))
        ) : r.tag ? (
          <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">{r.tag}</StateBadge>
        ) : null}
        {r.at && siblings.length <= 1 && <span className="num font-mono text-[11px] text-faint" title={r.at}>{relTime(r.at)}</span>}
      </div>
      {whatIf && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="label-caps">what-if</span>
          {r.patch && (
            <span className="rounded-sm border border-warn/50 px-1.5 py-0.5 font-mono text-[11px]" title={`${r.patch.name} = ${r.patch.value}`}>
              <span className="text-muted-foreground">{r.patch.name}</span> = {r.patch.value.length > 24 ? short(r.patch.value, 10, 6) : r.patch.value}
            </span>
          )}
          {(r.overrides ?? []).map((o) => (
            <Copy key={o} value={o} className="rounded-sm border border-warn/50 px-1.5 py-0.5 text-[11px]">{short(o, 4, 4)}</Copy>
          ))}
          {r.time_travel && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">{r.time_travel}</StateBadge>}
        </div>
      )}
      {base && <Drift now={r} base={base} label={whatIf && !isWhatIf(base) ? 'diff' : 'drift'} />}
      {r.args && Object.keys(r.args).length > 0 && <Args args={r.args} name={r.instruction} types={argTypes(facts)} max={12} />}
      {compact && (r.accounts ?? []).length > 0 && (
        <ul className="flex flex-col divide-y divide-border font-mono text-[11px]">
          {(r.accounts ?? []).map((a, i) => (
            <li key={`${a.pubkey}:${i}`} className="flex flex-col gap-0.5 py-1.5">
              <span className="flex flex-wrap items-center gap-x-2">
                <span className={a.name ? 'text-foreground' : 'text-faint'}>{a.name ?? `#${i}`}</span>
                <Copy value={a.pubkey} className="text-[11px]">{short(a.pubkey, 4, 4)}</Copy>
                {a.signer && <span className="text-warn">signer</span>}
                {a.writable && <span className="text-ok">writable</span>}
                {a.pda === 'mismatch' && <Tag tone="bad">pda mismatch</Tag>}
                {a.type && <span className="text-faint">{a.type}</span>}
              </span>
              {(a.changed ?? []).length > 0 && <Change a={a} statics={statics} facts={facts} />}
            </li>
          ))}
        </ul>
      )}
      {!compact && (r.accounts ?? []).length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] font-mono text-[11px]">
            <thead>
              <tr className="label-caps text-left">
                <th className="py-1 pr-3 font-medium">account</th>
                <th className="py-1 pr-3 font-medium">key</th>
                <th className="py-1 pr-3 font-medium"></th>
                <th className="py-1 pr-3 font-medium">type</th>
                <th className="py-1 font-medium">change</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {(r.accounts ?? []).map((a, i) => (
                <tr key={`${a.pubkey}:${i}`} className={cn((a.changed ?? []).length > 0 && 'bg-background/40')}>
                  <td className="py-1 pr-3">{a.name ?? <span className="text-faint">·</span>}</td>
                  <td className="py-1 pr-3"><Copy value={a.pubkey} className="text-[11px]">{short(a.pubkey, 4, 4)}</Copy></td>
                  <td className="py-1 pr-3 whitespace-nowrap">
                    {a.signer && <span className="mr-1 text-warn">signer</span>}
                    {a.writable && <span className="mr-1 text-ok">writable</span>}
                    {a.pda === 'ok' && <Tag tone="ok">pda</Tag>}
                    {a.pda === 'mismatch' && <Tag tone="bad">pda mismatch</Tag>}
                  </td>
                  <td className="py-1 pr-3 text-muted-foreground">{a.type ?? ''}</td>
                  <td className="py-1"><Change a={a} statics={statics} facts={facts} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {(r.calls ?? []).length > 0 && (
        <div className="flex flex-col gap-0.5 font-mono text-[11px]">
          {(r.calls ?? []).map((c, i) => (
            <div key={i} className="flex items-center gap-2" style={{ paddingLeft: `${(c.depth - 1) * 14}px` }}>
              <span className="text-faint">{c.depth}</span>
              <Callee node={byId.get(`p:${c.program}`)} id={`p:${c.program}`} className="text-muted-foreground" />
              {c.name && <span>{c.name}</span>}
            </div>
          ))}
        </div>
      )}
      {(r.logs ?? []).length > 0 && (
        <pre className={cn('overflow-auto rounded-md', compact ? 'max-h-[170px]' : 'max-h-[260px]', 'border border-border bg-background p-3 font-mono text-[11px] leading-[1.6] text-muted-foreground')}>
          {(r.logs ?? []).map((l, i) => (
            <div key={i} className={cn(l.includes(' failed') || l.includes('Error') ? 'text-bad' : l.includes('success') ? 'text-ok' : l.startsWith('Program log:') ? 'text-foreground' : '')}>{l}</div>
          ))}
        </pre>
      )}
    </div>
  )
}

// A one-word verdict in a cell.
function Tag({ tone, children }: { tone: 'ok' | 'warn' | 'bad' | 'neutral'; children: React.ReactNode }) {
  return (
    <span className={cn('mr-1 inline-block rounded-sm border px-1 text-[10px] whitespace-nowrap', tone === 'ok' ? 'border-ok/40 text-ok' : tone === 'warn' ? 'border-warn/50 text-warn' : tone === 'bad' ? 'border-bad/40 text-bad' : 'border-border text-muted-foreground')}>
      {children}
    </span>
  )
}

// What a replay changed on one account: lamports, size, decoded fields.
// A size change is paired with the source's realloc; a lamports change
// the source never declared is tagged.
function Change({ a, statics, facts }: { a: ReplayAccount; statics: Map<string, Record<string, string>>; facts: Record<string, string> }) {
  const ch = a.changed ?? []
  if (ch.length === 0) return <span className="text-faint">·</span>
  const b = a.before, af = a.after
  const src = a.name ? statics.get(a.name) : undefined
  return (
    <div className="flex flex-col gap-0.5">
      {ch.map((k) => {
        if (k === 'exists') return <span key={k} className="text-warn">{b ? 'closed' : 'created'}</span>
        if (k === 'lamports') {
          const d = (af?.lamports ?? 0) - (b?.lamports ?? 0)
          const undeclared = !nz(facts.lamports) && !nz(facts.cpi) && Math.abs(d) > DUST
          return (
            <span key={k} className={d < 0 ? 'text-bad' : 'text-ok'}>
              {d > 0 ? '+' : ''}{(d / 1e9).toLocaleString('en-US', { maximumFractionDigits: 6 })} SOL
              {undeclared && <Tag tone="warn">lamports</Tag>}
            </span>
          )
        }
        if (k === 'size')
          return (
            <span key={k} className="text-muted-foreground">
              size {b?.size} → {af?.size}{' '}
              {src?.realloc ? <Tag tone="neutral">realloc</Tag> : statics.size > 0 ? <Tag tone="warn">size Δ, no realloc</Tag> : null}
            </span>
          )
        const from = b?.fields?.[k] ?? '', to = af?.fields?.[k] ?? ''
        return <span key={k}><span className="text-muted-foreground">{k}</span> {short(from, 6, 4)} → <span className="text-foreground">{short(to, 6, 4)}</span></span>
      })}
    </div>
  )
}

// The inputs of one call, as chips: name muted, value plain. Long values
// are cut and carried whole on hover.
// argTypes reads the map's `name:type, …` fact into a lookup.
export function argTypes(facts: Record<string, string> | undefined): Record<string, string> | undefined {
  const f = facts?.args
  if (!f) return undefined
  const out: Record<string, string> = {}
  for (const part of f.split(', ')) {
    const [k, t] = part.split(':')
    if (k && t) out[k] = t
  }
  return out
}

// A call's arguments read as the call in source: the instruction name,
// then one argument per line as `name: value`, values typed by their
// shape. Big numbers are grouped and carry their scale, addresses are
// short and copyable, booleans are colored, nested fields keep their
// dotted path. Inline, the same on one line, cut to a few.
export function Args({ args, name, types, max = 8, inline }: { args: Record<string, string>; name?: string; types?: Record<string, string>; max?: number; inline?: boolean }) {
  // Top-level arguments first, in the order the call declares them when
  // the map knows it (the row's map lost that order), then their nested
  // fields; nested keys carry a dot.
  const order = types ? Object.keys(types) : []
  const rank = (k: string) => {
    const i = order.indexOf(k.split('.')[0])
    return i < 0 ? order.length : i
  }
  const entries = Object.entries(args).sort((a, b) => Number(a[0].includes('.')) - Number(b[0].includes('.')) || rank(a[0]) - rank(b[0]))
  const shown = entries.slice(0, max)
  const more = entries.length - shown.length
  if (inline) {
    return (
      <span className="inline-flex min-w-0 flex-wrap items-center gap-x-1.5 font-mono text-[10.5px]" title={entries.map(([k, v]) => `${k}: ${v}`).join('\n')}>
        {shown.map(([k, v], i) => (
          <span key={k} className="whitespace-nowrap">
            <span className="text-muted-foreground">{k}:</span> <Value v={v} type={types?.[k]} name={k} inline />{i < shown.length - 1 || more > 0 ? <span className="text-faint">,</span> : null}
          </span>
        ))}
        {more > 0 && <span className="text-faint">+{more}</span>}
      </span>
    )
  }
  return (
    <div className="font-mono text-[11.5px] leading-[1.75]">
      <span className="text-muted-foreground">{name ?? 'args'}(</span>
      {shown.map(([k, v]) => (
        <div key={k} className="flex min-w-0 items-baseline gap-1 pl-4">
          <span className="shrink-0 text-muted-foreground">{k}:</span>
          <Value v={v} type={types?.[k]} name={k} />
          <span className="text-faint">,</span>
        </div>
      ))}
      {more > 0 && <div className="pl-4 text-faint">… +{more}</div>}
      <span className="text-muted-foreground">)</span>
    </div>
  )
}

// A time-like argument: named like one and holding seconds within this
// century. Rendered as the moment it names, past in the muted tone.
const timeName = /(deadline|expir|until|timestamp|valid_?(after|before|until)|not_?(before|after)|start_?time|end_?time|_at$|^at$|^time$)/i
function asMoment(name: string | undefined, v: string): Date | null {
  if (!name || !timeName.test(name) || !/^\d{9,10}$/.test(v)) return null
  const n = Number(v)
  if (n < 946_684_800 || n > 4_102_444_800) return null
  return new Date(n * 1000)
}

// Value is one argument value, typed by shape: bool, number, address,
// text, moment. type is the IDL's word for it when the map knows one;
// name is what the call names it, which is what tells a time from a count.
function Value({ v, type, name, inline }: { v: string; type?: string; name?: string; inline?: boolean }) {
  const now = useNow(60_000)
  if (v === 'true' || v === 'false') return <span className={v === 'true' ? 'text-ok' : 'text-bad'}>{v}</span>
  const at = asMoment(name, v)
  if (at) {
    const past = at.getTime() < now
    const iso = at.toISOString()
    return (
      <span className={cn('num whitespace-nowrap', past ? 'text-muted-foreground' : 'text-foreground')} title={`${type ? `${type} ` : ''}${v} · ${past ? 'passed' : 'ahead'} · ${relTime(iso)}`}>
        {inline ? iso.slice(0, 16).replace('T', ' ') : iso.slice(0, 19).replace('T', ' ')}Z
      </span>
    )
  }
  if (/^-?\d+$/.test(v)) {
    const neg = v.startsWith('-')
    const digits = neg ? v.slice(1) : v
    const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, '_')
    const sci = digits.length > 12 ? `${digits[0]}.${digits.slice(1, 3)}e${digits.length - 1}` : ''
    return (
      <span className="min-w-0 break-all text-foreground" title={type ? `${type} ${v}` : v}>
        {neg ? '-' : ''}{inline && digits.length > 12 ? sci : grouped}
        {!inline && sci && <span className="ml-1.5 text-faint">// {sci}</span>}
      </span>
    )
  }
  if (/^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(v)) {
    return inline ? <span className="text-foreground" title={v}>{short(v, 4, 4)}</span> : <Copy value={v} className="text-[11.5px] text-foreground">{short(v, 6, 6)}</Copy>
  }
  if (/^\[.*\]$/.test(v) || v.length > 40) return <span className="min-w-0 break-all text-foreground" title={v}>{inline ? short(v, 12, 6) : v}</span>
  return <span className="text-foreground" title={type}>{/^[a-z_]+$/.test(v) && type && !/^(string|str)$/.test(type) ? v : `"${v}"`}</span>
}
