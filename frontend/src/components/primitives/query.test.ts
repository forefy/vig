import { describe, expect, it } from 'vitest'
import { countFacet, countMultiFacet, formatQuery, matches, parseQuery } from './query'

type Row = { kind: string; name: string; methods: string[] }
const rows: Row[] = [
  { kind: 'route', name: '/login', methods: ['GET', 'POST'] },
  { kind: 'route', name: '/me', methods: ['GET', 'PATCH'] },
  { kind: 'instruction', name: 'transfer', methods: [] },
]
const facets = [countFacet(rows, 'kind', 'kind', (r) => r.kind, (v) => v), countMultiFacet(rows, 'method', 'method', (r) => r.methods, (v) => v)]
const text = (r: Row) => r.name

describe('query', () => {
  it('round-trips through the url form', () => {
    const q = parseQuery('kind=route,method!=POST|kind=instruction')
    expect(q.items).toHaveLength(3)
    expect(q.conns).toEqual(['and', 'or'])
    expect(formatQuery(q)).toBe('kind=route,method!=POST|kind=instruction')
  })
  it('folds by a single value and by any of several', () => {
    const q = parseQuery('kind=route,method=POST')
    expect(rows.filter((r) => matches(r, facets, q, text)).map((r) => r.name)).toEqual(['/login'])
    const n = parseQuery('method!=POST')
    expect(rows.filter((r) => matches(r, facets, n, text)).map((r) => r.name)).toEqual(['/me', 'transfer'])
  })
  it('or splits the groups and ~ reads the text', () => {
    const q = parseQuery('kind=instruction|name~log')
    expect(rows.filter((r) => matches(r, facets, q, text)).map((r) => r.name)).toEqual(['/login', 'transfer'])
  })
  it('counts every value a row carries once', () => {
    expect(facets[1]?.values.map((v) => `${v.value}:${v.n}`)).toEqual(['GET:2', 'PATCH:1', 'POST:1'])
  })
})
