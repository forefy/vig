import { cn } from '@/lib/utils'

// A quantity against a cap. As a cap meter it turns warn near the cap and bad
// over it; as a remaining meter it turns warn when little is left and bad at
// none. Width is the only thing that animates.
export function Meter({
  value,
  max,
  label,
  kind = 'cap',
  format = (n) => n.toLocaleString('en-US'),
  className,
}: {
  value: number
  max: number
  label?: string
  kind?: 'cap' | 'remaining'
  format?: (n: number) => string
  className?: string
}) {
  const ratio = max > 0 ? value / max : 0
  const tone =
    kind === 'remaining'
      ? value <= 0
        ? 'bad'
        : ratio <= 0.2
          ? 'warn'
          : 'ok'
      : ratio > 1
        ? 'bad'
        : ratio > 0.85
          ? 'warn'
          : 'ok'
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <div className="flex items-baseline justify-between gap-3">
        {label && <span className="label-caps">{label}</span>}
        <span className={cn('num text-xs', tone === 'bad' ? 'text-bad' : 'text-muted-foreground')}>
          {format(value)} / {format(max)}
        </span>
      </div>
      <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
        <div
          className={cn('h-full rounded-full transition-[width]', tone === 'bad' ? 'bg-bad' : tone === 'warn' ? 'bg-warn' : 'bg-primary')}
          style={{ width: `${Math.max(0, Math.min(100, ratio * 100))}%` }}
        />
      </div>
    </div>
  )
}
