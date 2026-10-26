import type { components } from '@/api/client'
import { chainWords } from '@/lib/chain'
import { countFacet, countMultiFacet } from '@/components/primitives/query'
import type { Facet } from '@/components/primitives/query'

export type SurfaceIndex = components['schemas']['SurfaceIndex']
export type IndexNode = components['schemas']['IndexNode']
export type IndexAudit = components['schemas']['IndexAudit']

// A place is a row of the index; what hangs under it (an account, a
// parameter) rides on its row and opens with it.
export const PLACES = new Set(['program', 'host', 'instruction', 'route'])
export const UNDER = new Set(['account', 'param'])

export const key = (n: Pick<IndexNode, 'audit_id' | 'id'>) => `${n.audit_id}|${n.id}`
export const seen = (n: IndexNode) => n.observed > 0 || (n.ok ?? 0) + (n.failed ?? 0) > 0
// A fact that holds several values lists them with commas or spaces.
export const factValues = (v: string | undefined) => (v ? v.split(/[,\s]+/).filter(Boolean) : [])
export const methodsOf = (n: IndexNode) => factValues(n.facts?.methods)
export const nodeText = (n: IndexNode) => `${n.name} ${n.id} ${Object.values(n.facts ?? {}).join(' ')}`

// The words of one audit's adapter, for its rows.
export const wordsFor = (audits: IndexAudit[]) => {
  const m = new Map(audits.map((a) => [a.id, chainWords(a.adapter)]))
  return (auditID: string) => m.get(auditID) ?? chainWords(undefined)
}

const attention = ['finding', 'reviewed', 'observed', 'unseen'] as const

// Facts that name a thing rather than describe it, or that the fixed
// facets above already cover, are not offered as facets of their own;
// the text search still reaches them.
const NOT_A_FACET = new Set(['host', 'path', 'program', 'discriminator', 'example', 'file', 'line', 'args', 'accounts', 'methods', 'signer', 'writable', 'in'])

// Every other fact a mapper recorded becomes a facet, so what traffic
// says about a place (its auth, the statuses it answered, a parameter's
// value shape, whether the value came back) can be asked for as soon as
// it is recorded, with no list to keep here.
export function factFacets(nodes: IndexNode[]): Facet<IndexNode>[] {
  const keys = new Set<string>()
  for (const n of nodes) for (const k of Object.keys(n.facts ?? {})) if (!NOT_A_FACET.has(k)) keys.add(k)
  // A fact some rows carry and others lack is a split of its own, even
  // with one value (reflected = true), so those stay past the filter
  // below; a fact every row carries with one value says nothing.
  return [...keys].sort().flatMap((k) => {
    const present = nodes.filter((n) => n.facts?.[k] !== undefined)
    const facet = countMultiFacet(present, k, k, (n) => factValues(n.facts?.[k]), (v) => v)
    return facet.values.length > 1 || present.length < nodes.length ? [facet] : []
  })
}

export function nodeFacets(nodes: IndexNode[], audits: IndexAudit[]): Facet<IndexNode>[] {
  const repo = new Map(audits.map((a) => [a.id, a.repo]))
  const adapter = new Map(audits.map((a) => [a.id, a.adapter]))
  const yesno = (b: boolean) => (b ? 'yes' : 'no')
  return [
    countFacet(nodes, 'kind', 'kind', (n) => n.kind, (v) => v, ['host', 'route', 'param', 'program', 'instruction', 'account']),
    countFacet(nodes, 'attention', 'attention', (n) => n.attention, (v) => v, attention, true),
    countFacet(nodes, 'adapter', 'adapter', (n) => adapter.get(n.audit_id) ?? 'none', (v) => (v === 'webapp' ? 'web app' : v)),
    countFacet(nodes, 'target', 'target', (n) => repo.get(n.audit_id) ?? 'none', (v) => v.slice(v.indexOf('/') + 1)),
    countMultiFacet(nodes, 'method', 'method', methodsOf, (v) => v),
    countFacet(nodes, 'seen', 'seen', (n) => yesno(seen(n)), (v) => v, ['yes', 'no']),
    countFacet(nodes, 'findings', 'findings', (n) => yesno((n.findings ?? []).length > 0), (v) => v, ['yes', 'no']),
    countFacet(nodes.filter((n) => n.kind === 'account'), 'signer', 'signer', (n) => yesno(n.facts?.signer === 'true'), (v) => v, ['yes', 'no']),
    countFacet(nodes.filter((n) => n.kind === 'account'), 'writable', 'writable', (n) => yesno(n.facts?.writable === 'true'), (v) => v, ['yes', 'no']),
    countFacet(nodes.filter((n) => n.kind === 'param'), 'in', 'in', (n) => n.facts?.in ?? 'none', (v) => v, ['path', 'query', 'body']),
  ].filter((g) => g.values.length > 1).concat(factFacets(nodes))
}
