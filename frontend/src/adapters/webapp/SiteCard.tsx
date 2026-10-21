import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { api } from '@/api/client'
import { Panel, panelAction, Copy, StateBadge, Mark } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import type { components } from '@/api/client'

type Target = components['schemas']['Target']
type Nonce = { nonce: string; dns_name: string; value: string; file_url: string; expires_at: string }

const problem = (e: unknown) => (e as { detail?: string } | undefined)?.detail

// One live site: its origin, its intake panel, and the ownership proof.
// The proof is two places the nonce can go, shown as rows the owner copies
// from; Check asks the backend to look.
export function SiteCard({ target, onChange }: { target: Target; onChange: (t: Target) => void }) {
  const qc = useQueryClient()
  const site = target.site
  const [nonce, setNonce] = useState<Nonce | null>(null)
  const [busy, setBusy] = useState(false)
  if (!site) return null

  async function prove() {
    setBusy(true)
    const n = await api.POST('/v1/targets/{id}/ownership/nonce', { params: { path: { id: target.id } } })
    setBusy(false)
    if (!n.data) {
      toast.error(problem(n.error) ?? 'Could not issue a nonce')
      return
    }
    setNonce(n.data)
  }

  async function check() {
    setBusy(true)
    const v = await api.POST('/v1/targets/{id}/ownership/verify', { params: { path: { id: target.id } } })
    setBusy(false)
    if (!v.data) {
      toast.error(problem(v.error) ?? 'Not found')
      return
    }
    setNonce(null)
    onChange(v.data)
    void qc.invalidateQueries({ queryKey: ['get', '/v1/targets/{id}'] })
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <Copy value={site.origin} className="font-mono text-[12.5px] text-foreground">
          {site.origin}
        </Copy>
        {site.ownership_proven && <StateBadge state="ok">{site.ownership_method === 'dns_txt' ? 'dns' : 'well-known'}</StateBadge>}
      </div>
      <Panel
        panel={site.panel}
        onAction={(action, row) => {
          if (action === 'prove_ownership') void prove()
          else void panelAction(action, row)
        }}
      />
      {nonce && (
        <div className="flex flex-col gap-2 rounded-md border border-border px-3.5 py-3 font-mono text-xs">
          <div className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1.5">
            <span className="label-caps text-faint">txt</span>
            <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
              <Copy value={nonce.dns_name} className="break-all text-foreground">
                {nonce.dns_name}
              </Copy>
              <Copy value={nonce.value} className="break-all text-muted-foreground">
                {nonce.value}
              </Copy>
            </span>
            <span className="label-caps text-faint">file</span>
            <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
              <Copy value={nonce.file_url} className="break-all text-foreground">
                {nonce.file_url}
              </Copy>
              <Copy value={nonce.value} className="break-all text-muted-foreground">
                {nonce.value}
              </Copy>
            </span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <StateBadge state="neutral">either one</StateBadge>
            <Button size="sm" variant="outline" disabled={busy} onClick={() => void check()}>
              {busy ? <Mark size={14} className="animate-[spin_6s_linear_infinite]" /> : <RefreshCw className="size-3.5" />} Check
            </Button>
          </div>
        </div>
      )}
      {busy && !nonce && <StateBadge state="pending">nonce</StateBadge>}
    </div>
  )
}
