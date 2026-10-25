import { describe, expect, it } from 'vitest'
import { nodeFacets } from './model'
import type { IndexAudit, IndexNode } from './model'

const audits = [{ id: 'a', target_id: 't', repo: 'org/site', adapter: 'webapp', state: 'delivered', head_sha: 'x', coverage: 'full', built_at: '2026-10-25T00:00:00Z', nodes: 2 }] as IndexAudit[]
const nodes = [
  { audit_id: 'a', id: 'host:h', kind: 'host', name: 'h', source: 'traffic', attention: 'observed', observed: 2 },
  { audit_id: 'a', id: 'route:h:/x', parent: 'host:h', kind: 'route', name: '/x', source: 'traffic', attention: 'finding', observed: 2, facts: { methods: 'GET POST' }, findings: [{ ordinal: 1, severity: 'high', status: 'open', title: 'x' }] },
] as IndexNode[]

describe('nodeFacets', () => {
  it('folds the surface by kind, attention, method and findings', () => {
    const f = Object.fromEntries(nodeFacets(nodes, audits).map((g) => [g.key, g.values.map((v) => `${v.value}:${v.n}`)]))
    expect(f.kind).toEqual(['host:1', 'route:1'])
    expect(f.attention).toEqual(['finding:1', 'observed:1'])
    expect(f.method).toEqual(['GET:1', 'POST:1'])
    expect(f.findings).toEqual(['yes:1', 'no:1'])
    expect(f.target).toBeUndefined()
  })
})
