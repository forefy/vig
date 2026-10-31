import { describe, expect, it } from 'vitest'
import { QUESTIONS, RenderError, parseQuestion, renderQuestion, shellSplit } from './questions'
import { splitStatements } from './db'
import { cellLink, pathColumns } from './cells'

const text = `-- title: Hosts in code
-- description: String literals that name a host,
-- description: joined to the repo.
-- view: table
-- param: target
-- param: host_like % str
-- param: max_hops 4 int
-- param: sink_pattern (exec|yaml\\.load) str
-- param: note "two words" str
-- a free comment stays with the body

SELECT * FROM edges WHERE target = {{target}} AND dst_id LIKE 'host:' || {{ host_like }}
  AND hops <= {{max_hops}} AND x = {{undeclared}};
`

describe('parseQuestion', () => {
  const q = parseQuestion('code_hosts', text)
  it('reads the header', () => {
    expect(q.name).toBe('code_hosts')
    expect(q.title).toBe('Hosts in code')
    expect(q.description).toBe('String literals that name a host, joined to the repo.')
    expect(q.view).toBe('table')
  })
  it('reads params as name, default, type, like shlex', () => {
    expect(q.params.map((p) => [p.name, p.default, p.type])).toEqual([
      ['target', null, 'str'],
      ['host_like', '%', 'str'],
      ['max_hops', '4', 'int'],
      ['sink_pattern', '(exec|yaml.load)', 'str'],
      ['note', 'two words', 'str'],
      ['undeclared', null, 'str'],
    ])
  })
  it('keeps the body without header lines, free comments included', () => {
    expect(q.body.startsWith('-- a free comment stays with the body\n\nSELECT')).toBe(true)
    expect(q.body.endsWith(';')).toBe(true)
  })
  it('defaults the view to table and the title to the name', () => {
    const p = parseQuestion('x', '-- view: graph\nSELECT 1')
    expect(p.view).toBe('graph')
    expect(p.title).toBe('x')
    expect(parseQuestion('y', 'SELECT 1').view).toBe('table')
  })
})

describe('shellSplit', () => {
  it('splits on whitespace, groups quotes, honours backslashes', () => {
    expect(shellSplit('a  b\tc')).toEqual(['a', 'b', 'c'])
    expect(shellSplit(`name 'two words' str`)).toEqual(['name', 'two words', 'str'])
    expect(shellSplit(`name "a \\"q\\"" str`)).toEqual(['name', 'a "q"', 'str'])
    expect(shellSplit('p yaml\\.load')).toEqual(['p', 'yaml.load'])
    expect(shellSplit(`p ''`)).toEqual(['p', ''])
    expect(shellSplit('')).toEqual([])
  })
})

