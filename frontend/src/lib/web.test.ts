import { describe, expect, it } from 'vitest'
import { hostOf, routeOf, routeShape, shortRoute } from './web'

describe('routeShape', () => {
  it('folds ids the way the backend does', () => {
    expect(routeShape('/')).toBe('/')
    expect(routeShape('')).toBe('/')
    expect(routeShape('/users/42/orders/9f1c2d3e4a5b6c7d8e9f')).toBe('/users/{id}/orders/{id}')
    expect(routeShape('/api/v1/items/')).toBe('/api/v1/items')
    expect(routeShape('/u/3f2504e0-4f89-11d3-9a0c-0305e82c3301')).toBe('/u/{id}')
    expect(routeShape('/projects/{id}/mcp-servers')).toBe('/projects/{id}/mcp-servers')
    expect(routeShape('/agent-runtime/connectors/servicenow')).toBe('/agent-runtime/connectors/servicenow')
    expect(routeShape('/t/dGhpcy1pcy1hLXRva2VuMTIz')).toBe('/t/{id}')
    expect(routeShape('/some-very-long-static-segment-name')).toBe('/some-very-long-static-segment-name')
  })
})

describe('routeOf and hostOf', () => {
  it('read a URL into its place', () => {
    expect(routeOf('https://App.Example.com/users/7?x=1#f')).toBe('/users/{id}')
    expect(hostOf('https://App.Example.com/users/7')).toBe('app.example.com')
    expect(routeOf('https://a.example.com/projects/%7Bid%7D/servers')).toBe('/projects/{id}/servers')
    expect(routeOf(undefined)).toBe('none')
    expect(hostOf('nope')).toBe('none')
  })
  it('shortens a long route to its end', () => {
    expect(shortRoute('/a/b/c/d')).toBe('…/c/d')
    expect(shortRoute('/a/b')).toBe('/a/b')
  })
})
