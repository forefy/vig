import { cn } from '@/lib/utils'

// A number that matters, with its label under it. Used sparingly.
export function Stat({
  label,
  value,
  hint,
  tone,
  className,
  children,
}: {
  label: string
  value: React.ReactNode
  hint?: React.ReactNode
  tone?: 'ok' | 'warn' | 'bad'
  className?: string
  children?: React.ReactNode
}) {
  return (
    <div className={cn('flex flex-col gap-1 rounded-md border border-border bg-card p-4', className)}>
      <span className="label-caps">{label}</span>
      <span className={cn('num text-[28px] leading-none tracking-tight', tone === 'ok' && 'text-ok', tone === 'warn' && 'text-warn', tone === 'bad' && 'text-bad')}>
        {value}
      </span>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      {children}
    </div>
  )
}
