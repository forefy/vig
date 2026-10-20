import { Download, File, FileArchive, FileCode, FileTerminal, FileText, Film } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { components } from '@/api/client'

type Visual = components['schemas']['Visual']
type Kind = NonNullable<Visual['kind']>

const imageMimes = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']
const videoMimes = ['video/mp4', 'video/webm']

// How an attachment renders. Documents stored before kinds existed carry
// only a mime; it says the same thing.
export function visualKind(v: Pick<Visual, 'kind' | 'mime'>): Kind {
  if (v.kind) return v.kind
  const m = v.mime.toLowerCase()
  if (imageMimes.includes(m)) return 'image'
  if (videoMimes.includes(m)) return 'video'
  return 'file'
}

// A download's icon comes from what it is, so a script and an archive read
// differently on the chip before the name does.
function FileIcon({ mime, name, className }: { mime: string; name?: string; className?: string }) {
  const ext = (name ?? '').split('.').pop()?.toLowerCase() ?? ''
  const m = mime.toLowerCase()
  if (m.includes('zip') || m.includes('tar') || m.includes('gzip') || m.includes('compressed') || ['zip', 'tar', 'gz', 'tgz', '7z'].includes(ext)) return <FileArchive className={className} aria-hidden="true" />
  if (m === 'text/x-sh' || m === 'application/x-sh' || ['sh', 'bash', 'zsh'].includes(ext)) return <FileTerminal className={className} aria-hidden="true" />
  if (m.includes('json') || m.includes('javascript') || m.includes('python') || m.includes('rust') || m.includes('x-') || ['py', 'rs', 'ts', 'js', 'go', 'sol', 'json', 'toml', 'yaml', 'yml'].includes(ext)) return <FileCode className={className} aria-hidden="true" />
  if (m.startsWith('text/') || m === 'application/pdf' || ['txt', 'log', 'md', 'pdf', 'csv'].includes(ext)) return <FileText className={className} aria-hidden="true" />
  return <File className={className} aria-hidden="true" />
}

// What a tab or chip shows before an attachment's caption: nothing for a
// picture, a film mark for a recording, the file's own mark for a download.
export function VisualMark({ visual, className }: { visual: Visual; className?: string }) {
  const kind = visualKind(visual)
  if (kind === 'image') return null
  if (kind === 'video') return <Film className={cn('size-3 shrink-0', className)} aria-hidden="true" />
  return <FileIcon mime={visual.mime} name={visual.name} className={cn('size-3 shrink-0', className)} />
}

export function bytes(n: number | undefined): string {
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

// One attachment as the report shows it: a picture, a player that seeks
// (the route answers range requests), or a download chip carrying the name,
// the type and the size. The caption rides under all three.
export function VisualFigure({ visual, src, number, className }: { visual: Visual; src: string; number?: number; className?: string }) {
  const kind = visualKind(visual)
  return (
    <figure className={cn('flex flex-col gap-1', className)}>
      {number !== undefined && <span className="num w-fit rounded-sm border px-1.5 text-[11px] text-bad">{number}</span>}
      {kind === 'image' && (
        // Bounded like the player: a tall capture scrolls the page by a
        // screen at most, never by the whole image. Click opens it full size.
        <a href={src} target="_blank" rel="noreferrer" className="w-fit max-w-full">
          <img src={src} alt={visual.caption} loading="lazy" className="max-h-[60vh] w-auto max-w-full rounded-md border border-border object-contain" />
        </a>
      )}
      {kind === 'video' && (
        <video
          src={src}
          controls
          preload="metadata"
          playsInline
          aria-label={visual.caption}
          className="max-h-[60vh] w-full rounded-md border border-border bg-black"
        />
      )}
      {kind === 'file' && (
        <a
          href={src}
          download={visual.name || undefined}
          className="group flex w-fit max-w-full items-center gap-3 rounded-md border border-border bg-secondary/40 px-3 py-2 text-foreground hover:border-foreground/40 hover:bg-secondary"
        >
          <FileIcon mime={visual.mime} name={visual.name} className="size-5 shrink-0 text-muted-foreground group-hover:text-foreground" />
          <span className="flex min-w-0 flex-col">
            <span className="truncate font-mono text-[12.5px]">{visual.name || visual.id}</span>
            <span className="flex items-center gap-2 font-mono text-[10.5px] text-muted-foreground">
              <span>{visual.mime}</span>
              {visual.size ? <span className="num">{bytes(visual.size)}</span> : null}
            </span>
          </span>
          <Download className="size-3.5 shrink-0 text-muted-foreground group-hover:text-foreground" aria-hidden="true" />
        </a>
      )}
      <figcaption className="text-[11px] text-muted-foreground">{visual.caption}</figcaption>
    </figure>
  )
}
