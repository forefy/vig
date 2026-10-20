import { Mark } from './Mark'
import { cn } from '@/lib/utils'

// Empty state: the mark, one line, one action. No paragraphs.
export function Empty({ title, action, className }: { title: string; action?: React.ReactNode; className?: string }) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-4 rounded-md border border-dashed border-border px-6 py-14 text-center', className)}>
      <Mark size={28} className="text-faint" />
      <span className="text-sm text-muted-foreground">{title}</span>
      {action}
    </div>
  )
}
