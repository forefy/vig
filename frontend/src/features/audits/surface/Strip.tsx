import { useMemo, useRef, useState } from 'react'
import { Pause, Play } from 'lucide-react'
import { StateBadge } from '@/components/primitives'
import { cn, relTime, short } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { axisOf, clockOf, layout, niceTicks, sumCalls } from './live'
import type { LiveState } from './live'
import type { Player } from './usePlayer'
import { speeds } from './usePlayer'
import type { Program, Surface } from './types'
import type { Words } from './model'

// spans are the windows the timeline can show.
const spans: [string, number | null][] = [['all', null], ['1h', 3600_000], ['10m', 600_000], ['1m', 60_000]]

// The time of the surface, as a player. The whole watch on one linear
// axis: calls per bucket as peaks, failures stacked in red, one coverage
// lane per program under them (live, backfilled, missed), every upgrade
// and state write a tick, the transactions in view as marks. The playhead
// stands at now while live; a pointer sets a moment, play moves it
// forward at a speed and the map replays what it crosses, and reaching
// now is live again.
export function Strip({ live, surface, programs, player, marks, picked, words, className }: { live: LiveState; surface: Surface; programs: Program[]; player: Player; marks: { sig: string; at: number }[]; picked: string | null; words: Words; className?: string }) {
  const now = useNow(1000)
  const w = live.watch
  const sample = surface.sampling
  const at = player.at
  // The span on screen: the whole watch, or a window that ends at now
  // while live and holds the playhead while not. A narrow window is where
  // a drag moves dots one to one.
  const [span, setSpan] = useState<number | null>(null)
  const whole = useMemo(() => {
    const start = w ? Date.parse(w.since ?? '') : sample ? Date.parse(sample.from ?? '') : NaN
    const first = live.activity.length ? Date.parse(live.activity[0].at) : NaN
    const cands = [start, first].filter(Number.isFinite)
    const t = cands.length ? Math.min(...cands) : now - 60 * 60 * 1000
    return Math.min(t, now - 60_000)
  }, [w, sample, live.activity, now])
  // A window holds still while the playhead moves inside it, so a drag
  // does not chase its own axis; it slides only when the playhead leaves.
  const [winEnd, setWinEnd] = useState<number | null>(null)
  const axis = useMemo(() => {
    if (span === null) return axisOf(whole, now)
    let end: number
    if (at === null) end = now
    else if (winEnd !== null && at <= winEnd && at >= winEnd - span) end = winEnd
    else end = Math.min(now, at + span * 0.25)
    return axisOf(Math.max(whole, end - span), end)
  }, [whole, now, span, at, winEnd])
  const clock = useMemo(() => clockOf(live.coverage, live.rows, w && w.last_slot && w.last_event_at ? { slot: w.last_slot, at: w.last_event_at } : undefined), [live.coverage, live.rows, w])
  const { segments, ticks } = useMemo(() => layout(live.coverage, live.rows, clock, axis), [live.coverage, live.rows, clock, axis])
  const labels = useMemo(() => niceTicks(axis.t0, axis.now), [axis])
  const maxCalls = Math.max(1, ...live.activity.map((b) => b.calls))
  const bucketMs = live.bucket * 1000
  const calls = sumCalls(live.totals)
  const gaps = live.coverage.filter((c) => c.kind === 'gap').length
  const pct = (x: number) => `${(Math.max(0, Math.min(1, x)) * 100).toFixed(3)}%`
  const state = w?.state ?? 'off'
  const badge =
    state === 'live' ? <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">live</StateBadge>
    : state === 'connecting' || state === 'backfilling' ? <StateBadge state="pending" className="h-5 px-1.5 text-[10.5px]">{state}</StateBadge>
    : state === 'error' ? <StateBadge state="bad" className="h-5 px-1.5 text-[10.5px]" tip={w?.error}>watch error</StateBadge>
    : state === 'ended' ? <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">watch ended</StateBadge>
    : <StateBadge state="pending" className="h-5 px-1.5 text-[10.5px]">watch off</StateBadge>
  const laneOf = (pid: string) => segments.filter((s) => s.subject === pid)
  const sampleX = sample?.from && sample?.to ? [axis.x(Date.parse(sample.from)), axis.x(Date.parse(sample.to))] : null
  const longRange = axis.now - axis.t0 > 36 * 3600_000
  const shortRange = axis.now - axis.t0 <= 15 * 60_000
  const clockLabel = (t: number) => new Date(t).toLocaleTimeString('en-GB', longRange ? { weekday: 'short', hour: '2-digit', minute: '2-digit' } : shortRange ? { hour: '2-digit', minute: '2-digit', second: '2-digit' } : { hour: '2-digit', minute: '2-digit' })

  // A pointer on the track is a seek; a drag keeps seeking; hover shows
  // the time under the pointer.
  const track = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const frac = (clientX: number): number | null => {
    const r = track.current?.getBoundingClientRect()
    if (!r || r.width === 0) return null
    return (clientX - r.left) / r.width
  }
  const seekAt = (clientX: number) => {
    const u = frac(clientX)
    if (u === null) return
    if (u >= 0.995 && axis.now >= now - 1000) {
      player.seek(null)
      setWinEnd(null)
      return
    }
    const t = axis.t(u)
    // Landing inside the window pins it there; the next drag stays on it.
    if (span !== null && winEnd === null) setWinEnd(axis.now)
    player.seek(t)
  }
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId)
    seekAt(e.clientX)
  }
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const u = frac(e.clientX)
    setHover(u === null ? null : axis.t(u))
    if (e.buttons & 1) seekAt(e.clientX)
  }
  const headX = at === null ? 1 : axis.x(at)
  const timeWord = (t: number) => `${new Date(t).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' })} · ${relTime(new Date(t).toISOString())}`

  return (
    <div className={cn('rounded-md border border-border bg-card px-3.5 pt-2.5 pb-2', className)}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 font-mono text-[10.5px] text-muted-foreground">
        <button type="button" onClick={player.toggle} aria-label={player.playing ? 'Pause' : 'Play'} aria-pressed={player.playing} className={cn('flex size-6 items-center justify-center rounded-sm border', player.playing ? 'border-primary text-primary' : 'border-border text-foreground hover:border-muted-foreground')}>
          {player.playing ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
        </button>
        <span className="flex items-center gap-0.5">
          {speeds.map((s) => (
            <button key={s} type="button" onClick={() => player.setSpeed(s)} aria-pressed={player.speed === s} className={cn('rounded-sm px-1.5 py-px', player.speed === s ? 'bg-accent text-foreground' : 'hover:text-foreground')}>{s}×</button>
          ))}
        </span>
        <span className={cn('num', at === null ? 'text-ok' : 'text-foreground')}>{at === null ? 'now' : timeWord(at)}</span>
        <span className="flex items-center gap-0.5">
          {spans.map(([label, ms]) => (
            <button key={label} type="button" onClick={() => { setSpan(ms); setWinEnd(null) }} aria-pressed={span === ms} className={cn('rounded-sm px-1.5 py-px', span === ms ? 'bg-accent text-foreground' : 'hover:text-foreground')}>{label}</button>
          ))}
        </span>
        {player.loading && <span className="text-faint">…</span>}
        <span className="ml-auto flex flex-wrap items-center gap-x-3 gap-y-1">
          {badge}
          {calls > 0 && <span><span className="num text-foreground">{calls.toLocaleString('en-US')}</span> calls{w?.since ? ` since ${relTime(w.since).replace(' ago', '')} ago` : ''}</span>}
          {gaps > 0 && <span className="text-warn">{gaps} gap{gaps === 1 ? '' : 's'}</span>}
          {state === 'live' && typeof w?.quiet_seconds === 'number' && <span>quiet <span className="num">{w.quiet_seconds < 90 ? `${w.quiet_seconds}s` : relTime(new Date(now - w.quiet_seconds * 1000).toISOString()).replace(' ago', '')}</span></span>}
          {w?.last_slot ? <span className="num">{words.height} {w.last_slot.toLocaleString('en-US')}</span> : null}
          {live.error && <span className="text-bad">{live.error}</span>}
          <button type="button" onClick={() => player.seek(null)} aria-pressed={at === null} className={cn('rounded-sm border px-1.5 py-px', at === null ? 'border-ok text-ok' : 'border-border text-muted-foreground hover:text-foreground')}>now</button>
        </span>
      </div>

      <div
        ref={track}
        role="slider"
        aria-label="Time"
        aria-valuemin={axis.t0}
        aria-valuemax={now}
        aria-valuenow={at ?? now}
        tabIndex={0}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerLeave={() => setHover(null)}
        className="relative mt-2.5 h-24 cursor-crosshair touch-none select-none"
      >
        {/* peaks: calls per bucket, failures on top */}
        <div className="absolute inset-x-0 top-0 bottom-[14px] border-b border-border" aria-hidden>
          {live.activity.map((b) => {
            const t = Date.parse(b.at)
            const x0 = axis.x(t), x1 = axis.x(t + bucketMs)
            const h = Math.max(2, Math.round(Math.sqrt(b.calls / maxCalls) * 100))
            const f = b.calls > 0 ? (b.failed / b.calls) * h : 0
            return (
              <span key={b.at} className="absolute bottom-0" style={{ left: pct(x0), width: `max(2px, calc(${pct(x1 - x0)} - 1px))`, height: `${h}%` }} title={`${clockLabel(t)} · ${b.calls} calls${b.failed ? ` · ${b.failed} failed` : ''}`}>
                <span className="absolute inset-0 rounded-t-[1px] bg-primary/55" />
                {f > 0 && <span className="absolute inset-x-0 top-0 rounded-t-[1px] bg-bad/80" style={{ height: `${(f / h) * 100}%` }} />}
              </span>
            )
          })}
        </div>

        {/* coverage, one lane per program, under the peaks */}
        <div className="absolute inset-x-0 bottom-[3px] flex flex-col gap-[2px]" aria-hidden>
          {programs.map((p) => (
            <div key={p.id} className="relative h-[4px]" title={short(p.id, 4, 4)}>
              <span className="absolute inset-y-[1px] inset-x-0 bg-muted" />
              {laneOf(p.id).map((s) => (
                <span
                  key={s.key}
                  className={cn('absolute inset-y-0', s.kind === 'live' ? 'bg-ok/80' : s.kind === 'backfill' ? 'bg-muted-foreground/60' : 'bg-[repeating-linear-gradient(45deg,var(--warn)_0_1px,transparent_1px_4px)] opacity-70')}
                  style={{ left: pct(s.x0), width: pct(s.x1 - s.x0) }}
                  title={`${s.kind} · ${relTime(new Date(s.from).toISOString())} to ${relTime(new Date(s.to).toISOString())}`}
                />
              ))}
            </div>
          ))}
        </div>

        {sampleX && (
          <span className="pointer-events-none absolute bottom-0 h-[3px] border-x border-b border-muted-foreground" style={{ left: pct(sampleX[0]), width: `max(2px, ${pct(sampleX[1] - sampleX[0])})` }} title={`sample · ${sample?.transactions ?? 0} tx`} />
        )}

        {ticks.map((t) => (
          <span key={t.key} className="pointer-events-none absolute top-0 bottom-[14px]" style={{ left: pct(t.x) }} title={`${t.label} · ${relTime(new Date(t.at).toISOString())}`}>
            <span className={cn('absolute inset-y-0 w-px', t.kind === 'upgrade' ? 'bg-bad' : t.kind === 'idl' ? 'bg-warn' : 'bg-warn/50')} />
            <span className={cn('absolute -top-[3px] -left-[3px] size-[7px] rounded-full', t.kind === 'upgrade' ? 'bg-bad' : t.kind === 'idl' ? 'bg-warn' : 'bg-warn/70')} />
          </span>
        ))}

        {marks.map((m) => (
          <span key={m.sig} className={cn('pointer-events-none absolute bottom-[15px] -translate-x-1/2 rounded-full', picked === m.sig ? 'size-2 bg-primary ring-2 ring-primary/40' : 'size-[3px] bg-foreground/50')} style={{ left: pct(axis.x(m.at)) }} />
        ))}

        {hover !== null && (
          <span className="pointer-events-none absolute top-0 bottom-[14px] w-px bg-foreground/30" style={{ left: pct(axis.x(hover)) }} aria-hidden>
            <span className="absolute -top-0.5 left-1.5 whitespace-nowrap rounded-sm bg-popover px-1 font-mono text-[9.5px] text-muted-foreground">{clockLabel(hover)}</span>
          </span>
        )}

        {/* the playhead: a line with a handle */}
        <span className={cn('pointer-events-none absolute top-0 bottom-0 w-[2px] -translate-x-1/2', at === null ? 'bg-ok' : 'bg-foreground')} style={{ left: pct(headX) }} aria-hidden>
          <span className={cn('absolute -bottom-[2px] left-1/2 h-3 w-2.5 -translate-x-1/2 rounded-[3px] border', at === null ? 'border-ok bg-ok' : 'border-foreground bg-background')} />
        </span>
      </div>

      <div className="relative mt-1 h-3 font-mono text-[9.5px] text-faint" aria-hidden>
        {labels.map((t) => (
          <span key={t} className="absolute -translate-x-1/2" style={{ left: pct(axis.x(t)) }}>{clockLabel(t)}</span>
        ))}
      </div>
    </div>
  )
}
