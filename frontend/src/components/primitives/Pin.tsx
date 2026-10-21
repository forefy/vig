import { chainWords } from '@/lib/chain'
import type { ChainWords } from '@/lib/chain'
import { Copy } from './Copy'
import { FactGroup, FactRow } from './Facts'
import { StateBadge } from './StateBadge'
import type { components } from '@/api/client'

type PinT = components['schemas']['Pin']
type ProgramPinT = components['schemas']['ProgramPin']

const matchState = { verified: 'ok', self_built: 'ok', mismatch: 'bad', unverified: 'neutral' } as const
const matchWord = { verified: 'verified', self_built: 'self-built', mismatch: 'mismatch', unverified: 'unverified' } as const

// The pinning line: exactly what was reviewed. Scope is a heading over a list,
// because a review covers several things at once - the repository at a commit,
// each program on chain - and every one of them is pinned in full. A pin that
// elides half an address or half a sha is not a pin.
export function Pin({ pin, adapter }: { pin: PinT; adapter?: string }) {
  const programs = pin.programs ?? []
  const words = chainWords(adapter)
  return (
    <FactGroup label="scope">
        {pin.repo && (
          <FactRow kind={words.web ? 'site' : 'repo'}>
            <Copy value={pin.repo} className="break-all text-foreground">{pin.repo}</Copy>
            {/* Named, like the program's slot below it: a bare forty
                characters of hex says nothing about what it pins. */}
            {/* A live site without a snapshot has no commit to pin; the
                origin alone is the row. */}
            {pin.commit && (
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
        {programs.map((p) => (
          <FactRow key={p.id} kind={words.program}>
            {/* The match rides on the address itself, ahead of the copy mark.
                What was compared to reach it is on hover: the deployment and
                the image, which are detail until someone asks for them. */}
            <Copy value={p.id} className="break-all">
              {p.id}
              <StateBadge state={matchState[p.match]} className="ml-1" tip={deployment(p, words)}>{matchWord[p.match]}</StateBadge>
            </Copy>
          </FactRow>
        ))}
        {programs.length === 0 && (
          <FactRow>
            {/* A webapp pin is the live site; programs were never the
                subject, so the absence is not a downgrade to state. */}
            <StateBadge state="neutral">{adapter === 'webapp' ? 'live site' : 'source only'}</StateBadge>
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
