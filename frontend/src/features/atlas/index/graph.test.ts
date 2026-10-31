import { describe, expect, it } from 'vitest'
import { kindOf, labelOf, layout } from './graph'

const cols = { src: 'src_id', dst: 'dst_id', rel: 'rel' }
const e = (src: string, dst: string, rel = 'calls') => ({ src_id: src, dst_id: dst, rel })

describe('kindOf and labelOf', () => {
  it('reads the id vocabulary zz writes', () => {
    expect(kindOf('fn:acme/api:src/a.py:handler:12')).toBe('function')
    expect(labelOf('fn:acme/api:src/a.py:handler:12')).toEqual({ label: 'handler', place: 'a.py:12', detail: 'acme/api src/a.py:12' })
    expect(labelOf('fn:acme/api:src/a.js:<anon>:40').label).toBe('<anon>:40')
    expect(kindOf('acme/api:src/a.py:handler:12#cmd')).toBe('var')
    expect(labelOf('acme/api:src/a.py:handler:12#param:cmd').label).toBe('param:cmd')
    expect(labelOf('sink:acme/api:src/a.py:run:3#os.system( cmd )').label).toBe('os.system( cmd )')
    expect(labelOf('host:api.acme.com').label).toBe('api.acme.com')
    expect(labelOf('resource:arn:aws:s3:::acme-backups').label).toBe('acme-backups')
    expect(labelOf('literal:acme/api:src/conf/settings.py:9').label).toBe('settings.py:9')
    expect(labelOf('body:0123456789abcdef0123').label).toBe('0123456789ab')
  })
})

describe('layout', () => {
  it('puts a chain left to right and a sink last', () => {
    const g = layout([e('fn:r:a:h:1', 'fn:r:a:m:2'), e('fn:r:a:m:2', 'fn:r:a:s:3'), e('fn:r:a:s:3', 'sink:r:a:s:3#exec', 'sinks')], cols)
    const col = Object.fromEntries(g.nodes.map((n) => [n.label, n.col]))
    expect(col).toEqual({ h: 0, m: 1, s: 2, exec: 3 })
    expect(g.cols).toBe(4)
    expect(g.rows).toBe(1)
  })
  it('takes the longest chain for a node reached two ways', () => {
    const g = layout([e('fn:r:a:h:1', 'fn:r:a:x:2'), e('fn:r:a:x:2', 'fn:r:a:y:3'), e('fn:r:a:h:1', 'fn:r:a:y:3')], cols)
    expect(g.nodes.find((n) => n.label === 'y')!.col).toBe(2)
  })
  it('places a cycle and drops nothing under the cap', () => {
    const g = layout([e('host:a.com', 'body:1'), e('body:1', 'host:a.com'), e('body:1', 'repo:o/n')], cols)
    expect(g.nodes).toHaveLength(3)
    expect(new Set(g.nodes.map((n) => `${n.col}:${n.row}`)).size).toBe(3)
    expect(g.dropped).toBe(0)
  })
  it('stops adding nodes past the cap and says how many it left out', () => {
    const rows = Array.from({ length: 10 }, (_, i) => e(`fn:r:a:f${i}:1`, `fn:r:a:g${i}:1`))
    const g = layout(rows, cols, 6)
    expect(g.nodes).toHaveLength(6)
    expect(g.edges).toHaveLength(3)
    expect(g.dropped).toBe(14)
  })
})

describe('layout rows', () => {
  it('leaves no row empty in every column', () => {
    const rows = [
      ...Array.from({ length: 8 }, (_, i) => e(`fn:r:a:h${i}:1`, 'fn:r:a:mid:1')),
      e('fn:r:a:mid:1', 'fn:r:a:sink:1'),
      e('fn:r:a:lone:1', 'fn:r:a:other:1'),
    ]
    const g = layout(rows, cols)
    const usedRows = new Set(g.nodes.map((n) => n.row))
    expect(usedRows.size).toBe(g.rows)
  })
})

describe('layout confidence', () => {
  it('keeps an edge confidence when the result carries one', () => {
    const g = layout([{ ...e('fn:r:a:x:1', 'fn:r:a:y:1'), confidence: 0.6 }, e('fn:r:a:y:1', 'fn:r:a:z:1')], cols)
    expect(g.edges.map((x) => x.confidence)).toEqual([0.6, undefined])
  })
})
