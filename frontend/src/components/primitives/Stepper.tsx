import { cn } from '@/lib/utils'
import { Check } from 'lucide-react'

// Wizard stepper. Order carries meaning here, so numbers are allowed.
export function Stepper({ steps, current, onSelect }: { steps: { key: string; title: string }[]; current: number; onSelect?: (i: number) => void }) {
  return (
    <ol className="flex flex-wrap items-center gap-x-2 gap-y-2">
      {steps.map((s, i) => {
        const done = i < current
        const active = i === current
        return (
          <li key={s.key} className="flex items-center gap-2">
            <button
              type="button"
              disabled={!done || !onSelect}
              onClick={() => onSelect?.(i)}
              className={cn('flex items-center gap-2 rounded-sm text-[13px] disabled:cursor-default', active ? 'text-foreground' : done ? 'text-foreground/80 hover:text-foreground' : 'text-muted-foreground')}
            >
              <span className={cn('flex size-5 items-center justify-center rounded-full border font-mono text-[10.5px]', active && 'border-primary text-primary', done && 'border-primary bg-primary text-primary-foreground', !active && !done && 'border-border')}>
                {done ? <Check className="size-3" /> : i + 1}
              </span>
              {s.title}
            </button>
            {i < steps.length - 1 && <span className="h-px w-5 bg-border" />}
          </li>
        )
      })}
    </ol>
  )
}
