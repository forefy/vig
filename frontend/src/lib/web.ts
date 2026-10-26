// The web adapter's places, folded the way the backend folds them (core
// RouteShape): a report facet and a surface node for one endpoint name the
// same route.

export const ID_SEGMENT = '{id}'

// A path segment is an identifier when it is a number, a long hex or UUID,
// or a long opaque token; each must carry a digit, so a long word stays.
function isIdSegment(s: string): boolean {
  if (s === ID_SEGMENT) return true
  if (!/\d/.test(s)) return false
  return /^\d+$/.test(s) || /^[0-9a-fA-F-]{16,}$/.test(s) || /^[A-Za-z0-9_-]{22,}$/.test(s)
}

// routeShape folds identifiers out of a path; the root is "/".
export function routeShape(path: string): string {
  return `/${path.split('/').filter(Boolean).map((s) => (isIdSegment(s) ? ID_SEGMENT : s)).join('/')}`
}

export function hostOf(url: string | undefined): string {
  if (!url) return 'none'
  try {
    return new URL(url).host.toLowerCase()
  } catch {
    return 'none'
  }
}

// The route of an endpoint: its path with ids folded, so the same handler
// reached with different ids is one route.
export function routeOf(url: string | undefined): string {
  if (!url) return 'none'
  try {
    return routeShape(decodeURIComponent(new URL(url).pathname))
  } catch {
    return 'none'
  }
}

// A chip shows the end of a route, where handlers differ; the value the
// clause carries is the whole path.
export function shortRoute(route: string): string {
  const parts = route.split('/').filter(Boolean)
  return parts.length > 2 ? `…/${parts.slice(-2).join('/')}` : route
}

// Where a location is: the endpoint when it has one, or when what was
// written in file is an address (older web findings put it there), else
// the file. A place that is an address carries no line.
export function placeOf(loc: { file?: string; url?: string }): { url: string; file: string } {
  const url = loc.url || (loc.file && /^https?:\/\//i.test(loc.file) ? loc.file : '')
  return { url, file: url ? '' : loc.file ?? '' }
}
