import { cn } from '@/lib/utils'
import { CodeSnippet, tint } from './CodeSnippet'
import type { components } from '@/api/client'

type Location = components['schemas']['Location']
type Snippet = components['schemas']['Snippet']

// What a fix fence can be shown against: the code the server captured for
// it, the finding's own snippets, and the pin that makes the location a
// link into the repository.
export type PatchContext = {
  sites: { location: Location; snippet?: Snippet | null }[]
  pin?: { repo: string; commit: string }
}

// Finding prose is plain text with light structure: blank lines separate
// paragraphs, lines starting with "- " are bullets, backticks mark code,
// a ``` fence holds a code block, and anything that looks like a source
// path is set as one. A fix is the same text in a stricter shape: every
// line a bullet, and a fence right after a bullet is the definite fix for
// it (the correct code, the exact header value), shown nested under it.
const pathRE = /((?:[\w.-]+\/)*[\w.-]+\.(?:sol|rs|ts|tsx|js|go|toml|json|md)(?::\d+(?:-\d+)?)?)/g
function inline(text: string, key: string) {
  return text.split(/(`[^`]+`)/g).flatMap((part, i) => {
    if (part.startsWith('`') && part.endsWith('`') && part.length > 2) {
      return [<code key={`${key}-${i}`} className="rounded-sm bg-secondary px-1 py-0.5 font-mono text-[12.5px] text-foreground">{part.slice(1, -1)}</code>]
    }
    return part.split(pathRE).map((seg, j) =>
      j % 2 === 1 ? <code key={`${key}-${i}-${j}`} className="rounded-sm bg-secondary px-1 py-0.5 font-mono text-[12.5px] text-foreground">{seg}</code> : seg,
    )
  })
}

type Block = { code: true; lang: string; text: string } | { code: false; lines: string[] }

// Mirrors core.proseBlocks: paragraphs split on blank lines, fences run to
// the closing ``` and keep their blank lines.
export function proseBlocks(text: string): Block[] {
  const out: Block[] = []
  let para: string[] = []
  const flush = () => {
    const lines = para.map((l) => l.trim()).filter(Boolean)
    if (lines.length) out.push({ code: false, lines })
    para = []
  }
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i] ?? ''
    if (l.trim().startsWith('```')) {
      flush()
      const lang = l.trim().slice(3).trim()
      const code: string[] = []
      for (i++; i < lines.length && (lines[i] ?? '').trim() !== '```'; i++) code.push(lines[i] ?? '')
      out.push({ code: true, lang, text: code.join('\n') })
      continue
    }
    if (l.trim() === '') { flush(); continue }
    para.push(l)
  }
  flush()
  return out
}

// A fence's info line: the language, then optionally where the code goes
// as path:line or path:start-end. Mirrors core.fenceLocation.
function fenceInfo(info: string): { lang: string; loc?: { file: string; start: number; end?: number } } {
  const [lang = '', place] = info.trim().split(/\s+/)
  const m = place ? /^([^\s:]+):(\d+)(?:-(\d+))?$/.exec(place) : null
  if (!m) return { lang }
  return { lang, loc: { file: m[1] ?? '', start: Number(m[2]), end: m[3] ? Number(m[3]) : undefined } }
}

function Code({ text, lang }: { text: string; lang: string }) {
  return (
    <pre data-lang={lang || undefined} className="overflow-x-auto rounded-md border border-border bg-background px-3 py-2 font-mono text-[12px] leading-[1.6] text-foreground whitespace-pre">
      {text.split('\n').map((l, i) => <span key={i} className="block">{tint(l)}</span>)}
    </pre>
  )
}

// A fence that names its place: the original lines from the pinned commit
// as removed, the fence as added, the location linking to those lines in
// the repository. Without captured code it is the fence alone under the link.
function Patch({ text, lang, loc, ctx }: { text: string; lang: string; loc: { file: string; start: number; end?: number }; ctx: PatchContext }) {
  const end = loc.end ?? loc.start
  const covering = ctx.sites.find((s) => s.snippet && s.location.file === loc.file && s.snippet.start_line <= loc.start && s.snippet.start_line + s.snippet.code.split('\n').length - 1 >= end)
  const label = `${loc.file}:${loc.start}${loc.end && loc.end !== loc.start ? `-${loc.end}` : ''}`
  const href = ctx.pin ? `https://github.com/${ctx.pin.repo}/blob/${ctx.pin.commit}/${loc.file}#L${loc.start}${loc.end && loc.end !== loc.start ? `-L${loc.end}` : ''}` : undefined
  return (
    <div className="flex flex-col gap-1">
      {href ? (
        <a href={href} target="_blank" rel="noreferrer" className="w-fit max-w-full break-all rounded-sm font-mono text-[11.5px] text-muted-foreground underline decoration-border underline-offset-2 hover:text-foreground hover:decoration-foreground">{label}</a>
      ) : (
        <span className="max-w-full break-all font-mono text-[11.5px] text-muted-foreground">{label}</span>
      )}
      {covering?.snippet ? (
        <CodeSnippet code={covering.snippet.code} startLine={covering.snippet.start_line} markFrom={loc.start} markTo={end} fix={text} mode={loc.end ? 'replace' : 'insert'} />
      ) : (
        <Code text={text} lang={lang} />
      )}
    </div>
  )
}

// bullets: render as one list where every prose line is an item and a code
// block nests under the item before it. The fix's shape; old reports whose
// fix is a plain sentence become one item.
export function Prose({ text, className, bullets, patches }: { text: string; className?: string; bullets?: boolean; patches?: PatchContext }) {
  const blocks = proseBlocks(text)
  const block = (lang: string, code: string, key: string | number) => {
    const info = fenceInfo(lang)
    return info.loc && patches ? <Patch key={key} text={code} lang={info.lang} loc={info.loc} ctx={patches} /> : <Code key={key} text={code} lang={info.lang} />
  }
  if (bullets) {
    const items: { text: string; code?: { lang: string; text: string } }[] = []
    for (const b of blocks) {
      if (b.code) {
        const last = items[items.length - 1]
        if (last && !last.code) last.code = { lang: b.lang, text: b.text }
        else items.push({ text: '', code: { lang: b.lang, text: b.text } })
        continue
      }
      for (const l of b.lines) items.push({ text: l.replace(/^- /, '') })
    }
    return (
      <ul className={cn('flex max-w-[76ch] flex-col gap-2 pl-4 text-[14px] leading-relaxed text-foreground/90', className)}>
        {items.map((it, i) => (
          <li key={i} className="list-disc marker:text-muted-foreground">
            <div className="flex flex-col gap-1.5">
              {it.text && <span>{inline(it.text, String(i))}</span>}
              {it.code && block(it.code.lang, it.code.text, 'c')}
            </div>
          </li>
        ))}
      </ul>
    )
  }
  return (
    <div className={cn('flex max-w-[68ch] flex-col gap-3 text-[14px] leading-relaxed text-foreground/90', className)}>
      {blocks.map((b, i) => {
        if (b.code) return block(b.lang, b.text, i)
        if (b.lines.every((l) => /^- /.test(l))) {
          return (
            <ul key={i} className="flex flex-col gap-1.5 pl-4">
              {b.lines.map((l, j) => <li key={j} className="list-disc marker:text-muted-foreground">{inline(l.slice(2), `${i}-${j}`)}</li>)}
            </ul>
          )
        }
        return <p key={i}>{inline(b.lines.join(' '), String(i))}</p>
      })}
    </div>
  )
}
