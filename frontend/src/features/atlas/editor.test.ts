import { describe, expect, it } from 'vitest'
import { tokenize } from './SqlEditor'

describe('tokenize', () => {
  it('colours keywords, strings, numbers, calls and comments, and keeps the text whole', () => {
    const sql = "-- callers\nSELECT count(*) AS n FROM calls WHERE target = 'a''b' AND line > 10"
    const t = tokenize(sql)
    expect(t.map((x) => x.text).join('')).toBe(sql)
    const of = (kind: string) => t.filter((x) => x.kind === kind).map((x) => x.text)
    expect(of('comment')).toEqual(['-- callers'])
    expect(of('keyword')).toEqual(['SELECT', 'AS', 'FROM', 'WHERE', 'AND'])
    expect(of('function')).toEqual(['count'])
    expect(of('string')).toEqual(["'a''b'"])
    expect(of('number')).toEqual(['10'])
  })
  it('leaves names plain, in one run, and a keyword between them coloured', () => {
    const t = tokenize('calls c JOIN functions f ON f.fn_id = c.caller_fn')
    expect(t.map((x) => x.kind)).toEqual(['plain', 'keyword', 'plain', 'keyword', 'plain'])
    expect(t[0]!.text).toBe('calls c ')
    expect(t[1]!.text).toBe('JOIN')
    expect(t[4]!.text).toBe(' f.fn_id = c.caller_fn')
  })
})
