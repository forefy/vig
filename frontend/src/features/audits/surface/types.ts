import type { components } from '@/api/client'

export type Node = components['schemas']['SurfaceNode']
export type Edge = components['schemas']['SurfaceEdge']
export type Program = components['schemas']['SurfaceProgram']
export type Evidence = components['schemas']['SurfaceEvidence']
export type Surface = components['schemas']['Surface']

// Fields the backend returns that the generated schema may not carry yet.
// Read through intersections so the file compiles before and after `make gen`.
export type Volume = { mint: string; decimals: number; volume: string; count: number }
export type TokenMove = { mint: string; decimals: number; delta: string; owner?: string }

export type NodeStats = {
  ok: number
  failed: number
  errors?: Record<string, number>
  units_min?: number
  units_med?: number
  units_max?: number
  signers: number
  first_seen?: string
  replays: number
  moves?: Volume[]
}

export type Coverage = {
  instructions: number
  instructions_observed: number
  instructions_replayed: number
  edges: number
  edges_observed: number
  signers: number
  first_seen?: string
  changed?: number
  added?: number
}

export type ProgramExtra = {
  bytecode_changed?: boolean
  upgrades?: string
  first_deploy_at?: string
  last_upgrade_at?: string
  immutable?: boolean
  loader?: string
}

export type ReplayState = { lamports: number; owner?: string; size: number; fields?: Record<string, string> }
export type ReplayAccount = {
  name?: string
  pubkey: string
  signer?: boolean
  writable?: boolean
  type?: string
  before?: ReplayState
  after?: ReplayState
  changed?: string[]
  pda?: 'ok' | 'mismatch'
}
export type Replay = {
  replay: string
  instruction?: string
  args?: Record<string, string>
  outcome: string
  error?: { code?: number; name?: string; msg?: string; raw: string }
  units?: number
  logs?: string[]
  calls?: { program: string; name: string; depth: number }[]
  accounts?: ReplayAccount[]
  via?: string
  tag?: string
  patch?: { name: string; value: string }
  overrides?: string[]
  time_travel?: string
  moves?: TokenMove[]
  // Carried over from the evidence row so replays of one signature order.
  at?: string
  key: string
}

export const statsOf = (n: Node | undefined): NodeStats | undefined => (n as (Node & { stats?: NodeStats }) | undefined)?.stats
export const coverageOf = (s: Surface): Coverage | undefined => (s as Surface & { coverage?: Coverage }).coverage
export const extraOf = (p: Program): ProgramExtra => p as Program & ProgramExtra
export const isWhatIf = (r: Replay): boolean => Boolean(r.patch || (r.overrides && r.overrides.length > 0) || r.time_travel)
// The verdict in a word or two: `ok`, the error's name, and its message
// when one was decoded (`Error · allowance`).
export const outcomeWord = (r: Replay): string => {
  if (r.outcome === 'ok') return 'ok'
  const name = r.error?.name || r.outcome
  const msg = r.error?.msg?.trim()
  return msg && msg !== name ? `${name} · ${msg}` : name
}
export const time = (iso?: string): number => (iso ? new Date(iso).getTime() : 0)
