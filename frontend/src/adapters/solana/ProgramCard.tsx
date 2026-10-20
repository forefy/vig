import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useWallet } from '@solana/wallet-adapter-react'
import bs58 from 'bs58'
import { X } from 'lucide-react'
import { api } from '@/api/client'
import { Panel, panelAction, Copy, StateBadge } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { WalletPicker } from '@/features/auth/wallet'
import { short } from '@/lib/utils'
import { toast } from 'sonner'
import type { components } from '@/api/client'

type Program = components['schemas']['Program']

// One fetched program: its panel, and the ownership proof action.
export function ProgramCard({ targetId, program, onChange, onRemove }: { targetId: string; program: Program; onChange: (p: Program) => void; onRemove: () => void }) {
  const wallet = useWallet()
  const qc = useQueryClient()
  const [pick, setPick] = useState(false)
  const [proving, setProving] = useState(false)

  async function prove() {
    if (!wallet.connected || !wallet.signMessage || !wallet.publicKey) {
      setPick(true)
      return
    }
    if (program.upgrade_authority && wallet.publicKey.toBase58() !== program.upgrade_authority) {
      toast.error(`Connect the upgrade authority ${short(program.upgrade_authority, 4, 4)}`)
      return
    }
    setProving(true)
    try {
      const n = await api.POST('/v1/targets/{id}/programs/{pid}/ownership/nonce', { params: { path: { id: targetId, pid: program.id } } })
      if (!n.data) throw new Error('nonce')
      const sig = await wallet.signMessage(new TextEncoder().encode(n.data.message))
      const v = await api.POST('/v1/targets/{id}/programs/{pid}/ownership/verify', { params: { path: { id: targetId, pid: program.id } }, body: { signature: bs58.encode(sig) } })
      if (!v.data) throw new Error('verify')
      onChange(v.data)
      void qc.invalidateQueries({ queryKey: ['get', '/v1/targets/{id}'] })
    } catch {
      toast.error('Ownership proof did not complete')
    } finally {
      setProving(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <Copy value={program.id} className="text-[12.5px] text-foreground">{short(program.id, 8, 8)}</Copy>
        <Button size="icon-xs" variant="ghost" onClick={onRemove} aria-label="Remove program">
          <X className="size-3.5" />
        </Button>
      </div>
      <Panel
        panel={program.panel}
        onAction={(action, row) => {
          if (action === 'prove_ownership') void prove()
          else void panelAction(action, row)
        }}
      />
      {proving && <StateBadge state="pending">wallet</StateBadge>}
      <WalletPicker open={pick} onOpenChange={setPick} onPicked={async (w) => { try { await w.adapter.connect(); void prove() } catch { /* closed */ } }} />
    </div>
  )
}
