import { useId } from 'react'
import { cn } from '@/lib/utils'

// The SQL box: a textarea the reader types into, drawn over the same text
// set in colour. The two share one font and one line height, so the caret
// sits on the coloured glyphs; the coloured copy sets the height and the
// width, the textarea covers all of it, and the box scrolls as one when
// the text runs long or wide, so a caret far to the right is still over
// its own glyphs. Lines never wrap, so the numbers in the gutter are the
// lines of the query.

export type Token = { kind: 'keyword' | 'function' | 'string' | 'number' | 'comment' | 'plain'; text: string }

const KEYWORDS = new Set(
  (
    'select from where and or not as with recursive group by order limit offset join on left right inner outer full cross union all distinct ' +
    'case when then else end is null in like ilike having desc asc exists between using over partition window values insert update delete ' +
    'create table view true false cast interval array struct qualify sample pivot unpivot except intersect describe show'
  ).split(' '),
)

const TOKEN_RE = /(--[^\n]*)|('(?:[^']|'')*'?)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_.]*)(\s*\()?|([\s\S])/g

// tokenize splits SQL into runs the editor colours: comments, strings,
// numbers, keywords and function names. Everything else stays plain.
export function tokenize(sql: string): Token[] {
  const out: Token[] = []
  const push = (kind: Token['kind'], text: string) => {
    const last = out[out.length - 1]
    if (last && last.kind === kind && (kind === 'plain' || kind === 'comment')) last.text += text
    else out.push({ kind, text })
  }
  for (const m of sql.matchAll(TOKEN_RE)) {
    if (m[1] !== undefined) push('comment', m[1])
    else if (m[2] !== undefined) push('string', m[2])
    else if (m[3] !== undefined) push('number', m[3])
    else if (m[4] !== undefined) {
      const word = m[4]
      if (KEYWORDS.has(word.toLowerCase())) push('keyword', word)
      else if (m[5] !== undefined) push('function', word)
      else push('plain', word)
      if (m[5] !== undefined) push('plain', m[5])
    } else push('plain', m[6] ?? '')
  }
  return out
}

const ink: Record<Token['kind'], string> = {
  keyword: 'text-primary',
  function: 'text-foreground',
  string: 'text-warn',
  number: 'text-sev-low',
  comment: 'text-faint italic',
  plain: 'text-foreground/85',
}

export function SqlEditor({ value, onChange, onRun, className }: { value: string; onChange: (v: string) => void; onRun: () => void; className?: string }) {
  const id = useId()
  // A trailing newline needs a line of its own to keep the caret inside.
  const shown = value.endsWith('\n') ? value + ' ' : value || ' '
  const lines = shown.split('\n').length
  return (
    <div className={cn('max-h-[34vh] overflow-auto bg-background', className)}>
      <div className="relative grid grid-cols-[auto_minmax(0,1fr)]">
        <div aria-hidden="true" className="sticky left-0 z-[1] border-r border-border bg-background py-2.5 pr-2 pl-3 text-right font-mono text-[12.5px] leading-relaxed text-faint select-none">
          {Array.from({ length: lines }, (_, i) => (
            <div key={i}>{i + 1}</div>
          ))}
        </div>
        <div className="relative w-max min-w-full">
          <pre aria-hidden="true" className="m-0 px-3 py-2.5 pr-6 font-mono text-[12.5px] leading-relaxed whitespace-pre">
            {tokenize(shown).map((t, i) => (
              <span key={i} className={ink[t.kind]}>
                {t.text}
              </span>
            ))}
          </pre>
          <textarea
            id={id}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                e.preventDefault()
                onRun()
              }
            }}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            wrap="off"
            aria-label="SQL"
            className="absolute inset-0 m-0 h-full w-full resize-none overflow-hidden border-0 bg-transparent px-3 py-2.5 pr-6 font-mono text-[12.5px] leading-relaxed whitespace-pre text-transparent caret-foreground outline-none selection:bg-primary/25"
          />
        </div>
      </div>
    </div>
  )
}
