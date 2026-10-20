import { cn } from '@/lib/utils'

// The identity mark: two heavy strokes meeting at the base, with the accent
// as the point held in the notch. It is the only place the identity appears.
// No wordmark anywhere.
export function Mark({ size = 24, className }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} className={cn('shrink-0', className)} aria-hidden="true">
      <path d="M6 7 16 27 26 7" fill="none" stroke="currentColor" strokeWidth="4.2" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="16" cy="12" r="2.4" className="fill-primary" />
    </svg>
  )
}
