import { cn } from '@/lib/utils'
import { CodeSnippet } from './CodeSnippet'
import type { components } from '@/api/client'

type Code = components['schemas']['ReferenceCode']

// A reference link. One the server captured when the report was stored (a
// GitHub file at lines) shows those lines under it, read at the commit the
// link was pinned to; the link itself opens there. Any other link is the
// link, host stripped.
export function Reference({ url, code, className }: { url: string; code?: Code; className?: string }) {
  if (!code) {
    return (
      <a href={url} target="_blank" rel="noreferrer" className={cn('min-w-0 max-w-full truncate font-mono text-[11px] text-muted-foreground hover:text-foreground', className)}>
        {url.replace(/^https?:\/\//, '')}
      </a>
    )
  }
  const range = code.start === code.end ? `${code.start}` : `${code.start}-${code.end}`
  return (
    <div className={cn('flex min-w-0 flex-col gap-1', className)}>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <a href={code.pinned} target="_blank" rel="noreferrer" className="min-w-0 max-w-full break-all rounded-sm font-mono text-[11.5px] text-muted-foreground underline decoration-border underline-offset-2 hover:text-foreground hover:decoration-foreground">
          {code.repo}/{code.file}:{range}
        </a>
        <span className="num text-[10.5px] text-faint">{code.commit.slice(0, 7)}</span>
      </div>
      <CodeSnippet code={code.code.code} startLine={code.code.start_line} markFrom={code.start} markTo={code.end} />
    </div>
  )
}
