import { useState } from 'react'
import { Download } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Switch } from '@/components/ui/switch'
import { StateBadge } from '@/components/primitives'
import { cn } from '@/lib/utils'
import type { components } from '@/api/client'

type Finding = components['schemas']['Finding']

// The parts of a report an export may leave out, in the server's words,
// with what the page calls each.
const parts: { key: string; word: string }[] = [
  { key: 'scope', word: 'components' },
  { key: 'methods', word: 'methods' },
  { key: 'ruled_out', word: 'ruled out' },
  { key: 'history', word: 'history' },
  { key: 'code', word: 'code' },
  { key: 'attachments', word: 'attachments' },
  { key: 'references', word: 'references' },
  { key: 'known', word: 'known' },
]

// What an export reaches: the owner's report by audit id, a public one by
// its slug.
export type ExportTarget = { id: string } | { slug: string }

// What the last export chose, kept in the browser so the next one starts
// from it: the parts left out, the numbering, the archived ones. Which
// findings go is the page's own question each time.
type Prefs = { without: string[]; renumber: boolean; archived: boolean; fullScope: boolean }
const prefsKey = 'vig.export'
const defaults: Prefs = { without: ['methods', 'ruled_out', 'history', 'known'], renumber: true, archived: false, fullScope: false }

function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(prefsKey)
    if (!raw) return defaults
    const p = JSON.parse(raw) as Partial<Prefs>
    return {
      without: Array.isArray(p.without) ? p.without.filter((k): k is string => typeof k === 'string' && parts.some((x) => x.key === k)) : defaults.without,
      renumber: typeof p.renumber === 'boolean' ? p.renumber : defaults.renumber,
      archived: typeof p.archived === 'boolean' ? p.archived : defaults.archived,
      fullScope: typeof p.fullScope === 'boolean' ? p.fullScope : defaults.fullScope,
    }
  } catch {
    return defaults
  }
}

function savePrefs(p: Prefs) {
  try {
    localStorage.setItem(prefsKey, JSON.stringify(p))
  } catch {
    /* storage may be unavailable */
  }
}

