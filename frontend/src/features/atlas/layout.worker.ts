// The network is laid out off the page's thread, so a large picture does
// not hold the page still while it settles.
import { layout } from './graph'
import type { Cols } from './graph'

type Ask = { id: number; rows: Record<string, unknown>[]; cols: Cols; shadows: Record<string, unknown>[] }

const scope = self as unknown as { onmessage: ((e: MessageEvent<Ask>) => void) | null; postMessage: (m: unknown) => void }
scope.onmessage = (e) => {
  const { id, rows, cols, shadows } = e.data
  scope.postMessage({ id, g: layout(rows, cols, shadows) })
}
