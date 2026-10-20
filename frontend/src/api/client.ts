// The only way the frontend talks to the backend. Types come from the spec
// the Go server generates; regenerate with `make gen`. Never hand-edit
// src/api/generated.
import createClient from 'openapi-fetch'
import type { paths } from './generated/schema'

const apiOrigin = import.meta.env.VITE_API_URL ?? ''

export const api = createClient<paths>({
  baseUrl: apiOrigin,
  credentials: 'include',
})

// The API reference is served by the backend next to the spec it renders.
export const docsUrl = apiOrigin + '/docs'

// Absolute API origin for text handed to another machine.
export const apiOriginAbs = () => apiOrigin || window.location.origin

// The access cookie is short-lived; the refresh cookie rotates it. On the
// first 401 from a non-auth route, refresh once (shared across concurrent
// callers) and replay the request. A failed refresh leaves the 401 in
// place, and the auth guard sends the viewer to sign in.
let refreshing: Promise<boolean> | null = null
function refreshSession(): Promise<boolean> {
  if (!refreshing) {
    refreshing = fetch((import.meta.env.VITE_API_URL ?? '') + '/v1/auth/refresh', { method: 'POST', credentials: 'include' })
      .then((r) => r.ok)
      .catch(() => false)
      .finally(() => {
        refreshing = null
      })
  }
  return refreshing
}

api.use({
  async onResponse({ request, response }) {
    if (response.status !== 401) return response
    const path = new URL(request.url).pathname
    if (path.startsWith('/v1/auth/') || request.headers.get('x-retried') === '1') return response
    if (!(await refreshSession())) return response
    const retry = new Request(request, { headers: new Headers(request.headers) })
    retry.headers.set('x-retried', '1')
    return fetch(retry)
  },
})

// Every error the app sees is an RFC 9457 problem. A proxy or a gateway can
// answer with an empty body before the backend does; turn that into one too,
// so query hooks always reject with a status instead of resolving to nothing.
api.use({
  async onResponse({ response }) {
    if (response.ok) return response
    const type = response.headers.get('content-type') ?? ''
    if (type.includes('json')) return response
    const text = await response.clone().text()
    if (text.trim().startsWith('{')) return response
    const body = JSON.stringify({ status: response.status, title: response.statusText || 'Error', detail: text.slice(0, 200) })
    return new Response(body, { status: response.status, statusText: response.statusText, headers: { 'content-type': 'application/problem+json' } })
  },
})

export type { paths, components } from './generated/schema'
