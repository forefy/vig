import { useState } from 'react'
import { cn } from '@/lib/utils'

// The enclosing item a finding points at, from the pinned commit. Everything
// outside the located lines (plus two of context) is folded the way an editor
// folds a block: the first and last lines of the item stay, a fold row says
// how many lines it hides, a click opens it. Line numbers in the gutter, the
// located range marked, a light Rust tint. Nothing else is styled.
// mode: 'mark' (default) marks the located lines; 'replace' shows them as
// removed (-) with the fix added (+) after them; 'insert' leaves them plain
// and adds the fix (+) before markFrom.
export function CodeSnippet({ code, startLine, markFrom, markTo, fix, fixAt, mode = 'mark', className }: { code: string; startLine: number; markFrom: number; markTo?: number; fix?: string; fixAt?: number; mode?: 'mark' | 'replace' | 'insert'; className?: string }) {
  const lines = code.replace(/\r\n/g, '\n').split('\n')
  const to = markTo && markTo >= markFrom ? markTo : markFrom
  const last = startLine + lines.length - 1
  const width = String(last).length
  const [opened, setOpened] = useState<Set<number>>(() => new Set())

  // Visible: the first and last line, the marked range with two lines of
  // context, and any fold the reader opened.
  const keep = (n: number) => n === startLine || n === last || (n >= markFrom - 2 && n <= to + 2)
  // Rendered before this line when set, otherwise after the located lines.
  const anchor = mode === 'insert' ? markFrom : fixAt && fixAt >= startLine && fixAt <= last ? fixAt : null
  const inRange = (n: number) => n >= markFrom && n <= to
  const rows: React.ReactNode[] = []
  let i = 0
  while (i < lines.length) {
    const n = startLine + i
    if (keep(n) || opened.has(n)) {
      // A recommendation that guards the located lines has to be read before
      // them; one that replaces them reads after. fix_at says which, and
      // without it the code would be spliced into the middle of a call.
      if (fix && anchor === n) rows.push(...fixLines(fix, width, n))
      rows.push(<Line key={n} n={n} width={width} text={lines[i] ?? ''} marked={mode !== 'insert' && inRange(n)} removed={mode === 'replace' && inRange(n)} />)
      if (fix && anchor === null && n === to) rows.push(...fixLines(fix, width, n))
      i++
      continue
    }
    let j = i
    while (j < lines.length && !keep(startLine + j) && !opened.has(startLine + j)) j++
    const count = j - i
    const from = n
    if (count <= 1) {
      rows.push(<Line key={n} n={n} width={width} text={lines[i] ?? ''} marked={false} />)
      i++
      continue
    }
    rows.push(
      <button
        key={`fold-${from}`}
        type="button"
        onClick={() => setOpened((o) => { const nx = new Set(o); for (let k = from; k < from + count; k++) nx.add(k); return nx })}
        className="flex w-full items-center text-left hover:bg-secondary/60"
        aria-label={`Show ${count} folded lines`}
      >
        <span className="num w-[calc(var(--w)*1ch+2.25rem)] shrink-0 pr-3 text-right text-faint" style={{ ['--w' as string]: width }}>
          ⋯
        </span>
        <span className="num border-l-2 border-transparent pl-3 text-[11px] text-faint">{count} lines</span>
      </button>,
    )
    i = j
  }
  return (
    <pre className={cn('overflow-x-auto rounded-md border border-border bg-background font-mono text-[12px] leading-[1.6]', className)}>
      <code className="block min-w-max py-2">{rows}</code>
    </pre>
  )
}

function fixLines(fix: string, width: number, at: number) {
  return fix
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((t, k) => <Line key={`fix-${at}-${k}`} n={null} width={width} text={t} marked={false} added />)
}

// A removed line keeps its number and takes a "-" at the border; an added
// line has no number and takes a "+". That is the whole diff grammar.
function Line({ n, width, text, marked, added, removed }: { n: number | null; width: number; text: string; marked: boolean; added?: boolean; removed?: boolean }) {
  return (
    <span className={cn('flex', marked && 'bg-bad-soft/40', added && 'bg-ok-soft/40')}>
      <span
        className={cn('num w-[calc(var(--w)*1ch+2.25rem)] shrink-0 select-none pr-3 text-right', added ? 'text-ok' : marked ? 'text-bad' : 'text-faint')}
        style={{ ['--w' as string]: width }}
      >
        {added ? '+' : n}
      </span>
      <span className={cn('relative pr-5 pl-3 border-l-2', added ? 'border-ok' : marked ? 'border-bad' : 'border-transparent')}>
        {removed && <span className="num absolute -left-[1.1rem] select-none text-bad" aria-hidden="true">-</span>}
        {tint(text)}
      </span>
    </span>
  )
}

const KEYWORDS = new Set(['pub', 'fn', 'let', 'mut', 'use', 'mod', 'struct', 'enum', 'impl', 'for', 'in', 'if', 'else', 'match', 'return', 'ref', 'self', 'Self', 'where', 'as', 'const', 'static', 'trait', 'type', 'unsafe', 'loop', 'while', 'break', 'continue', 'true', 'false', 'crate', 'super', 'async', 'await', 'dyn', 'move'])
const TOKEN = /(\/\/.*$)|("(?:[^"\\]|\\.)*")|(#!?\[[^\]]*\])|(\b[a-z_][a-z0-9_]*!)|(\b[A-Z][A-Za-z0-9_]*\b)|(\b[a-z_][a-z0-9_]*\b)|(\b\d[\d_]*\b)|(\s+|.)/gm

export function tint(line: string) {
  const out: React.ReactNode[] = []
  let m: RegExpExecArray | null
  let i = 0
  TOKEN.lastIndex = 0
  while ((m = TOKEN.exec(line)) !== null) {
    const [text, comment, str, attr, macro, typ, ident, num] = m
    let cls: string | undefined
    if (comment) cls = 'text-faint italic'
    else if (str) cls = 'text-ok/80'
    else if (attr) cls = 'text-muted-foreground'
    else if (macro) cls = 'text-warn/90'
    else if (typ) cls = 'text-foreground'
    else if (ident && KEYWORDS.has(ident)) cls = 'text-primary/90'
    else if (num) cls = 'text-warn/90'
    out.push(cls ? <span key={i++} className={cls}>{text}</span> : text)
    if (m.index === TOKEN.lastIndex) TOKEN.lastIndex++
  }
  return out
}
