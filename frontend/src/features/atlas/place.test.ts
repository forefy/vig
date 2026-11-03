import { describe, expect, it } from 'vitest'
import { placeOf, sourceLink } from './Place'

describe('placeOf', () => {
  it('reads a function, a parameter and a return into the function span', () => {
    expect(placeOf('fn:acme/app:server/api.go:getConnected:17')).toMatchObject({ kind: 'code', repo: 'acme/app', path: 'server/api.go', from: 17, marks: [17], tone: 'ok', fn: 'acme/app:server/api.go:getConnected:17' })
    expect(placeOf('var:acme/app:server/api.go:getConnected:17#user_id')).toMatchObject({ kind: 'code', fn: 'acme/app:server/api.go:getConnected:17', flowsOf: 'var:acme/app:server/api.go:getConnected:17#user_id' })
    expect(placeOf('ret:acme/app:server/api.go:getConnected:17')).toMatchObject({ kind: 'code', fn: 'acme/app:server/api.go:getConnected:17', flowsOf: 'ret:acme/app:server/api.go:getConnected:17' })
    expect(placeOf('fn:acme/app:server/api.go:p.(*Plugin).get:9')).toMatchObject({ path: 'server/api.go', from: 9 })
    expect(placeOf('var:acme/app:server/api.go#errors')).toMatchObject({ kind: 'code', repo: 'acme/app', path: 'server/api.go', from: 0, flowsOf: 'var:acme/app:server/api.go#errors' })
  })
  it('reads a call, a sink and a literal into lines around them', () => {
    expect(placeOf('call:acme/app:server/api.go:35:4')).toMatchObject({ kind: 'code', repo: 'acme/app', path: 'server/api.go', from: 27, to: 43, marks: [35], tone: 'ok' })
    expect(placeOf('call:db.Query@server/db.go:5')).toMatchObject({ kind: 'code', path: 'server/db.go', from: 1, to: 13, marks: [5] })
    expect(placeOf('sink:exec.Command@server/run.go:41')).toMatchObject({ kind: 'code', path: 'server/run.go', marks: [41], tone: 'bad' })
    expect(placeOf('literal:acme/app:server/api.go:29')).toMatchObject({ kind: 'code', repo: 'acme/app', marks: [29] })
    expect(placeOf('sink:acme/app:f.go:h:1#acme/app:f.go:g:2#exec.Command')).toBeNull()
  })
  it('reads a capture entry and the rows of the other pillars', () => {
    expect(placeOf('entry:0a1b2c3d4e5f6071')).toEqual({ kind: 'entry', entry: '0a1b2c3d4e5f6071' })
    expect(placeOf('value:0a1b2c3d4e5f6071')).toEqual({ kind: 'edges', id: 'value:0a1b2c3d4e5f6071' })
    expect(placeOf('param:user_id')).toEqual({ kind: 'edges', id: 'param:user_id' })
    expect(placeOf('host:app.acme.test')).toEqual({ kind: 'row', table: 'hosts', column: 'host', value: 'app.acme.test' })
    expect(placeOf('resource:arn:aws:s3:::x')).toEqual({ kind: 'row', table: 'cloud_resources', column: 'rid', value: 'arn:aws:s3:::x' })
    expect(placeOf('nope')).toBeNull()
  })
})

describe('wire marks', () => {
  it('finds a value as itself and a name as a field', async () => {
    const { needleOf, markLines } = await import('./Place')
    const lines = ['GET /api/v1/users/usr_12345678?user_id=7', 'host: app.acme.test', '', '{"data":{"id":"usr_12345678","user_id":7}}']
    expect([...markLines(lines, needleOf('usr_12345678', false))]).toEqual([0, 3])
    expect([...markLines(lines, needleOf('user_id', true))]).toEqual([0, 3])
    expect([...markLines(lines, needleOf('id', true))]).toEqual([3])
    expect(markLines(lines, needleOf('acme.test', true)).size).toBe(0)
    expect([...markLines(['GET /models?slug=openai%2Fgpt-6', 'x'], needleOf('openai/gpt-6', false))]).toEqual([0])
  })
  it('cuts a long line to a window around the needle', async () => {
    const { needleOf, windowOf } = await import('./Place')
    const long = `${'x'.repeat(300)}NEEDLE${'y'.repeat(300)}`
    const w = windowOf(long, needleOf('NEEDLE', false), 20)
    expect(w).toBe(`…${'x'.repeat(20)}NEEDLE${'y'.repeat(20)}…`)
    expect(windowOf('short line', needleOf('short', false))).toBe('short line')
  })
})


describe('wire lines', () => {
  it('lays a query out one parameter per line, decoded', async () => {
    const { queryLines } = await import('./Place')
    expect(queryLines('slug=openai%2Fgpt-6&view=week&flag')).toEqual(['?slug = openai/gpt-6', '&view = week', '&flag'])
    expect(queryLines('')).toEqual([])
  })
  it('lays a JSON body out one leaf per line and a form one field per line', async () => {
    const { bodyLines, needleOf, markLines } = await import('./Place')
    const json = bodyLines('{"data":{"id":"usr_12345678","tags":["a","b"]}}', 'application/json')
    expect(json[0]).toBe('{')
    expect(json.some((l) => l.includes('"id": "usr_12345678"'))).toBe(true)
    expect([...markLines(json, needleOf('usr_12345678', false))].length).toBe(1)
    expect(bodyLines('a=1&b=x%20y', 'application/x-www-form-urlencoded')).toEqual(['a = 1', 'b = x y'])
    expect(bodyLines('<html>', 'text/html')).toEqual(['<html>'])
    expect(bodyLines('{not json', 'application/json')).toEqual(['{not json'])
  })
})

it('a call drawn at a path with an @ in it keeps the whole path', () => {
  const place = placeOf('call:render@webapp/node_modules/@scope/pkg/index.js:41')
  expect(place).toMatchObject({ kind: 'code', path: 'webapp/node_modules/@scope/pkg/index.js', marks: [41] })
})

it('a sink drawn with its repository opens that repository, not the first with the path', () => {
  const place = placeOf('sink:plugin.ClientMain@mattermost/mattermost-plugin-zoom:server/main.go:11')
  expect(place).toMatchObject({ kind: 'code', repo: 'mattermost/mattermost-plugin-zoom', path: 'server/main.go', marks: [11], tone: 'bad' })
})

describe('sourceLink', () => {
  it('opens the marked lines on GitHub at the indexed commit', () => {
    expect(sourceLink('mattermost/mattermost-plugin-github', '2b9840c1d2e3', 'server/plugin/api.go', 592)).toBe('https://github.com/mattermost/mattermost-plugin-github/blob/2b9840c1d2e3/server/plugin/api.go#L592')
    expect(sourceLink('acme/app', 'abcdef1', 'web/@scope/a b.ts', 5, 9)).toBe('https://github.com/acme/app/blob/abcdef1/web/%40scope/a%20b.ts#L5-L9')
  })
  it('has none for a site, a missing commit or no path', () => {
    expect(sourceLink('https://openrouter.ai', 'abcdef1', 'x', 1)).toBeNull()
    expect(sourceLink('acme/app', '', 'x', 1)).toBeNull()
    expect(sourceLink('acme/app', 'main', 'x', 1)).toBeNull()
    expect(sourceLink('acme/app', 'abcdef1', '', 1)).toBeNull()
  })
})
