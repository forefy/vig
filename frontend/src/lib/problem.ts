// RFC 9457 problem, as the client sees it. Anything else becomes one.
export type Problem = { status?: number; title?: string; detail?: string; errors?: { message?: string; location?: string }[] }

export function problemOf(e: unknown): Problem {
  if (e && typeof e === 'object') return e as Problem
  return { title: e instanceof Error ? e.message : 'Error' }
}

// The detail, and for a validation failure the first field that failed:
// "validation failed · body.report.methods.fuzzing.coverage: expected length <= 40".
export function problemDetail(e: unknown, fallback: string): string {
  const p = problemOf(e)
  const base = p.detail ?? p.title ?? fallback
  const first = p.errors?.[0]
  if (first && (first.location || first.message)) return `${base} · ${[first.location?.replace(/^body\./, ''), first.message].filter(Boolean).join(': ')}`
  return base
}
