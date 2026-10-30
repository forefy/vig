import { useState } from 'react'
import { chainWords } from '@/lib/chain'
import type { ChainWords } from '@/lib/chain'
import { Copy } from './Copy'
import { FactGroup, FactRow } from './Facts'
import { StateBadge } from './StateBadge'
import type { components } from '@/api/client'

type PinT = components['schemas']['Pin']
type ProgramPinT = components['schemas']['ProgramPin']
type PinRepoT = components['schemas']['PinRepo']
type FindingT = components['schemas']['Finding']

// How many findings sit in a pinned item: a program by its address, a
// repository by the package that names it or the path under it.
export function findingsIn(pin: PinT, findings: FindingT[]): { programs: Map<string, number>; repos: Map<string, number> } {
  const programs = new Map<string, number>()
  const repos = new Map<string, number>()
  for (const f of findings) {
    const loc = f.location
    if (loc.program_id) programs.set(loc.program_id, (programs.get(loc.program_id) ?? 0) + 1)
    for (const r of pin.repos ?? []) {
      const pkg = loc.package ?? ''
      if (pkg === r.name || pkg.startsWith(`${r.name} `) || (loc.file ?? '').startsWith(`${r.name}/`)) repos.set(r.name, (repos.get(r.name) ?? 0) + 1)
    }
  }
  return { programs, repos }
}

// The pinned items the page shows first: the ones with the most findings.
// Beyond the first few the rest fold behind their count.
const shownScope = 3

const matchState = { verified: 'ok', self_built: 'ok', mismatch: 'bad', unverified: 'neutral' } as const
const matchWord = { verified: 'verified', self_built: 'self-built', mismatch: 'mismatch', unverified: 'unverified' } as const

// The pinning line: exactly what was reviewed. Scope is a heading over a list,
// because a review covers several things at once - the repository at a commit,
// each program on chain - and every one of them is pinned in full. A pin that
// elides half an address or half a sha is not a pin.
export function Pin({ pin, adapter, findings }: { pin: PinT; adapter?: string; findings?: FindingT[] }) {
  const words = chainWords(adapter)
  const [open, setOpen] = useState(false)
  const counts = findingsIn(pin, findings ?? [])
  const items: { key: string; count: number; node: React.ReactNode }[] = [
    ...(pin.programs ?? []).map((p) => ({ key: `p:${p.id}`, count: counts.programs.get(p.id) ?? 0, node: programRow(p, words, findings ? counts.programs.get(p.id) ?? 0 : 0) })),
    ...(pin.repos ?? []).map((r) => ({ key: `r:${r.name}`, count: counts.repos.get(r.name) ?? 0, node: repoRow(r, findings ? counts.repos.get(r.name) ?? 0 : 0) })),
  ]
  if (findings) items.sort((a, b) => b.count - a.count)
  const folded = findings && !open && items.length > shownScope + 1
  const shown = folded ? items.slice(0, shownScope) : items
  return (
    <FactGroup label="scope">
        {pin.repo && (
          <FactRow kind={words.web ? 'site' : 'repo'}>
            <Copy value={pin.repo} className="break-all text-foreground">{pin.repo}</Copy>
            {/* Named, like the program's slot below it: a bare forty
                characters of hex says nothing about what it pins. */}
            {/* A live site without a snapshot has no commit to pin; the
                origin alone is the row. A commit belongs to a repo pin,
                where the source it names sits next to it. */}
            {pin.commit && !words.web && (
              <span className="flex flex-wrap items-center gap-x-1.5 font-mono text-xs text-faint">
                commit
                <Copy value={pin.commit} className="break-all">
                  {pin.base_sha ? `${pin.base_sha}..` : ''}
                  {pin.commit}
                </Copy>
              </span>
            )}
          </FactRow>
        )}
        {(pin.programs ?? []).length === 0 && (
          <FactRow>
            {/* A webapp pin is the live site; programs were never the
                subject, so the absence is not a downgrade to state. */}
            <StateBadge state="neutral">{adapter === 'webapp' ? 'live site' : 'source only'}</StateBadge>
          </FactRow>
        )}
        {shown.map((it) => (
          <FactRow key={it.key} kind={it.key.startsWith('p:') ? words.program : 'repo'}>
            {it.node}
          </FactRow>
        ))}
        {folded && (
          <FactRow>
            <button type="button" onClick={() => setOpen(true)} className="rounded-sm outline-none hover:text-foreground focus-visible:text-foreground">
              <StateBadge state="neutral">{items.length - shownScope} others</StateBadge>
            </button>
          </FactRow>
        )}
        {(pin.excluded ?? []).length > 0 && (
          <FactRow kind="excluded">
            <span className="flex flex-wrap gap-1">
              {(pin.excluded ?? []).map((p) => (
                <span key={p} className="rounded-sm border border-border px-1.5 font-mono text-[11px] text-muted-foreground">{p}</span>
              ))}
            </span>
          </FactRow>
        )}
    </FactGroup>
  )
}

// What the match was reached from, for the badge's tooltip.
function deployment(p: ProgramPinT, words: ChainWords) {
  return [p.slot > 0 ? `${words.height} ${p.slot.toLocaleString('en-US')}` : '', p.bytecode_sha256 ? `${words.hash} ${p.bytecode_sha256}` : ''].filter(Boolean).join(' \u00b7 ')
}

// How many findings sit in the item, after its name.
function count(n: number) {
  return n > 0 ? <span className="num ml-1 text-[11px] text-faint">{n}</span> : null
}

function programRow(p: ProgramPinT, words: ChainWords, n: number) {
  // The match rides on the address itself, ahead of the copy mark. What was
  // compared to reach it is on hover: the deployment and the image.
  return (
    <Copy value={p.id} className="break-all">
      {p.id}
      <StateBadge state={matchState[p.match]} className="ml-1" tip={deployment(p, words)}>{matchWord[p.match]}</StateBadge>
      {count(n)}
    </Copy>
  )
}

function repoRow(r: PinRepoT, n: number) {
  return (
    <>
      <Copy value={r.name} className="break-all text-foreground">{r.name}{count(n)}</Copy>
      <span className="flex flex-wrap items-center gap-x-1.5 font-mono text-xs text-faint">
        commit
        <Copy value={r.commit} className="break-all">{r.commit}</Copy>
      </span>
    </>
  )
}
