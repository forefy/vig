import { useState } from 'react'
import { ArrowRight, Flag, Route, Zap } from 'lucide-react'
import { CodeSnippet } from './CodeSnippet'
import { Copy } from './Copy'
import { Prose, type LinkTarget } from './Prose'
import { VisualFigure, VisualMark } from './Visual'
import { cn } from '@/lib/utils'
import { placeOf } from '@/lib/web'
import type { components } from '@/api/client'

type Finding = components['schemas']['Finding']
type Site = components['schemas']['Site']

type Step =
  | { kind: 'use'; text: string }
  | { kind: 'expected'; text: string }
  | { kind: 'code'; site: Site; n: number }
  | { kind: 'impact'; text: string }

// The reproduction chain of a finding. It opens on the groundwork: how the
// code is ordinarily used, then what it is supposed to guarantee; runs
// through the code steps, each with its
// location, its lines and at most one attachment; and closes on the impact,
// which carries the finding's own attachments: the script that ran it, the
// recording, the dump. A flow numbers the code steps left to right;
// occurrences are a row of file:line tabs between the same two ends. The
// report never grows; the reader switches.
export function FindingCode({ finding, visualSrc, pin, links }: { finding: Finding; visualSrc?: (id: string) => string; pin?: { repo: string; commit: string }; links?: LinkTarget[] }) {
  const sites: Site[] = (finding.sites ?? []).length > 0
    ? (finding.sites ?? [])
    : [{ location: finding.location, snippet: finding.snippet ?? undefined }]
  const steps: Step[] = [
    ...(finding.normal_use ? [{ kind: 'use', text: finding.normal_use } as Step] : []),
    ...(finding.expected ? [{ kind: 'expected', text: finding.expected } as Step] : []),
    ...sites.map((site, i) => ({ kind: 'code', site, n: i + 1 }) as Step),
    ...(finding.impact ? [{ kind: 'impact', text: finding.impact } as Step] : []),
  ]
  const [active, setActive] = useState(0)
  const step = steps[Math.min(active, steps.length - 1)] ?? steps[0]
  if (!step) return null
  const flow = finding.sites_kind === 'flow' || sites.length === 1
  const multi = steps.length > 1
  const last = steps.length - 1
  // The finding's own attachments belong to the end of the chain: the
  // impact item when there is one, the last code step otherwise.
  const own = visualSrc ? (finding.visuals ?? []) : []
  const showOwn = own.length > 0 && Math.min(active, last) === last
  return (
    <div className="flex flex-col gap-2">
      {multi && (
        <div className="flex flex-wrap items-center gap-1" role="tablist">
          {steps.map((st, i) => {
            // The step's words: its note, else its place. The open tab
            // shows all of them, wrapping; the others are cut to a width
            // and carry the whole text on hover.
            const label = st.kind === 'code' ? st.site.note || placeOf(st.site.location).url || `${base(st.site.location.file)}:${st.site.location.line_start}` : st.kind === 'use' ? 'normal use' : st.kind
            const caption = st.kind === 'code' ? st.site.visuals?.[0]?.caption : undefined
            return (
            <span key={i} className="flex items-center gap-1">
              <button
                type="button"
                role="tab"
                aria-selected={i === active}
                onClick={() => setActive(i)}
                title={caption ? `${label}\n${caption}` : label}
                className={cn(
                  'flex min-h-7 items-center gap-2 rounded-md border px-2.5 py-1 text-left font-mono text-[11px]',
                  i === active ? 'border-border bg-secondary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground',
                )}
              >
                {st.kind === 'use' && <Route className={cn('size-3', i === active ? 'text-foreground' : 'text-faint')} aria-hidden="true" />}
                {st.kind === 'expected' && <Flag className={cn('size-3', i === active ? 'text-ok' : 'text-faint')} aria-hidden="true" />}
                {st.kind === 'impact' && <Zap className={cn('size-3', i === active ? 'text-bad' : 'text-faint')} aria-hidden="true" />}
                {st.kind === 'code' && flow && <span className={cn('num', i === active ? 'text-bad' : 'text-faint')}>{st.n}</span>}
                <span className={cn('min-w-0', i === active ? 'whitespace-normal break-words' : 'max-w-[22rem] truncate')}>{label}</span>
                {st.kind === 'code' && st.site.visuals?.[0] && <VisualMark visual={st.site.visuals[0]} className="shrink-0 text-faint" />}
              </button>
              {flow && i < last && <ArrowRight className="size-3 shrink-0 text-faint" aria-hidden="true" />}
            </span>
            )
          })}
        </div>
      )}
      {/* The item's body: one bounded block carrying its mark. */}
      <div
        className={cn(
          'flex flex-col gap-3',
          multi && 'rounded-md border p-3',
          multi && step.kind === 'use' && 'border-border bg-secondary/30',
          multi && step.kind === 'expected' && 'border-ok/25 bg-ok-soft/30',
          multi && step.kind === 'impact' && 'border-bad/25 bg-bad-soft/30',
          multi && step.kind === 'code' && 'border-border',
        )}
      >
        {step.kind === 'use' && (
          <>
            <span className="label-caps text-muted-foreground">normal use</span>
            <Prose text={step.text} links={links} />
          </>
        )}
        {step.kind === 'expected' && (
          <>
            <span className="label-caps text-ok">expected</span>
            <Prose text={step.text} links={links} />
          </>
        )}
        {step.kind === 'code' && (
          <>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              {flow && multi && (
                <span className="num rounded-sm border px-1.5 text-[11px] text-bad">{step.n}</span>
              )}
              {/* The location opens the exact lines in the repo at the
                  pinned commit, like the known strip's references. */}
              {(() => {
                const loc = step.site.location
                // A webapp location is the live endpoint itself, not repo
                // lines: link straight to it and stop there.
                const place = placeOf(loc)
                if (place.url) {
                  return (
                    <a href={place.url} target="_blank" rel="noreferrer" className="min-w-0 max-w-full break-all rounded-sm font-mono text-[11.5px] text-muted-foreground underline decoration-border underline-offset-2 hover:text-foreground hover:decoration-foreground">
                      {place.url}
                    </a>
                  )
                }
                const gh = pin ? `https://github.com/${pin.repo}/blob/${pin.commit}/${loc.file}#L${loc.line_start}${loc.line_end && loc.line_end !== loc.line_start ? `-L${loc.line_end}` : ''}` : undefined
                const label = `${loc.file}:${loc.line_start}${loc.line_end && loc.line_end !== loc.line_start ? `-${loc.line_end}` : ''}`
                return gh ? (
                  <a href={gh} target="_blank" rel="noreferrer" className="min-w-0 max-w-full break-all rounded-sm font-mono text-[11.5px] text-muted-foreground underline decoration-border underline-offset-2 hover:text-foreground hover:decoration-foreground">
                    {label}
                  </a>
                ) : (
                  <Copy value={`${loc.file}:${loc.line_start}`} className="min-w-0 max-w-full break-all text-left text-[11.5px]">{label}</Copy>
                )
              })()}
            </div>
            {step.site.snippet && <CodeSnippet code={step.site.snippet.code} startLine={step.site.snippet.start_line} markFrom={step.site.location.line_start} markTo={step.site.location.line_end ?? undefined} fix={step.site.snippet.fix ?? undefined} fixAt={step.site.snippet.fix_at ?? undefined} />}
            {/* The step's one attachment, under its code. */}
            {visualSrc && step.site.visuals?.[0] && (
              <VisualFigure visual={step.site.visuals[0]} src={visualSrc(step.site.visuals[0].id)} />
            )}
          </>
        )}
        {step.kind === 'impact' && (
          <>
            <span className="label-caps text-bad">impact</span>
            <Prose text={step.text} links={links} />
          </>
        )}
        {showOwn && visualSrc && (
          <div className="flex flex-col gap-2">
            {own.map((v) => <VisualFigure key={v.id} visual={v} src={visualSrc(v.id)} />)}
          </div>
        )}
      </div>
    </div>
  )
}

function base(path: string) {
  return path.split('/').pop() ?? path
}
