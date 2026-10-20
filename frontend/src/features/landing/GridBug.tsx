import { useEffect, useMemo, useState } from 'react'
import { reducedMotion } from '@/app/a11y'
import { Link } from 'react-router'
import { ArrowRight } from 'lucide-react'
import { $api } from '@/api/hooks'
import { StateBadge, severityState } from '@/components/primitives'
import { cn, short } from '@/lib/utils'
import { CELL, cardLeft, cardWidth, key, neighbours, placeCard, startNode } from './grid'

// A bug walking the blueprint grid. It moves node to node along the lines,
// leaves a short dotted trail, freezes and blinks under the cursor, and opens
// a real published report when caught. It only appears when that report
// exists, so the card is never a mock.
export function GridBug({ frameW, frameH, free, slug }: { frameW: number; frameH: number; free: ReadonlySet<string>; slug: string }) {
  const report = $api.useQuery('get', '/v1/reports/{slug}', { params: { path: { slug } } }, { retry: false, staleTime: Infinity })
  // Memoised: it is a dependency of the walk, and a fresh object each render
  // would restart the interval before it ever ticked.
  const home = useMemo(() => startNode(free, frameH), [free, frameH])
  const [walked, setPos] = useState(home)
  // The page reflows under the point when it resizes or the display face
  // lands; a node that is no longer free hands it back to its starting one.
  const pos = free.has(key(walked.x, walked.y)) ? walked : home
  const [trail, setTrail] = useState<{ x: number; y: number }[]>([])
  const [held, setHeld] = useState(false)
  const [open, setOpen] = useState(false)
  const [reduced] = useState(() => reducedMotion())

  useEffect(() => {
    if (held || open || reduced || !report.data) return
    const t = setInterval(() => {
      setPos((prev) => {
        const p = free.has(key(prev.x, prev.y)) ? prev : home
        const moves = neighbours(p.x, p.y, free)
        const next = moves[Math.floor(Math.random() * moves.length)] ?? p
        setTrail((tr) => [p, ...tr].slice(0, 6))
        return next
      })
    }, 900)
    return () => clearInterval(t)
  }, [held, open, reduced, report.data, free, home])

  // Close on Escape or a click anywhere else.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    const onDown = (e: MouseEvent) => {
      if (!(e.target as HTMLElement).closest('[data-bug-card]')) setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener('mousedown', onDown)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('mousedown', onDown)
    }
  }, [open])

  if (!report.data) return null
  const doc = report.data.document
  const pin = doc.report.pin
  const sum = doc.report.summary
  const findings = (doc.findings ?? []).slice(0, 2)
  const program = pin.programs?.[0]
  // owner/name wraps after the slash, never mid-word.
  const slash = pin.repo.indexOf('/')
  const owner = slash < 0 ? pin.repo : pin.repo.slice(0, slash)
  const name = slash < 0 ? '' : pin.repo.slice(slash + 1)
  // The card is centred under the point wherever it was caught, and never
  // takes the point's own row.
  const CARD_W = cardWidth(frameW)
  const left = cardLeft(pos.x, frameW, CARD_W)
  const place = placeCard(pos.y, frameH)
  return (
    <>
      {trail.map((t, i) => (
        <span
          key={`${t.x}-${t.y}-${i}`}
          className="pointer-events-none absolute size-1 rounded-full bg-bad"
          style={{ left: t.x * CELL - 2, top: t.y * CELL - 2, opacity: 0.55 - i * 0.08 }}
          aria-hidden="true"
        />
      ))}
      <button
        type="button"
        data-bug-card
        aria-label="Open an example report"
        aria-expanded={open}
        onMouseEnter={() => setHeld(true)}
        onMouseLeave={() => setHeld(false)}
        onFocus={() => setHeld(true)}
        onBlur={() => setHeld(false)}
        onClick={() => setOpen((o) => !o)}
        // Above the card: the margin is only wide enough for one of them, and
        // the point you caught is the thing that must stay visible.
        className={cn('absolute z-30 flex size-6 items-center justify-center rounded-full', held && 'cursor-pointer')}
        style={{ left: pos.x * CELL - 12, top: pos.y * CELL - 12, transition: reduced ? undefined : 'left 850ms linear, top 850ms linear' }}
      >
        <span className={cn('block size-2.5 rounded-full bg-bad', (held || open) && 'animate-[pulse_700ms_ease-in-out_infinite]')} />
      </button>
      {open && (
        <div
          data-bug-card
          role="dialog"
          aria-label="Example report"
          className="absolute z-20 flex flex-col gap-4 rounded-md border border-border bg-card p-5 text-left"
          style={{ left, width: CARD_W, ...place }}
        >
          <div className="flex flex-col gap-2">
            <span className="font-mono text-[13px] leading-snug break-words">
              {owner}/<wbr />
              {name}
            </span>
            <div className="num flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11px] text-muted-foreground">
              <span>{short(pin.commit, 7, 0)}</span>
              {program && <span>{short(program.id, 4, 4)}</span>}
              {program && (
                <StateBadge state={program.match === 'mismatch' ? 'bad' : program.match === 'unverified' ? 'neutral' : 'ok'}>
                  {program.match.replace('_', '-')}
                </StateBadge>
              )}
            </div>
          </div>
          <div className="grid grid-cols-5 gap-px overflow-hidden rounded-md border border-border bg-border">
            {(
              [
                ['crit', sum.critical, 'text-bad'],
                ['high', sum.high, 'text-bad'],
                ['medi', sum.medium, 'text-warn'],
                ['low', sum.low, 'text-muted-foreground'],
                ['info', sum.info, 'text-muted-foreground'],
              ] as const
            ).map(([k, n, tone]) => (
              <div key={k} className="flex flex-col gap-1 bg-card px-2 py-2.5">
                <span className={cn('label-caps text-[9.5px]', n > 0 && tone)}>{k}</span>
                <span className={cn('num text-base leading-none', n > 0 ? tone : 'text-faint')}>{n}</span>
              </div>
            ))}
          </div>
          <div className="flex flex-col gap-2.5">
            {findings.map((f) => (
              <div key={f.ordinal} className="flex min-w-0 items-center gap-2.5 text-[12.5px]">
                <StateBadge state={severityState[f.severity]} className="h-5 shrink-0 px-1.5 text-[10px]">
                  {f.severity}
                </StateBadge>
                <span className="min-w-0 truncate">{f.title}</span>
              </div>
            ))}
            {(doc.findings ?? []).length > findings.length && <span className="num pl-1 text-sm leading-none text-faint">…</span>}
          </div>
          <Link to={`/r/${slug}`} className="flex items-center gap-1 self-end text-xs text-muted-foreground hover:text-foreground">
            Full report <ArrowRight className="size-3" />
          </Link>
        </div>
      )}
    </>
  )
}
