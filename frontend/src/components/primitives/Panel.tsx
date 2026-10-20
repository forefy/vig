import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { StateBadge } from './StateBadge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { toast } from 'sonner'
import type { components } from '@/api/client'

type VerificationPanel = components['schemas']['VerificationPanel']
export type PanelRow = components['schemas']['PanelRow']
type CodeProfile = components['schemas']['CodeProfile']
type Disclosure = components['schemas']['Disclosure']

const hashRe = /^[0-9a-f]{64}$/i

// The verification panel: facts as badges. This replaces every help text.
export function Panel({ panel, onAction, className }: { panel: VerificationPanel; onAction?: (action: string, row: PanelRow) => void; className?: string }) {
  const rows = panel.rows ?? []
  return (
    <div className={cn('divide-y divide-border rounded-md border border-border bg-card', className)}>
      {rows.map((row) => {
        const isHash = !!row.value && hashRe.test(row.value)
        const text = isHash ? row.value!.slice(0, 12) : row.value || stateWord[row.state]
        return (
          <div key={row.key} className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-3 px-3.5 py-2">
            <span className="shrink-0 text-[13px] text-muted-foreground">{row.label}</span>
            <div className="flex min-w-0 items-center justify-end gap-2">
              {row.detail && <span className="num hidden min-w-0 truncate text-xs text-muted-foreground sm:inline" title={row.detail}>{row.detail}</span>}
              <StateBadge state={row.state} tip={isHash ? row.value : undefined}>
                {text}
              </StateBadge>
              {row.action && onAction && (
                <Button size="xs" variant="outline" onClick={() => onAction(row.action!, row)}>
                  {actionWord[row.action] ?? row.action}
                </Button>
              )}
            </div>
          </div>
        )
      })}
      {panel.profile && (
        <Fold
          label="bytecode"
          summary={
            <>
              <StateBadge state="neutral">{panel.profile.framework}</StateBadge>
              <span className="num text-xs text-muted-foreground">{kb(panel.profile.size)}</span>
            </>
          }
        >
          <Profile profile={panel.profile} />
        </Fold>
      )}
      {panel.disclosure && (
        <Fold
          label="security.txt"
          summary={
            <>
              <StateBadge state="neutral">{panel.disclosure.name || 'unnamed'}</StateBadge>
              {(panel.disclosure.auditors ?? []).length === 0 && <StateBadge state="warn">no auditor named</StateBadge>}
            </>
          }
        >
          <Disclosed d={panel.disclosure} />
        </Fold>
      )}
    </div>
  )
}

const kb = (n: number) => (n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} B`)

// A section that opens on demand. The header carries the facts a reader
// decides on, so a closed fold hides detail, never a verdict.
function Fold({ label, summary, children }: { label: string; summary: React.ReactNode; children: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="flex w-full items-center gap-2 px-3.5 py-2 text-left hover:bg-accent/40">
        <ChevronRight className={cn('size-3.5 text-muted-foreground transition-transform', open && 'rotate-90')} />
        <span className="label-caps">{label}</span>
        <span className="ml-auto flex flex-wrap items-center justify-end gap-2">{summary}</span>
      </button>
      {open && <div className="border-t border-border">{children}</div>}
    </div>
  )
}

// What the deployed bytes reveal, as facts. Neutral tones only: a capability
// is not a verdict.
function Profile({ profile: p }: { profile: CodeProfile }) {
  const chips = (items: string[] | null | undefined, tone: 'neutral' | 'pending' = 'neutral') =>
    (items ?? []).map((c) => (
      <StateBadge key={c} state={tone}>
        {c}
      </StateBadge>
    ))
  return (
    <div className="flex flex-col gap-3 px-3.5 py-3">
      <div className="grid grid-cols-3 gap-3">
        <Fact k="size" v={kb(p.size)} />
        <Fact k="instructions" v={p.units.toLocaleString()} />
        <Fact k="functions" v={p.functions.toLocaleString()} />
      </div>
      {(p.capabilities ?? []).length > 0 && <div className="flex flex-wrap gap-1.5">{chips(p.capabilities)}</div>}
      {(p.symbols ?? []).length > 0 && <ProfileList k="instructions" items={p.symbols ?? []} />}
      {(p.types ?? []).length > 0 && <ProfileList k="accounts" items={p.types ?? []} />}
      {(p.components ?? []).length > 0 && <ProfileList k="crates" items={(p.components ?? []).map((c) => `${c.name}@${c.version}`)} />}
      {(p.sources ?? []).length > 0 && <ProfileList k="sources" items={p.sources ?? []} />}
      {(p.imports ?? []).length > 0 && <ProfileList k="syscalls" items={p.imports ?? []} muted />}
      {p.toolchain && <Fact k="toolchain" v={p.toolchain} />}
    </div>
  )
}

// What the deployer wrote into the image about it (security.txt). Every
// value is their claim, so the tones stay neutral; links open as-is.
function Disclosed({ d }: { d: Disclosure }) {
  const host = (u: string) => u.replace(/^https?:\/\//, '').replace(/\/$/, '')
  const contact = (c: string) => c.replace(/^(email|link|discord|telegram|twitter|other):/, '')
  return (
    <div className="flex flex-col gap-3 px-3.5 py-3">
      <div className="num flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
        {d.project_url && <Href href={d.project_url}>{host(d.project_url)}</Href>}
        {d.source_url && <Href href={d.source_url}>{host(d.source_url)}</Href>}
        {d.policy && <Href href={d.policy}>policy</Href>}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {d.source_release && <StateBadge state="neutral">{d.source_release}</StateBadge>}
        {(d.auditors ?? []).map((a) => (
          <StateBadge key={a} state="neutral">
            {/^https?:/.test(a) ? host(a) : a}
          </StateBadge>
        ))}
        {(d.contacts ?? []).map((c) => (
          <StateBadge key={c} state="neutral">
            {contact(c)}
          </StateBadge>
        ))}
        {d.expiry && <StateBadge state="neutral">expires {d.expiry}</StateBadge>}
      </div>
    </div>
  )
}

function Href({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noreferrer noopener" className="truncate hover:text-foreground hover:underline" title={href}>
      {children}
    </a>
  )
}

function Fact({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="label-caps">{k}</span>
      <span className="num text-sm">{v}</span>
    </div>
  )
}

function ProfileList({ k, items, muted }: { k: string; items: string[]; muted?: boolean }) {
  const shown = items.slice(0, 24)
  return (
    <div className="flex flex-col gap-1">
      <span className="label-caps">
        {k} <span className="num">{items.length}</span>
      </span>
      <div className={cn('num flex flex-wrap gap-x-3 gap-y-0.5 text-xs', muted ? 'text-faint' : 'text-muted-foreground')}>
        {shown.map((s) => (
          <span key={s} className="truncate" title={s}>
            {s}
          </span>
        ))}
        {items.length > shown.length && <span className="text-faint">+{items.length - shown.length}</span>}
      </div>
    </div>
  )
}

// The built-in row actions. Callers hand any action they do not own to this.
export async function panelAction(action: string, row: PanelRow): Promise<boolean> {
  if (action !== 'copy') return false
  if (!row.value) return true
  try {
    await navigator.clipboard.writeText(row.value)
    toast('copied')
  } catch {
    toast.error('clipboard unavailable')
  }
  return true
}

const stateWord: Record<string, string> = { ok: 'ok', warn: 'check', bad: 'mismatch', neutral: '-', pending: 'pending' }
const actionWord: Record<string, string> = { prove_ownership: 'Prove', refresh: 'Refresh', copy: 'Copy' }
