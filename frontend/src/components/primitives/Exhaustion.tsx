import { cn } from '@/lib/utils'
import type { components } from '@/api/client'

type Answer = components['schemas']['Exhaustion']

// What the reproduction ruled out, as a row of dots: one per item vig
// listed for the finding, filled when it was tried to the end, hollow when
// it was not. The row is a button; the items open under the finding's
// header when it is pressed, each with its words and, when not done, the
// writer's note on why.
export function ExhaustionDots({ answers, open, onToggle, className }: { answers: Answer[]; open: boolean; onToggle: () => void; className?: string }) {
  const done = answers.filter((a) => a.done).length
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={open}
      aria-label={`${done} of ${answers.length} ruled out`}
      title={`${done} of ${answers.length}`}
      className={cn('flex h-5 items-center gap-[3px] rounded-sm px-1 outline-none hover:bg-secondary focus-visible:bg-secondary', className)}
    >
      {answers.map((a) => (
        <span key={a.key} className={cn('inline-block size-[7px] rounded-full border', a.done ? 'border-ok bg-ok' : 'border-faint bg-transparent')} />
      ))}
    </button>
  )
}

export function ExhaustionList({ answers, className }: { answers: Answer[]; className?: string }) {
  return (
    <ul className={cn('flex flex-col gap-1 border-l border-border pl-3 text-[12px]', className)}>
      {answers.map((a) => (
        <li key={a.key} className="flex items-baseline gap-2">
          <span className={cn('mt-[1px] inline-block size-[7px] shrink-0 translate-y-[-1px] rounded-full border', a.done ? 'border-ok bg-ok' : 'border-faint bg-transparent')} />
          <span className={a.done ? 'text-foreground/80' : 'text-muted-foreground'}>{a.text || a.key}</span>
          {!a.done && a.note && <span className="font-mono text-[11px] text-faint">{a.note}</span>}
        </li>
      ))}
    </ul>
  )
}
