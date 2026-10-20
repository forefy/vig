import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '@/api/client'
import { emptyLive, failed, fold, withFull } from './live'
import type { LiveState } from './live'

// pollMs matches the feed's own lag at confirmed commitment.
export const pollMs = 5000
// fullMs is how often the rows read in full are re-read; they arrive at
// the detail budget's pace, a couple a second at most.
export const fullMs = 15000
export const fullRows = 200

// useLive keeps the feed of one audit folded into state: a first read of
// the newest rows, then rows after the cursor every few seconds. The tab
// reads it whether the watch is live, quiet or off.
export function useLive(auditId: string, admin: boolean): LiveState {
  const key = `${admin ? 'admin' : 'user'}:${auditId}`
  // The state carries the key it belongs to, so a new audit starts empty
  // without a reset in an effect.
  const [box, setBox] = useState<{ key: string; state: LiveState }>(() => ({ key, state: emptyLive() }))
  const cursor = useRef('')
  useEffect(() => {
    cursor.current = ''
    let stop = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const apply = (fn: (s: LiveState) => LiveState) => setBox((b) => ({ key, state: fn(b.key === key ? b.state : emptyLive()) }))
    const read = async () => {
      const path = admin ? '/v1/admin/audits/{id}/surface/live' : '/v1/audits/{id}/surface/live'
      try {
        const q = cursor.current ? { after: cursor.current } : {}
        const r = await api.GET(path, { params: { path: { id: auditId }, query: q } })
        if (stop) return
        if (r.data) {
          const v = r.data
          cursor.current = v.cursor || cursor.current
          apply((s) => fold(s, v))
        } else apply((s) => failed(s, 'feed unavailable'))
      } catch (e) {
        if (!stop) apply((s) => failed(s, e instanceof Error ? e.message : 'feed unavailable'))
      }
      if (!stop) timer = setTimeout(() => void read(), document.hidden ? pollMs * 6 : pollMs)
    }
    // The rows read in full have their own, slower read: a busy program
    // pushes them out of the newest rows in seconds.
    let fullTimer: ReturnType<typeof setTimeout> | undefined
    const readFull = async () => {
      const path = admin ? '/v1/admin/audits/{id}/surface/live' : '/v1/audits/{id}/surface/live'
      try {
        const r = await api.GET(path, { params: { path: { id: auditId }, query: { full: true, limit: fullRows } } })
        if (!stop && r.data) {
          const full = r.data.evidence ?? []
          apply((s) => withFull(s, full))
        }
      } catch {
        // The next read tries again.
      }
      if (!stop) fullTimer = setTimeout(() => void readFull(), document.hidden ? fullMs * 4 : fullMs)
    }
    void read()
    void readFull()
    return () => {
      stop = true
      if (timer) clearTimeout(timer)
      if (fullTimer) clearTimeout(fullTimer)
    }
  }, [auditId, admin, key])
  const empty = useMemo(() => emptyLive(), [])
  return box.key === key ? box.state : empty
}