describe('renderQuestion', () => {
  const q = parseQuestion('q', text)
  it('quotes str params with doubled quotes, places int and raw verbatim', () => {
    const out = renderQuestion("SELECT {{s}}, {{n}}, {{r}}", [
      { name: 's', default: null, type: 'str' },
      { name: 'n', default: null, type: 'int' },
      { name: 'r', default: null, type: 'raw' },
    ], { s: "o'brien", n: ' 7 ', r: 'count(*) DESC' })
    expect(out).toBe("SELECT 'o''brien', 7, count(*) DESC")
  })
  it('uses the default when the value is empty and allows spaces inside the braces', () => {
    const out = renderQuestion(q.body, q.params, { target: 't', host_like: '', undeclared: 'u' })
    expect(out).toContain("target = 't'")
    expect(out).toContain("LIKE 'host:' || '%'")
    expect(out).toContain('hops <= 4')
    expect(out).toContain("x = 'u'")
    expect(out).not.toMatch(/\{\{/)
  })
  it('replaces every occurrence of a placeholder', () => {
    expect(renderQuestion('{{a}} {{a}}', [{ name: 'a', default: null, type: 'str' }], { a: 'x' })).toBe("'x' 'x'")
  })
  it('refuses a non-integer for an int param', () => {
    expect(() => renderQuestion('{{n}}', [{ name: 'n', default: null, type: 'int' }], { n: '4; DROP' })).toThrow(RenderError)
    expect(() => renderQuestion('{{n}}', [{ name: 'n', default: '4', type: 'int' }], {})).not.toThrow()
  })
  it('names every missing param, sorted', () => {
    expect(() => renderQuestion(q.body, q.params, {})).toThrow('missing: target, undeclared')
  })
  it('treats an undeclared placeholder as a required str', () => {
    expect(() => renderQuestion('{{z}}', [], {})).toThrow(RenderError)
    expect(renderQuestion('{{z}}', [], { z: 'v' })).toBe("'v'")
  })
})

describe('QUESTIONS', () => {
  it('ships fourteen questions, each over one target, none needing the graph extension', () => {
    expect(QUESTIONS.map((q) => q.name).sort()).toEqual([
      'bodies_from_repos', 'code_mentions_hosts', 'code_mentions_resources', 'code_resource_subdomain', 'dangling_cnames', 'findings_by_repo',
      'handler_sink_paths', 'handlers_to_sinks', 'har_hosts', 'host_repo_paths', 'neighbors', 'public_resources', 'subdomains_to_cloud', 'target_overview',
    ])
    for (const q of QUESTIONS) {
      expect(q.params.some((p) => p.name === 'target'), q.name).toBe(true)
      expect(q.body.toUpperCase(), q.name).not.toContain('GRAPH_TABLE')
      expect(q.title, q.name).not.toBe(q.name)
    }
  })
  it('renders every question with only a target, except neighbors which needs a node', () => {
    for (const q of QUESTIONS) {
      const go = () => renderQuestion(q.body, q.params, { target: '0199' })
      if (q.name === 'neighbors') expect(go).toThrow('missing: node')
      else expect(go(), q.name).not.toMatch(/\{\{/)
    }
  })
  it('gives every graph question the edge columns the graph draws from', () => {
    for (const q of QUESTIONS.filter((x) => x.view === 'graph')) {
      for (const col of ['src_id', 'dst_id', 'rel']) expect(q.body, `${q.name} ${col}`).toContain(col)
    }
  })
  it('bounds the walks to six hops', () => {
    for (const n of ['handlers_to_sinks', 'handler_sink_paths', 'neighbors']) {
      const q = QUESTIONS.find((x) => x.name === n)!
      expect(q.body).toContain('WITH RECURSIVE')
      expect(q.body).toMatch(/least\(\{\{(max_hops|hops)\}\}, 6\)/)
      expect(q.body).toContain('NOT list_contains(w.seen')
    }
  })
})

describe('splitStatements', () => {
  it('splits on semicolons outside quotes and comments', () => {
    expect(splitStatements("SET a=1; SELECT ';' -- x; y\n, \"a;b\"; ")).toEqual(['SET a=1', `SELECT ';' -- x; y\n, "a;b"`])
    expect(splitStatements('SELECT 1')).toEqual(['SELECT 1'])
  })
})

describe('cells', () => {
  it('links repos to github and names to https, by column or by id prefix', () => {
    expect(cellLink('repo', 'acme/site')).toBe('https://github.com/acme/site')
    expect(cellLink('dst_id', 'repo:acme/site')).toBe('https://github.com/acme/site')
    expect(cellLink('host', 'api.acme.com')).toBe('https://api.acme.com')
    expect(cellLink('src_id', 'subdomain:cdn.acme.com')).toBe('https://cdn.acme.com')
    expect(cellLink('evidence', 'api.acme.com')).toBeNull()
    expect(cellLink('repo', 'not a repo')).toBeNull()
  })
  it('sees a path list in src_id, dst_id and rel', () => {
    expect(pathColumns(['src_id', 'rel', 'dst_id', 'hops'])).toEqual({ src: 'src_id', dst: 'dst_id', rel: 'rel' })
    expect(pathColumns(['src', 'dst', 'rel'])).toBeNull()
  })
})
