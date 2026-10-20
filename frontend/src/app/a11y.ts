import { useCallback, useSyncExternalStore } from 'react'

// Reader settings: text size, contrast, motion. One store so the footer
// control and the page agree; public/theme.js applies the stored values
// before first paint. Text size is a zoom on the root so every px-sized
// label scales with the page; contrast and motion are classes on <html>
// that tokens.css reads.
export const scaleSteps = [0.9, 1, 1.1, 1.2, 1.35] as const
export type ScaleStep = (typeof scaleSteps)[number]

export type A11y = { scale: ScaleStep; contrast: boolean; reduceMotion: boolean }

const listeners = new Set<() => void>()
let current: A11y | null = null

function read(): A11y {
  const out: A11y = { scale: 1, contrast: false, reduceMotion: false }
  if (typeof window === 'undefined') return out
  try {
    const v = Number(localStorage.getItem('text-scale'))
    out.scale = scaleSteps.find((s) => s === v) ?? 1
    out.contrast = localStorage.getItem('contrast') === 'high'
    out.reduceMotion = localStorage.getItem('motion') === 'reduce'
  } catch {
    /* no storage */
  }
  return out
}

function apply(a: A11y) {
  if (typeof document === 'undefined') return
  const el = document.documentElement
  el.style.zoom = a.scale === 1 ? '' : String(a.scale)
  el.style.setProperty('--text-scale', String(a.scale))
  el.classList.toggle('contrast', a.contrast)
  el.classList.toggle('reduce-motion', a.reduceMotion)
}

function get(): A11y {
  if (!current) current = read()
  return current
}

function store(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    /* no storage */
  }
}

export function setA11y(patch: Partial<A11y>) {
  current = { ...get(), ...patch }
  store('text-scale', current.scale === 1 ? null : String(current.scale))
  store('contrast', current.contrast ? 'high' : null)
  store('motion', current.reduceMotion ? 'reduce' : null)
  apply(current)
  listeners.forEach((l) => l())
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

const fallback: A11y = { scale: 1, contrast: false, reduceMotion: false }

export function useA11y() {
  const a = useSyncExternalStore(subscribe, get, () => fallback)
  const index = scaleSteps.indexOf(a.scale)
  const step = useCallback((delta: 1 | -1) => {
    const i = scaleSteps.indexOf(get().scale) + delta
    if (i >= 0 && i < scaleSteps.length) setA11y({ scale: scaleSteps[i] })
  }, [])
  const resetScale = useCallback(() => setA11y({ scale: 1 }), [])
  const setContrast = useCallback((contrast: boolean) => setA11y({ contrast }), [])
  const setReduceMotion = useCallback((reduceMotion: boolean) => setA11y({ reduceMotion }), [])
  const changed = a.scale !== 1 || a.contrast || a.reduceMotion
  return { ...a, changed, step, resetScale, setContrast, setReduceMotion, min: index === 0, max: index === scaleSteps.length - 1 }
}

// reducedMotion answers the OS preference or the reader's own choice.
export function reducedMotion(): boolean {
  if (typeof document !== 'undefined' && document.documentElement.classList.contains('reduce-motion')) return true
  try {
    return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
  } catch {
    return false
  }
}
