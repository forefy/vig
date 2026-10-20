import { useCallback, useSyncExternalStore } from 'react'

export type Theme = 'dark' | 'light'

// One store for the whole app so the rail, the public footer and the toaster
// agree. public/theme.js applies the same choice before first paint.
const listeners = new Set<() => void>()
let current: Theme | null = null

export function readTheme(): Theme {
  if (typeof window === 'undefined') return 'dark'
  try {
    const v = localStorage.getItem('theme')
    if (v === 'light' || v === 'dark') return v
  } catch {
    /* no storage */
  }
  try {
    if (typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: light)').matches) return 'light'
  } catch {
    /* no matchMedia */
  }
  return 'dark'
}

export function applyTheme(t: Theme) {
  if (typeof document === 'undefined') return
  document.documentElement.classList.toggle('dark', t === 'dark')
}

function get(): Theme {
  if (!current) current = readTheme()
  return current
}

export function setTheme(t: Theme) {
  current = t
  try {
    localStorage.setItem('theme', t)
  } catch {
    /* no storage */
  }
  applyTheme(t)
  listeners.forEach((l) => l())
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

export function useTheme() {
  const theme = useSyncExternalStore(subscribe, get, () => 'dark' as Theme)
  const toggle = useCallback(() => setTheme(get() === 'dark' ? 'light' : 'dark'), [])
  return { theme, toggle }
}
