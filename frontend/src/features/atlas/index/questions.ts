// The saved questions: SQL files with a `-- key: value` header, the same
// format zz keeps them in (title, description, view, param), and the same
// substitution: a str param becomes a quoted literal with '' doubling, an
// int is checked and placed verbatim, raw is placed as written. A
// placeholder with no declared param is a required str.
import target_overview from './questions/target_overview.sql?raw'
import findings_by_repo from './questions/findings_by_repo.sql?raw'
import code_mentions_hosts from './questions/code_mentions_hosts.sql?raw'
import code_mentions_resources from './questions/code_mentions_resources.sql?raw'
import public_resources from './questions/public_resources.sql?raw'
import subdomains_to_cloud from './questions/subdomains_to_cloud.sql?raw'
import dangling_cnames from './questions/dangling_cnames.sql?raw'
import har_hosts from './questions/har_hosts.sql?raw'
import bodies_from_repos from './questions/bodies_from_repos.sql?raw'
import handlers_to_sinks from './questions/handlers_to_sinks.sql?raw'
import neighbors from './questions/neighbors.sql?raw'
import handler_sink_paths from './questions/handler_sink_paths.sql?raw'
import host_repo_paths from './questions/host_repo_paths.sql?raw'
import code_resource_subdomain from './questions/code_resource_subdomain.sql?raw'

export type ParamType = 'str' | 'int' | 'raw'
export type Param = { name: string; default: string | null; type: ParamType }
export type Question = { name: string; title: string; description: string; view: 'table' | 'graph'; params: Param[]; body: string }

const PARAM_TYPES = new Set<string>(['str', 'int', 'raw'])
const PLACEHOLDER_RE = /\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g
const HEADER_RE = /^\s*--\s*(title|description|view|param|kind)\s*:\s*(.*?)\s*$/i

export function parseQuestion(name: string, text: string): Question {
  const q: Question = { name, title: name, description: '', view: 'table', params: [], body: '' }
  const body: string[] = []
  let inHeader = true
  for (const line of text.split(/\r?\n/)) {
    const m = inHeader ? HEADER_RE.exec(line) : null
    if (m) {
      const key = m[1]!.toLowerCase()
      const val = m[2]!
      if (key === 'title') q.title = val
      else if (key === 'description') q.description = `${q.description} ${val}`.trim()
      else if (key === 'view') q.view = val.trim().toLowerCase() === 'graph' ? 'graph' : 'table'
      else if (key === 'param') {
        const toks = shellSplit(val)
        if (toks.length) {
          const p: Param = { name: toks[0]!, default: null, type: 'str' }
          const rest = toks.slice(1)
          const last = rest[rest.length - 1]?.toLowerCase()
          if (last && PARAM_TYPES.has(last)) {
            p.type = last as ParamType
            rest.pop()
          }
          if (rest.length) p.default = rest.join(' ')
          q.params.push(p)
        }
      }
      continue
    }
    if (inHeader && (!line.trim() || line.trim().startsWith('--'))) {
      // A free comment opens the body; blank lines after it are kept.
      if (line.trim().startsWith('--') || body.length) body.push(line)
      continue
    }
    inHeader = false
    body.push(line)
  }
  q.body = body.join('\n').trim()
  const declared = new Set(q.params.map((p) => p.name))
  for (const m of q.body.matchAll(PLACEHOLDER_RE)) {
    const ph = m[1]!
    if (!declared.has(ph)) {
      declared.add(ph)
      q.params.push({ name: ph, default: null, type: 'str' })
    }
  }
  return q
}

// shlex.split: whitespace separates, quotes group, a backslash escapes.
export function shellSplit(s: string): string[] {
  const out: string[] = []
  let cur = ''
  let has = false
  let q: string | null = null
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]!
    if (q === "'") {
      if (ch === "'") q = null
      else cur += ch
      continue
    }
    if (q === '"') {
      if (ch === '"') q = null
      else if (ch === '\\' && i + 1 < s.length && '"\\$`\n'.includes(s[i + 1]!)) cur += s[++i]
      else cur += ch
      continue
    }
    if (ch === '\\' && i + 1 < s.length) {
      cur += s[++i]
      has = true
      continue
    }
    if (ch === "'" || ch === '"') {
      q = ch
      has = true
      continue
    }
    if (/\s/.test(ch)) {
      if (has) out.push(cur)
      cur = ''
      has = false
      continue
    }
    cur += ch
    has = true
  }
  if (has) out.push(cur)
  return out
}

export const sqlLiteral = (v: unknown): string => (v === null || v === undefined ? 'NULL' : `'${String(v).replace(/'/g, "''")}'`)

export class RenderError extends Error {}

export function renderQuestion(body: string, params: Param[], values: Record<string, string>): string {
  const byName = new Map(params.map((p) => [p.name, p]))
  const names = new Set<string>()
  for (const m of body.matchAll(PLACEHOLDER_RE)) names.add(m[1]!)
  const missing: string[] = []
  let out = body
  for (const ph of names) {
    const p = byName.get(ph) ?? { name: ph, default: null, type: 'str' as ParamType }
    const raw = values[ph]
    const val = raw !== undefined && raw !== '' ? raw : p.default
    if (val === null || val === undefined) {
      missing.push(ph)
      continue
    }
    let rep: string
    if (p.type === 'int') {
      const s = String(val).trim()
      if (!/^[+-]?\d+$/.test(s)) throw new RenderError(`${ph}: integer, got ${JSON.stringify(val)}`)
      rep = String(parseInt(s, 10))
    } else if (p.type === 'raw') rep = String(val)
    else rep = sqlLiteral(val)
    const re = new RegExp(`\\{\\{\\s*${ph.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\}\\}`, 'g')
    out = out.replace(re, () => rep)
  }
  if (missing.length) throw new RenderError(`missing: ${missing.sort().join(', ')}`)
  return out
}

// Order is the rail's order: graph questions first, then lists.
const SOURCES: Record<string, string> = {
  handler_sink_paths,
  host_repo_paths,
  code_resource_subdomain,
  neighbors,
  handlers_to_sinks,
  findings_by_repo,
  code_mentions_hosts,
  code_mentions_resources,
  public_resources,
  subdomains_to_cloud,
  dangling_cnames,
  har_hosts,
  bodies_from_repos,
  target_overview,
}

export const QUESTIONS: Question[] = Object.entries(SOURCES).map(([name, text]) => parseQuestion(name, text))
