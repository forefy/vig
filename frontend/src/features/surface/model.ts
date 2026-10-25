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
export const methodsOf = (n: IndexNode) => (n.facts?.methods ? n.facts.methods.split(' ') : [])
export const nodeText = (n: IndexNode) => `${n.name} ${n.id} ${Object.values(n.facts ?? {}).join(' ')}`

// The words of one audit's adapter, for its rows.
export const wordsFor = (audits: IndexAudit[]) => {
  const m = new Map(audits.map((a) => [a.id, chainWords(a.adapter)]))
  return (auditID: string) => m.get(auditID) ?? chainWords(undefined)
}

const attention = ['finding', 'reviewed', 'observed', 'unseen'] as const

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
  ].filter((g) => g.values.length > 1)
}
