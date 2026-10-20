import { FactGroup, FactRow } from './Facts'
import { StateBadge } from './StateBadge'
import type { components } from '@/api/client'

type Methods = components['schemas']['Methods']
type Evidence = components['schemas']['Evidence']

// How a report was produced, in the same shape as scope: what looked, and
// what it ran. The campaign's numbers live on the tool rather than on the
// page - they are detail for whoever asks, not a headline.
export function MethodsStrip({ methods }: { methods: Methods | null | undefined }) {
  if (!methods) return null
  const fz = methods.fuzzing
  const agents = [methods.static && 'static', methods.bytecode && 'bytecode', methods.onchain && 'on-chain', methods.live && 'live'].filter(Boolean) as string[]
  if (agents.length === 0 && !fz) return null
  return (
    <FactGroup label="methods">
      {agents.length > 0 && (
        <FactRow kind="agents">
          <span className="flex flex-wrap items-center gap-1.5">
            {agents.map((a) => (
              <StateBadge key={a} state="neutral">{a}</StateBadge>
            ))}
          </span>
        </FactRow>
      )}
      {fz && (
        <FactRow kind="tools">
          <span className="flex flex-wrap items-center gap-1.5">
            <StateBadge state={fz.crashes > 0 ? 'warn' : 'neutral'} tip={campaign(fz)}>{fz.tool}</StateBadge>
          </span>
        </FactRow>
      )}
    </FactGroup>
  )
}

// The campaign, for the tool's tooltip.
function campaign(fz: NonNullable<Methods['fuzzing']>) {
  const hours = Math.round((fz.duration_s ?? 0) / 3600)
  return [
    `${fz.runs.toLocaleString('en-US')} runs`,
    fz.harnesses > 0 ? `${fz.harnesses} ${fz.harnesses === 1 ? 'harness' : 'harnesses'}` : '',
    `${fz.crashes} ${fz.crashes === 1 ? 'crash' : 'crashes'}`,
    hours > 0 ? `${hours}h` : '',
    fz.coverage ?? '',
  ]
    .filter(Boolean)
    .join(' \u00b7 ')
}

const evidenceWord: Record<string, string> = { static: 'static', fuzz: 'fuzz', onchain: 'on-chain', bytecode: 'bytecode' }

// The kind of evidence behind a finding, with its artifact in mono.
export function EvidenceBadge({ evidence }: { evidence: Evidence | null | undefined }) {
  if (!evidence) return null
  // The chip names what ran; what it produced is on hover. A finding carries
  // one line of these and the artifact is a reference, not a headline.
  const word = evidence.tool || evidenceWord[evidence.kind] || evidence.kind
  const detail = [evidence.tool ? evidenceWord[evidence.kind] ?? evidence.kind : '', evidence.artifact ?? ''].filter(Boolean).join(' \u00b7 ')
  return (
    <StateBadge
      state={evidence.kind === 'fuzz' || evidence.kind === 'onchain' ? 'pending' : 'neutral'}
      className="h-5 px-1.5 text-[10.5px]"
      tip={detail || undefined}
    >
      {word}
    </StateBadge>
  )
}
