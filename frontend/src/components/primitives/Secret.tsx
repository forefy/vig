import { Copy } from './Copy'
import { cn } from '@/lib/utils'

// A value handed to a person or a machine: a key, a secret, a shell block.
// It never wraps a command mid-token; long single values wrap instead of
// scrolling. The copy mark sits in the corner and takes the whole value.
export function Secret({ value, lang = 'text', className }: { value: string; lang?: 'sh' | 'text'; className?: string }) {
  return (
    <div className={cn('relative min-w-0 rounded-md border border-border bg-background', className)}>
      <pre className={cn('p-3 pr-10 font-mono text-xs leading-[1.7] text-foreground', lang === 'sh' ? 'overflow-x-auto whitespace-pre' : 'break-all whitespace-pre-wrap')}>
        <code>{lang === 'sh' ? value.split('\n').map((l, i) => <span key={i} className="block">{shell(l)}</span>) : value}</code>
      </pre>
      <Copy value={value} iconOnly className="absolute top-2 right-2 rounded-sm p-1 hover:bg-accent" />
    </div>
  )
}

// One shell line: comment, command, export NAME=value, $VAR, "string".
const SH = /(#.*$)|("(?:[^"\\]|\\.)*"|'[^']*')|(\$\{?[A-Za-z_][A-Za-z0-9_:-]*\}?)|(^\s*[A-Za-z_][\w./-]*(?=\s|$))|(\b[A-Z_][A-Z0-9_]*(?==))|(\s+|.)/gm

function shell(line: string) {
  const out: React.ReactNode[] = []
  let m: RegExpExecArray | null
  let i = 0
  let seenCommand = false
  SH.lastIndex = 0
  while ((m = SH.exec(line)) !== null) {
    const [text, comment, str, variable, command, envName] = m
    let cls: string | undefined
    if (comment) cls = 'text-faint italic'
    else if (str) cls = 'text-ok/80'
    else if (variable) cls = 'text-warn/90'
    else if (command && !seenCommand) {
      cls = 'text-primary'
      seenCommand = true
    } else if (envName) cls = 'text-muted-foreground'
    out.push(cls ? <span key={i++} className={cls}>{text}</span> : text)
    if (m.index === SH.lastIndex) SH.lastIndex++
  }
  return out
}
