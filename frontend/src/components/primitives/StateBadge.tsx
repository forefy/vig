import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import type { components } from '@/api/client'

export type BadgeState = components['schemas']['PanelRow']['state']
export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info'
// A badge carries a state (what something is) or a severity (how much a
// finding weighs); the two palettes never share a hue.
export type BadgeTone = BadgeState | Severity

const styles: Record<BadgeTone, string> = {
  ok: 'text-ok bg-ok-soft',
  warn: 'text-warn bg-warn-soft',
  bad: 'text-bad bg-bad-soft',
  neutral: 'text-muted-foreground border border-border',
  pending: 'text-pending border border-dashed border-border',
  critical: 'text-sev-critical bg-sev-critical-soft',
  high: 'text-sev-high bg-sev-high-soft',
  medium: 'text-sev-medium bg-sev-medium-soft',
  low: 'text-sev-low bg-sev-low-soft',
  info: 'text-sev-info bg-sev-info-soft',
}

// The ladder's ink and dot classes, for numbers and marks outside a badge.
export const severityInk: Record<Severity, string> = {
  critical: 'text-sev-critical',
  high: 'text-sev-high',
  medium: 'text-sev-medium',
  low: 'text-sev-low',
  info: 'text-sev-info',
}
export const severityDot: Record<Severity, string> = {
  critical: 'bg-sev-critical',
  high: 'bg-sev-high',
  medium: 'bg-sev-medium',
  low: 'bg-sev-low',
  info: 'bg-sev-info',
}

// A fact as a badge. State is the meaning; the text is the value.
export function StateBadge({
  state,
  children,
  className,
  mono = true,
  title,
  tip,
}: {
  state: BadgeTone
  children: React.ReactNode
  className?: string
  mono?: boolean
  title?: string
  // What the chip does not have room to say. A real tooltip rather than the
  // browser's, so it appears at once and is reachable from the keyboard.
  tip?: React.ReactNode
}) {
  const badge = (
    <span
      title={title}
      className={cn(
        'inline-flex h-6 max-w-full items-center gap-1.5 rounded-sm px-2 text-[11.5px] whitespace-nowrap',
        mono && 'font-mono',
        styles[state],
        className,
      )}
    >
      {state === 'pending' && <Dot className="animate-pulse" />}
      {state === 'ok' && <Dot />}
      {(state === 'bad' || state === 'critical' || state === 'high') && <Dot />}
      {children}
    </span>
  )
  if (!tip) return badge
  return (
    <Tooltip>
      <TooltipTrigger render={badge} />
      <TooltipContent>{tip}</TooltipContent>
    </Tooltip>
  )
}

function Dot({ className }: { className?: string }) {
  return <span className={cn('size-1.5 shrink-0 rounded-full bg-current', className)} />
}

export const severityState: Record<string, BadgeTone> = {
  critical: 'critical',
  high: 'high',
  medium: 'medium',
  low: 'low',
  info: 'info',
}

export const auditState: Record<string, BadgeState> = {
  queued: 'neutral',
  claimed: 'pending',
  cloning: 'pending',
  programs_verified: 'pending',
  analysis: 'pending',
  human_review: 'pending',
  delivered: 'ok',
  delayed: 'warn',
  failed: 'bad',
  cancelled: 'bad',
}
