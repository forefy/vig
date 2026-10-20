import { useState } from 'react'
import { ChevronRight, File, Folder } from 'lucide-react'
import { Checkbox } from '@/components/ui/checkbox'
import { cn, kb } from '@/lib/utils'
import type { components } from '@/api/client'

type Node = components['schemas']['TreeNode']

function collect(n: Node, acc: string[]) {
  if (!n.dir) acc.push(n.path)
  n.children?.forEach((c) => collect(c, acc))
  return acc
}

// The files the API marked selected, as a set. The read-only default.
export function autoSelection(nodes: Node[] | null | undefined): Set<string> {
  const sel = new Set<string>()
  const walk = (list: Node[]) => list.forEach((x) => { if (!x.dir && x.selected) sel.add(x.path); if (x.children) walk(x.children) })
  walk(nodes ?? [])
  return sel
}

// File tree with tri-state folders and a lines column. The selection is the
// audit scope, so it must be exact.
export function Tree({ nodes, selected, onChange, readOnly = false }: { nodes: Node[] | null | undefined; selected?: Set<string>; onChange?: (next: Set<string>) => void; readOnly?: boolean }) {
  const list = nodes ?? []
  const sel = selected ?? autoSelection(list)
  return (
    <div className="max-h-[520px] overflow-auto rounded-md border border-border bg-card">
      <ul className="py-1">
        {list.map((n) => (
          <Row key={n.path} node={n} depth={0} sel={sel} readOnly={readOnly} onToggle={(paths, on) => {
            if (readOnly || !onChange) return
            const next = new Set(sel)
            paths.forEach((p) => (on ? next.add(p) : next.delete(p)))
            onChange(next)
          }} />
        ))}
      </ul>
    </div>
  )
}

function Row({ node, depth, sel, readOnly, onToggle }: { node: Node; depth: number; sel: Set<string>; readOnly: boolean; onToggle: (paths: string[], on: boolean) => void }) {
  const [open, setOpen] = useState(depth < 2)
  const files = node.dir ? collect(node, []) : [node.path]
  const on = files.filter((f) => sel.has(f)).length
  const state: boolean | 'indeterminate' = on === 0 ? false : on === files.length ? true : 'indeterminate'
  const name = node.path.split('/').pop() ?? node.path
  return (
    <li>
      <div className={cn('grid grid-cols-[auto_auto_1fr_auto] items-center gap-2 py-1 pr-3 text-[13px] hover:bg-accent/60', state === true && !node.dir && 'bg-primary/[0.04]')} style={{ paddingLeft: 10 + depth * 18 }}>
        <Checkbox checked={state === true} indeterminate={state === 'indeterminate'} disabled={readOnly} onCheckedChange={(v) => onToggle(files, v === true)} aria-label={node.path} />
        {node.dir ? (
          <button
            type="button"
            onClick={() => setOpen(!open)}
            aria-expanded={open}
            aria-label={`${open ? 'Collapse' : 'Expand'} ${name}`}
            className="flex items-center gap-1 rounded-sm text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          >
            <ChevronRight className={cn('size-3.5', open && 'rotate-90')} />
            <Folder className="size-3.5" />
          </button>
        ) : (
          <span className="flex items-center gap-1 pl-[18px] text-faint">
            <File className="size-3.5" />
          </span>
        )}
        <span className={cn('truncate font-mono', node.dir ? 'text-foreground' : state ? 'text-foreground' : 'text-muted-foreground')}>{name}</span>
        <span className="num text-[11px] text-faint" title={!node.dir && node.lines ? `${node.lines} lines` : undefined}>{node.dir ? files.length : node.lines ? `${node.code ?? node.lines} ln` : kb(node.bytes)}</span>
      </div>
      {node.dir && open && node.children && (
        <ul>
          {node.children.map((c) => (
            <Row key={c.path} node={c} depth={depth + 1} sel={sel} readOnly={readOnly} onToggle={onToggle} />
          ))}
        </ul>
      )}
    </li>
  )
}

export { collect as collectFiles }
