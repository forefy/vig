import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '@/api/client'
import type { Evidence } from './types'
import { time } from './types'
import { baseLife, liveLag } from './LivePulses'

// The player moves the reader through the watch like a session replay: the
// picture is a function of the playhead. Paused, the dots stand where the
// moment leaves them; dragged, they run forward or back with it; playing,
// it advances at a speed until it reaches now and is live again. Rows are
// read from the feed in ranges around the playhead and kept in a store.

export const speeds = [1, 10, 60] as const
export type Speed = (typeof speeds)[number]

const tickMs = 100
const readAhead = 30_000 // chain ms read ahead of the playhead, times speed
const readBack = 120_000 // chain ms read behind a seek
const rangeRows = 500
const storeRows = 4000
const dockRows = 200

export type Player = {
  at: number | null // null is now, live
  playing: boolean
  speed: Speed
  life: number // how long one call takes to cross the map, in chain ms
  rows: Evidence[] // what had happened up to the playhead, oldest first, for the dock
  flight: Evidence[] // every row the store holds, for the map to place by time
  loading: boolean
  seek: (t: number | null) => void
  toggle: () => void
  setSpeed: (s: Speed) => void
  playFrom: (t: number) => void
}

type Store = { rows: Evidence[]; ids: Set<string>; from: number; to: number }
const emptyStore = (): Store => ({ rows: [], ids: new Set(), from: 0, to: 0 })
const at = (e: Evidence) => time(e.at ?? e.created_at)

export function usePlayer(auditId: string, admin: boolean): Player {
  const [head, setHead] = useState<number | null>(null)
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState<Speed>(10)
  const store = useRef<Store>(emptyStore())
  const [rows, setRows] = useState<Evidence[]>([])
  const [loading, setLoading] = useState(false)
  const reading = useRef(0)
  const path = admin ? '/v1/admin/audits/{id}/surface/live' : '/v1/audits/{id}/surface/live'

  // merge folds a read into the store, unique by id, in time order, and
  // drops what is far from the playhead.
  const merge = useCallback((list: Evidence[], center: number) => {
    const s = store.current
    let added = false
    for (const e of list) {
      const id = e.id ?? `${e.node_id}:${e.ref}`
      if (s.ids.has(id)) continue
      s.ids.add(id)
      s.rows.push(e)
      added = true
    }
    if (!added) return
    s.rows.sort((a, b) => at(a) - at(b))
    if (s.rows.length > storeRows) {
      s.rows.sort((a, b) => Math.abs(at(a) - center) - Math.abs(at(b) - center))
      s.rows = s.rows.slice(0, storeRows).sort((a, b) => at(a) - at(b))
      s.ids = new Set(s.rows.map((e) => e.id ?? `${e.node_id}:${e.ref}`))
      s.from = at(s.rows[0])
      s.to = at(s.rows[s.rows.length - 1])
    }
    setRows([...s.rows])
  }, [])

  // read loads a range, oldest first, when since is given: what playback
  // crosses next. Without since it loads the newest rows up to before:
  // what a seek lands on, which on a busy program a range from behind
  // would never reach.
  const read = useCallback(
    async (since: number | undefined, before: number, center: number) => {
      const n = ++reading.current
      setLoading(true)
      try {
        const query = since === undefined ? { before: new Date(before).toISOString(), limit: rangeRows } : { since: new Date(since).toISOString(), before: new Date(before).toISOString(), limit: rangeRows }
        const r = await api.GET(path, { params: { path: { id: auditId }, query } })
        const list = r.data?.evidence ?? []
        const s = store.current
        merge(list, center)
        if (since === undefined) {
          // Covered from the oldest row landed (or the whole span when few) to before.
          const from = list.length >= rangeRows ? at(list[0]) : before - readBack
          s.from = s.from === 0 ? from : Math.min(s.from, from)
          s.to = Math.max(s.to, before)
        } else {
          const to = list.length >= rangeRows ? at(list[list.length - 1]) : before
          s.from = s.from === 0 ? since : Math.min(s.from, since)
          s.to = Math.max(s.to, to)
        }
      } finally {
        if (reading.current === n) setLoading(false)
      }
    },
    [auditId, path, merge],
  )

  // A seek pauses, empties the store when it lands outside what the
  // store holds, then reads back from the moment.
  const seek = useCallback((t: number | null) => {
    setHead(t)
    setPlaying(false)
    const s = store.current
    if (t === null || s.rows.length === 0 || t < s.from - readBack || t > s.to + readBack) {
      store.current = emptyStore()
      setRows([])
    }
  }, [])
  const wanted = playing ? null : head
  useEffect(() => {
    if (wanted === null) return
    const id = setTimeout(() => {
      const s = store.current
      if (s.rows.length > 0 && wanted >= s.from && wanted <= s.to) return
      void read(undefined, wanted, wanted)
    }, 150)
    return () => clearTimeout(id)
  }, [wanted, read])

  const playFrom = useCallback((t: number) => {
    setHead(t)
    setPlaying(true)
  }, [])
  const toggle = useCallback(() => {
    setPlaying((p) => {
      if (p) return false
      // Playing from now starts a little back, so there is something to play.
      setHead((h) => h ?? Date.now() - liveLag - 60_000)
      return true
    })
  }, [])

  // Playing: read ahead of the playhead.
  const seated = playing && head !== null
  useEffect(() => {
    if (!seated || head === null) return
    const s = store.current
    const need = head + speed * readAhead
    if (s.to >= need || loading) return
    const since = s.rows.length === 0 ? head - readBack : Math.max(s.to, head - readBack)
    const before = Math.min(Date.now() - liveLag, since + speed * readAhead * 4)
    if (before - since < 1000) return
    void read(since, before, head)
  }, [seated, head, speed, loading, read])

  // The clock: advance, and stop at now.
  useEffect(() => {
    if (!seated) return
    const id = setInterval(() => {
      setHead((prev) => {
        if (prev === null) return prev
        const next = prev + speed * tickMs
        if (next >= Date.now() - liveLag) {
          setPlaying(false)
          store.current = emptyStore()
          setRows([])
          return null
        }
        return next
      })
    }, tickMs)
    return () => clearInterval(id)
  }, [seated, speed])

  const dock = useMemo(() => {
    if (head === null) return []
    const upTo = rows.filter((e) => at(e) <= head)
    return upTo.length > dockRows ? upTo.slice(upTo.length - dockRows) : upTo
  }, [rows, head])
  const life = baseLife * (head === null ? 1 : speed)
  return { at: head, playing, speed, life, rows: dock, flight: rows, loading, seek, toggle, setSpeed, playFrom }
}
