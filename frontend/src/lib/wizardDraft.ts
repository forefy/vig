import type { components } from '@/api/client'

type Target = components['schemas']['Target']
type Program = components['schemas']['Program']

// The wizard's state, parked in sessionStorage so a detour (buying credits,
// a reload) comes back to the same step. One draft per user, one hour.
export type WizardDraft = {
  v: 1
  saved_at: number
  step: number
  kind: string
  coverage: 'full' | 'diff'
  source_kind: 'public' | 'installation' | 'program'
  source_pid: string
  chain?: string
  repo_url: string
  installation_id: string
  install_repo: string
  selector: 'branch' | 'tag' | 'commit'
  revision: string
  base: string
  base_url?: string
  hosts?: string[]
  paths?: string[]
  target: Target | null
  programs: Program[]
  selected: string[]
}

export type WizardDraftInput = Omit<WizardDraft, 'v' | 'saved_at'>

const TTL_MS = 60 * 60 * 1000
const key = (userId: string) => `audit-draft:${userId}`

export function loadDraft(userId: string): WizardDraft | null {
  try {
    const raw = sessionStorage.getItem(key(userId))
    if (!raw) return null
    const d = JSON.parse(raw) as Partial<WizardDraft>
    if (d.v !== 1 || typeof d.saved_at !== 'number' || typeof d.step !== 'number') return null
    if (Date.now() - d.saved_at > TTL_MS) {
      sessionStorage.removeItem(key(userId))
      return null
    }
    return d as WizardDraft
  } catch {
    return null
  }
}

export function saveDraft(userId: string, d: WizardDraftInput): void {
  try {
    const draft: WizardDraft = { v: 1, saved_at: Date.now(), ...d }
    sessionStorage.setItem(key(userId), JSON.stringify(draft))
  } catch {
    /* storage unavailable */
  }
}

export function clearDraft(userId: string): void {
  try {
    sessionStorage.removeItem(key(userId))
  } catch {
    /* storage unavailable */
  }
}
