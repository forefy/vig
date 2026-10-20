import { cn } from '@/lib/utils'

// Option cards: pick one. Selected is the accent; disabled shows "soon".
export function OptionCards<T extends string>({
  options,
  value,
  onChange,
  columns = 3,
  className,
}: {
  options: { value: T; label: string; hint?: string; disabled?: boolean; soon?: boolean }[]
  value?: T
  onChange: (v: T) => void
  columns?: 2 | 3
  className?: string
}) {
  return (
    <div className={cn('grid gap-2', columns === 3 ? 'grid-cols-1 sm:grid-cols-3' : 'grid-cols-1 sm:grid-cols-2', className)}>
      {options.map((o) => {
        const selected = o.value === value
        return (
          <button
            key={o.value}
            type="button"
            disabled={o.disabled}
            onClick={() => onChange(o.value)}
            aria-pressed={selected}
            className={cn(
              'group flex min-h-[72px] flex-col items-start gap-1 rounded-md border px-4 py-3 text-left transition-colors',
              selected ? 'border-primary bg-primary/5' : 'border-border bg-card hover:border-foreground/25',
              o.disabled && 'cursor-not-allowed opacity-50 hover:border-border',
            )}
          >
            <span className="flex w-full items-center justify-between gap-2">
              <span className={cn('text-[14px] font-medium', selected && 'text-primary')}>{o.label}</span>
              {o.soon && <span className="label-caps rounded-sm border border-border px-1.5 py-0.5 text-[9.5px]">soon</span>}
            </span>
            {o.hint && <span className="text-xs text-muted-foreground">{o.hint}</span>}
          </button>
        )
      })}
    </div>
  )
}
