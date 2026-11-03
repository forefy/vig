import { describe, expect, it } from 'vitest'
import { cellText, csv, decimalOf, errorText, splitStatements, typeName, uniqueNames } from './db'
import { STARTERS, defaultSql } from './starters'

describe('starters', () => {
  it('opens on the functions, and no starter names a build', () => {
    expect(defaultSql).toContain('FROM functions')
    for (const s of STARTERS) {
      expect(s.sql, s.label).not.toMatch(/target\s*=\s*'/)
      expect(s.sql, s.label).not.toContain('{{')
      expect(s.sql.trimEnd(), s.label).toMatch(/LIMIT \d+;$/)
    }
  })
  it('keeps every join to one build', () => {
    for (const s of STARTERS) {
      for (const m of s.sql.matchAll(/JOIN\s+\S+(?:\s+\S+)?\s+ON\s+([^\n]+)/g)) {
        expect(m[1], `${s.label}: ${m[0]}`).toMatch(/\.target = \w+\.target/)
      }
    }
  })
})

describe('cells', () => {
  it('shows every value as text', () => {
    expect(cellText(null)).toBe('')
    expect(cellText('a')).toBe('a')
    expect(cellText(12n)).toBe('12')
    expect(cellText(true)).toBe('true')
    expect(cellText(new Date(Date.UTC(2026, 0, 2)))).toBe('2026-01-02T00:00:00.000Z')
    expect(cellText(['a', 'b'])).toBe('["a","b"]')
    expect(cellText({ toJSON: () => ({ k: 1n }) })).toBe('{"k":1}')
    expect(cellText(1793558609315, 'Timestamp<MILLISECOND>')).toBe('2026-11-01T18:43:29.315Z')
    expect(cellText(1793558609315n, 'Date64<MILLISECOND>')).toBe('2026-11-01T18:43:29.315Z')
    expect(cellText(12, 'Int64')).toBe('12')
  })
  it('writes a csv that quotes what needs it', () => {
    const out = csv({ columns: ['a', 'b'], types: ['VARCHAR', 'BIGINT'], rows: [{ a: 'x,y', b: 1n }, { a: 'say "hi"', b: null }], ms: 1 })
    expect(out).toBe('a,b\n"x,y",1\n"say ""hi""",')
  })
  it('strips the engine wrapper off an error and a block it repeats', () => {
    expect(errorText(new Error('Binder Error: Referenced column "nope" not found'))).toBe('Referenced column "nope" not found')
    expect(errorText(new Error('Parser Error: syntax error at or near "SELEC"\n\nLINE 1: SELEC 1;\n        ^\n\nLINE 1: SELEC 1;\n        ^'))).toBe('syntax error at or near "SELEC"\n\nLINE 1: SELEC 1;\n        ^')
    expect(errorText(new Error('Out of Memory Error: failed to allocate data of size 64.0 MiB (2.0 GiB/3.1 GiB used)\nDatabase is launched in in-memory mode and no temporary directory is specified.\nUnused blocks cannot be offloaded to disk.\n\nPossible solutions:\n* Reducing the number of threads (SET threads=X)'))).toBe('failed to allocate data of size 64.0 MiB (2.0 GiB/3.1 GiB used)')
    expect(errorText(new Error('Out of Memory Error: failed to allocate data of size 256.0 KiB (3.1 GiB/3.1 GiB used)\n\nDatabase is launched in in-memory mode and no temporary directory is specified.\n\nPossible solutions:\n* Reducing the number of threads (SET threads=X)'))).toBe('failed to allocate data of size 256.0 KiB (3.1 GiB/3.1 GiB used)')
  })
  it('cuts a box into statements outside quotes and comments', () => {
    expect(splitStatements("SELECT 1 AS a; SELECT ';' AS b; -- c; d\nSELECT 3 /* ; */ AS c;")).toEqual(['SELECT 1 AS a', "SELECT ';' AS b", '-- c; d\nSELECT 3 /* ; */ AS c'])
    expect(splitStatements('  ')).toEqual([])
  })
})

describe('results', () => {
  it('keeps every column of a result, a repeated name numbered', () => {
    expect(uniqueNames(['a', 'a', 'b', 'a'])).toEqual(['a', 'a_1', 'b', 'a_2'])
  })
  it('names a decimal type as SQL does', () => {
    expect(typeName({ precision: 10, scale: 2, toString: () => 'Decimal[10e+2]' })).toBe('DECIMAL(10,2)')
    expect(typeName({ toString: () => 'Utf8' })).toBe('Utf8')
  })
  it('reads a decimal as its value whatever Arrow hands over', () => {
    expect(decimalOf(15n, 1)).toBe(1.5)
    expect(decimalOf(375, 2)).toBe(3.75)
    expect(decimalOf('1234567891', 3)).toBe(1234567.891)
    expect(decimalOf(new Uint32Array([375, 0, 0, 0]), 2)).toBe(3.75)
    expect(decimalOf(new Uint32Array([0xfffffff1, 0xffffffff, 0xffffffff, 0xffffffff]), 1)).toBe(-1.5)
    expect(decimalOf(null, 2)).toBeNull()
  })
})
