import { X } from 'lucide-react'
import { Panel, panelAction, Copy } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { short } from '@/lib/utils'
import type { components } from '@/api/client'

type Program = components['schemas']['Program']

// One fetched contract: its address and its panel. There is no ownership
// proof on the EVM side; the panel's rows are the whole story.
export function ContractCard({ program, onRemove }: { program: Program; onRemove: () => void }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <Copy value={program.id} className="text-[12.5px] text-foreground">{short(program.id, 8, 6)}</Copy>
        <Button size="icon-xs" variant="ghost" onClick={onRemove} aria-label="Remove contract">
          <X className="size-3.5" />
        </Button>
      </div>
      <Panel panel={program.panel} onAction={(action, row) => void panelAction(action, row)} />
    </div>
  )
}
