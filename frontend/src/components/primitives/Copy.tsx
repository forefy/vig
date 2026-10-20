import { useState } from 'react'
import { Check, Copy as CopyIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

// Copy a value. The whole thing is the button.
export function Copy({ value, children, className, iconOnly }: { value: string; children?: React.ReactNode; className?: string; iconOnly?: boolean }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value)
          setDone(true)
          setTimeout(() => setDone(false), 1200)
        } catch {
          /* clipboard unavailable */
        }
      }}
      className={cn('inline-flex items-center gap-1.5 rounded-sm font-mono text-xs text-muted-foreground hover:text-foreground', className)}
      title="Copy"
    >
      {!iconOnly && (children ?? value)}
      {done ? <Check className={iconOnly ? 'size-3.5 text-ok' : 'size-3 text-ok'} /> : <CopyIcon className={iconOnly ? 'size-3.5' : 'size-3 opacity-60'} />}
    </button>
  )
}
