// What a result cell is, from its column and the shape of its value: a
// repo that lives on GitHub, a name that answers on https, a digest worth
// copying, or the three columns that make a result a list of paths.

const SHA256_RE = /^[a-f0-9]{64}$/i
const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/
const HOST_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{1,62}$/i
const REPO_COLS = new Set(['repo', 'repository'])
const HOST_COLS = new Set(['host', 'fqdn', 'apex', 'hostname', 'cname'])

export function cellText(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean' || typeof v === 'bigint') return String(v)
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

export const isSha256 = (v: unknown): v is string => typeof v === 'string' && SHA256_RE.test(v)

export function cellLink(column: string, v: unknown): string | null {
  if (typeof v !== 'string' || !v) return null
  const col = column.toLowerCase()
  if (REPO_COLS.has(col) && REPO_RE.test(v)) return `https://github.com/${v}`
  if (v.startsWith('repo:') && REPO_RE.test(v.slice(5))) return `https://github.com/${v.slice(5)}`
  if (HOST_COLS.has(col) && HOST_RE.test(v)) return `https://${v}`
  const m = /^(?:host|subdomain):(.+)$/.exec(v)
  if (m && HOST_RE.test(m[1]!)) return `https://${m[1]}`
  return null
}

export function pathColumns(columns: string[]): { src: string; dst: string; rel: string } | null {
  const has = (c: string) => columns.includes(c)
  return has('src_id') && has('dst_id') && has('rel') ? { src: 'src_id', dst: 'dst_id', rel: 'rel' } : null
}
