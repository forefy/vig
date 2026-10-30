import { useSearchParams } from 'react-router'
import { hostOf, placeOf, routeOf, shortRoute } from '@/lib/web'
import { chainWords } from '@/lib/chain'
import type { ChainWords } from '@/lib/chain'
import { countFacet, matches, parseQuery } from '@/components/primitives/query'
import type { Facet as FacetOf, Query } from '@/components/primitives/query'
import type { components } from '@/api/client'

type Finding = components['schemas']['Finding']
type Report = components['schemas']['Report']

export const order = ['critical', 'high', 'medium', 'low', 'info'] as const

// A facet is one enum of the findings schema, with the values these
// findings actually carry. Hiding a value removes its findings from the
// page; the set of hidden values lives in the URL so a view can be shared.
export type Facet = FacetOf<Finding>

export function facetsOf(findings: Finding[], words: ChainWords): Facet[] {
  const facet = (key: string, label: string, get: (f: Finding) => string, word: (v: string) => string, orderOf?: readonly string[], ranked = false): Facet => countFacet(findings, key, label, get, word, orderOf, ranked)
  const statuses = ['open', 'acknowledged', 'fixed', 'disputed'] as const
  // Least to most privileged, so the chips read as a ladder.
  const exploit = ['anyone', 'any_user', 'any_signer', 'account_owner', 'api_key', 'internal_network', 'internal', 'admin', 'upgrade_authority', 'other_program', 'none']
  const short = (p: string) => p.slice(p.lastIndexOf('/') + 1)
  // A file chip shows the base name. When base names collide across the
  // report, the nearest directory that differs is shown beside it, so five
  // lib.rs chips read as five packages rather than one word five times.
  const files = [...new Set(findings.map((f) => placeOf(f.location).file).filter(Boolean))]
  const fileWord = (p: string) => {
    const base = short(p)
    const twins = files.filter((o) => o !== p && short(o) === base)
    if (twins.length === 0) return base
    const parts = p.split('/')
    for (let i = parts.length - 2; i >= 0; i--) {
      const seg = parts[i] ?? ''
      if (twins.every((o) => o.split('/')[i] !== seg)) return `${seg} · ${base}`
    }
    return p
  }
  return [
    facet('severity', 'severity', (f) => f.severity, (v) => v, order, true),
    facet('status', 'status', (f) => f.status, (v) => v, statuses),
    facet('exploitable', 'exploitable by', (f) => f.exploitable_by ?? 'unset', (v) => words.exploitable[v] ?? v, [...exploit, 'unset']),
    facet('evidence', 'evidence', (f) => f.evidence?.kind ?? 'none', (v) => (v === 'onchain' ? 'on-chain' : v), ['static', 'fuzz', 'onchain', 'bytecode', 'live', 'none']),
    facet('tool', 'tool', (f) => f.evidence?.tool ?? 'none', (v) => v),
    // A web finding's place is its endpoint: the host and the route are
    // what the reader folds by, with ids folded out of the route.
    facet(words.instruction, words.instruction, (f) => (words.web ? routeOf(placeOf(f.location).url) : f.location.instruction ?? 'none'), (v) => (words.web ? shortRoute(v) : v)),
    ...(words.web ? [facet('method', 'method', (f) => methodOf(f), (v) => v)] : []),
    facet('file', 'file', (f) => placeOf(f.location).file || 'none', (v) => (v === 'none' ? 'no source' : fileWord(v))),
    facet(words.program, words.program, (f) => (words.web ? hostOf(placeOf(f.location).url) : f.location.program_id ?? 'none'), (v) => (words.web || v.length <= 12 ? v : `${v.slice(0, 6)}…${v.slice(-4)}`)),
    facet('sites', 'sites', (f) => ((f.sites ?? []).length > 0 ? 'several' : 'one'), (v) => v, ['one', 'several']),
    facet('exploit path', 'exploit path', (f) => (f.exploit_path || f.exploit_path_withheld ? 'yes' : 'no'), (v) => v, ['yes', 'no']),
  ].filter((g) => g.values.length > 1)
}

export const findingText = (f: Finding) => `${f.title} ${f.description}`

function methodOf(f: Finding): string {
  const text = `${f.location.instruction ?? ''} ${f.sites?.[0]?.note ?? ''}`
  const m = /\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b/i.exec(text)
  return m ? (m[1] ?? '').toUpperCase() : 'none'
}

// inView is the page's list: the findings the query keeps, archived ones
// apart.
export function inView(all: Finding[], words: ChainWords, query: Query): Finding[] {
  const facets = facetsOf(all, words)
  return all.filter((f) => f.status !== 'archived' && matches(f, facets, query, findingText))
}

// useReportView reads the query from the URL the way the page does, so an
// export can take what is on screen.
export function useReportView(report: Report | undefined): { all: Finding[]; shown: Finding[]; filtered: boolean } {
  const [params] = useSearchParams()
  const query = parseQuery(params.get('q') ?? '')
  const all = report?.document.findings ?? []
  return { all, shown: inView(all, chainWords(report?.adapter), query), filtered: query.items.length > 0 }
}