// The report leaves the page as a file: the findings document as JSON, or
// on paper as a PDF the server draws from that same document. What goes
// is chosen first: every finding or the ones the query keeps on screen,
// with the archived ones only when asked for.
export function ExportDialog({ target, repo, components, all, shown, filtered }: { target: ExportTarget; repo: string; components: number; all: Finding[]; shown: Finding[]; filtered: boolean }) {
  const [open, setOpen] = useState(false)
  // What is on screen is the default while a query narrows the page.
  const [picked0, setScope] = useState<'all' | 'view' | null>(null)
  const scope = picked0 ?? (filtered ? 'view' : 'all')
  // A file handed on carries the findings, their code, captures and
  // references; what ran and what was re-rated stay on the page unless
  // asked for, and numbers run 1 upward in reading order. Whatever the
  // last export chose instead is where the next one starts.
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs)
  const withArchived = prefs.archived
  const without = new Set(prefs.without)
  const renumber = prefs.renumber
  const update = (next: Partial<Prefs>) => {
    const p = { ...prefs, ...next }
    setPrefs(p)
    savePrefs(p)
  }
  const setWithArchived = (archived: boolean) => update({ archived })
  const setRenumber = (renumber: boolean) => update({ renumber })
  const setFullScope = (fullScope: boolean) => update({ fullScope })
  const toggle = (key: string) => update({ without: without.has(key) ? prefs.without.filter((k) => k !== key) : [...prefs.without, key] })
  const [busy, setBusy] = useState<'json' | 'pdf' | null>(null)
  const live = all.filter((f) => f.status !== 'archived')
  const archived = all.filter((f) => f.status === 'archived')
  const base = scope === 'view' ? shown : live
  const picked = [...base, ...(withArchived ? archived : [])].map((f) => f.ordinal).sort((a, b) => a - b)

  const run = async (kind: 'json' | 'pdf') => {
    setBusy(kind)
    try {
      const blob = await fetchExport(target, kind, picked.join(','), [...without].join(','), renumber, prefs.fullScope && components > 4)
      if (!blob) return toast.error('Export failed')
      save(blob, `${repo.replace('/', '-')}-findings.${kind}`)
      setOpen(false)
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <Download className="size-4" /> Export
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="label-caps">export</DialogTitle>
          <div className="flex flex-wrap items-center gap-2">
            <Pick on={scope === 'all'} onClick={() => setScope('all')}>
              all <span className="num">{live.length}</span>
            </Pick>
            {filtered && (
              <Pick on={scope === 'view'} onClick={() => setScope('view')}>
                in view <span className="num">{shown.length}</span>
              </Pick>
            )}
          </div>
          {archived.length > 0 && (
            <label className="flex w-fit items-center gap-2 text-[12.5px]">
              <Switch checked={withArchived} onCheckedChange={setWithArchived} />
              archived <span className="num text-muted-foreground">{archived.length}</span>
            </label>
          )}
          <label className="flex w-fit items-center gap-2 text-[12.5px]">
            <Switch checked={renumber} onCheckedChange={setRenumber} />
            renumber <span className="num text-muted-foreground">{renumber ? `01-${String(picked.length).padStart(2, '0')}` : picked.slice(0, 4).map((n) => String(n).padStart(2, '0')).join(' ') + (picked.length > 4 ? ' …' : '')}</span>
          </label>
          {components > 4 && (
            <label className="flex w-fit items-center gap-2 text-[12.5px]">
              <Switch checked={prefs.fullScope} onCheckedChange={setFullScope} />
              all components <span className="num text-muted-foreground">{components}</span>
            </label>
          )}
          <div className="flex flex-col gap-1.5">
            <span className="label-caps">with</span>
            <div className="flex flex-wrap gap-1.5">
              {parts.map((p) => (
                <Pick key={p.key} on={!without.has(p.key)} onClick={() => toggle(p.key)} small>
                  {p.word}
                </Pick>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <StateBadge state={picked.length ? 'neutral' : 'warn'}>{picked.length} findings</StateBadge>
            <div className="ml-auto flex gap-2">
              <Button size="sm" variant="outline" disabled={!picked.length || !!busy} onClick={() => void run('json')}>
                <Download className="size-4" /> JSON
              </Button>
              <Button size="sm" disabled={!picked.length || !!busy} onClick={() => void run('pdf')}>
                <Download className="size-4" /> PDF
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}

function Pick({ on, onClick, children, small }: { on: boolean; onClick: () => void; children: React.ReactNode; small?: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn('flex items-center gap-1.5 rounded-md border', small ? 'h-6 px-2 text-[11.5px]' : 'h-7 px-2.5 text-xs', on ? 'border-foreground/40 bg-secondary text-foreground' : 'border-border text-muted-foreground line-through decoration-faint hover:text-foreground')}
    >
      {children}
    </button>
  )
}

async function fetchExport(target: ExportTarget, kind: 'json' | 'pdf', findings: string, without: string, renumber: boolean, fullScope: boolean): Promise<Blob | null> {
  const query = { findings, without, renumber, full_scope: fullScope }
  if (kind === 'pdf') {
    const res =
      'id' in target
        ? await api.GET('/v1/audits/{id}/report.pdf', { params: { path: { id: target.id }, query }, parseAs: 'blob' })
        : await api.GET('/v1/reports/{slug}/report.pdf', { params: { path: { slug: target.slug }, query }, parseAs: 'blob' })
    return res.data ?? null
  }
  const res =
    'id' in target
      ? await api.GET('/v1/audits/{id}/report.json', { params: { path: { id: target.id }, query } })
      : await api.GET('/v1/reports/{slug}/report.json', { params: { path: { slug: target.slug }, query } })
  if (!res.data) return null
  return new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' })
}

function save(blob: Blob, name: string) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = name
  a.click()
  URL.revokeObjectURL(a.href)
}
