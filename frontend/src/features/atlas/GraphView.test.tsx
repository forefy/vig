import { beforeAll, describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { Graph } from './GraphView'

// The relations around a result arrive after its rows. A reader who has
// already panned, zoomed or picked keeps the view, and the result's nodes
// stay where they were drawn.

// jsdom measures nothing and scrolls nothing: the frame reports the size
// a test sets, 800 by 480 unless it says otherwise.
let size = { width: 800, height: 480 }
beforeAll(() => {
  globalThis.ResizeObserver = class {
    cb: ResizeObserverCallback
    constructor(cb: ResizeObserverCallback) {
      this.cb = cb
    }
    observe() {
      this.cb([{ contentRect: size } as unknown as ResizeObserverEntry], this as unknown as ResizeObserver)
    }
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
  Element.prototype.scrollIntoView ??= () => {}
})

const cols = { src: 'src_id', dst: 'dst_id', rel: 'rel' }
const rows: Record<string, unknown>[] = []
for (let i = 0; i < 6; i++) rows.push({ src_id: `fn:acme/app:f.go:a${i}:1`, dst_id: `fn:acme/app:f.go:b${i % 2}:2`, rel: 'calls' })
const around: Record<string, unknown>[] = []
for (let i = 0; i < 20; i++) around.push({ src_id: `fn:acme/app:f.go:b${i % 2}:2`, dst_id: `fn:acme/app:g.go:s${i}:9`, rel: 'calls' })

const NONE: Record<string, unknown>[] = []
const view = (c: HTMLElement) => c.querySelector('svg[aria-label="The result as a graph"] > g')!.getAttribute('transform')
const places = (c: HTMLElement) =>
  Array.from(c.querySelectorAll('svg[aria-label="The result as a graph"] g[role=button]'))
    .filter((g) => !g.querySelector('title')!.textContent!.includes('g.go'))
    .map((g) => `${g.querySelector('title')!.textContent} ${g.getAttribute('transform')}`)

describe('the view when the relations around a result arrive', () => {
  it('keeps a pan and the result\'s places', () => {
    const r = render(<Graph rows={rows} cols={cols} shadows={NONE} />)
    const svg = r.getByRole('group', { name: 'The result as a graph' })
    const fitted = view(r.container)
    fireEvent.pointerDown(svg, { pointerId: 1, clientX: 8, clientY: 8 })
    fireEvent.pointerUp(svg, { pointerId: 1, clientX: 8, clientY: 8 })
    fireEvent.wheel(svg, { deltaX: 30, deltaY: 120 })
    const panned = view(r.container)
    expect(panned).not.toBe(fitted)
    const before = places(r.container)
    r.rerender(<Graph rows={rows} cols={cols} shadows={around} />)
    expect(r.container.querySelectorAll('svg[aria-label="The result as a graph"] g[role=button]').length).toBe(before.length + 20)
    expect(view(r.container)).toBe(panned)
    expect(places(r.container)).toEqual(before)
  })

  it('keeps a picked node centred', () => {
    const r = render(<Graph rows={rows} cols={cols} shadows={NONE} />)
    const node = r.getByRole('button', { name: 'b0:2' })
    fireEvent.click(node)
    const centred = view(r.container)
    r.rerender(<Graph rows={rows} cols={cols} shadows={around} />)
    expect(view(r.container)).toBe(centred)
    expect(r.getByRole('button', { name: 'b0:2' })).toHaveAttribute('aria-pressed', 'true')
  })
})

describe('the opening view and expand', () => {
  it('opens framing every node of the result, however large', () => {
    const big: Record<string, unknown>[] = []
    for (let i = 0; i < 120; i++) big.push({ src_id: `entry:e${i % 12}`, dst_id: `entry:f${i}`, rel: 'triggers' })
    const r = render(<Graph rows={big} cols={cols} shadows={NONE} />)
    const t = view(r.container)!.match(/translate\(([-\d.e]+) ([-\d.e]+)\) scale\(([\d.e]+)\)/)!
    const [x, y, k] = [Number(t[1]), Number(t[2]), Number(t[3])]
    // jsdom measures nothing, so the frame is the view's default 800 by 480.
    for (const g of Array.from(r.container.querySelectorAll('svg[aria-label="The result as a graph"] g[role=button]'))) {
      const m = g.getAttribute('transform')!.match(/translate\(([-\d.e]+) ([-\d.e]+)\)/)!
      const sx = x + Number(m[1]) * k
      const sy = y + Number(m[2]) * k
      expect(sx).toBeGreaterThanOrEqual(0)
      expect(sx).toBeLessThanOrEqual(800)
      expect(sy).toBeGreaterThanOrEqual(0)
      expect(sy).toBeLessThanOrEqual(480)
    }
  })

  it('follows an expanded node into the grown picture', () => {
    let grown = rows
    const r = render(<Graph rows={grown} cols={cols} shadows={NONE} onExpand={() => undefined} />)
    fireEvent.click(r.getByRole('button', { name: 'b0:2' }))
    fireEvent.click(r.getByRole('button', { name: 'expand' }))
    grown = [...rows]
    for (let i = 0; i < 15; i++) grown.push({ src_id: 'fn:acme/app:f.go:b0:2', dst_id: `fn:acme/app:h.go:c${i}:3`, rel: 'calls' })
    r.rerender(<Graph rows={grown} cols={cols} shadows={NONE} onExpand={() => undefined} />)
    const t = view(r.container)!.match(/translate\(([-\d.e]+) ([-\d.e]+)\) scale\(([\d.e]+)\)/)!
    const node = r.getByRole('button', { name: 'b0:2' }).getAttribute('transform')!.match(/translate\(([-\d.e]+) ([-\d.e]+)\)/)!
    // A third of the way in, half way down, at a scale it reads at.
    expect(Number(t[1]) + Number(node[1]) * Number(t[3])).toBeCloseTo(800 / 3, 0)
    expect(Number(t[2]) + Number(node[2]) * Number(t[3])).toBeCloseTo(240, 0)
    expect(Number(t[3])).toBeGreaterThanOrEqual(0.8)
  })
})

describe('labels and narrow frames', () => {
  const big: Record<string, unknown>[] = []
  for (let i = 0; i < 90; i++) big.push({ src_id: `fn:acme/app:f.go:aRatherLongFunctionName${i}:1`, dst_id: `fn:acme/app:f.go:hub${i % 6}:2`, rel: 'calls' })
  const texts = (c: HTMLElement) => c.querySelectorAll('svg[aria-label="The result as a graph"] text[data-node]').length
  const scale = (c: HTMLElement) => Number(view(c)!.match(/scale\(([\d.e]+)\)/)![1])

  it('names every hub, and more nodes as the reader zooms in', () => {
    const r = render(<Graph rows={big} cols={cols} shadows={NONE} />)
    for (let i = 0; i < 6; i++) expect(r.container.querySelector(`text[data-node="fn:acme/app:f.go:hub${i}:2"]`)).not.toBeNull()
    const open = texts(r.container)
    for (let i = 0; i < 4; i++) fireEvent.click(r.getByRole('button', { name: 'Zoom in' }))
    expect(texts(r.container)).toBeGreaterThan(open)
  })

  it('opens a narrow frame close enough to read, on the weightiest node, and fit shows the whole result', () => {
    size = { width: 300, height: 500 }
    try {
      const r = render(<Graph rows={big} cols={cols} shadows={NONE} />)
      expect(scale(r.container)).toBeCloseTo(0.55, 2)
      fireEvent.click(r.getByRole('button', { name: 'fit' }))
      expect(scale(r.container)).toBeLessThan(0.55)
    } finally {
      size = { width: 800, height: 480 }
    }
  })
})

describe('a pick and the next result', () => {
  it('does not dim a new picture around a node it lacks', () => {
    const r = render(<Graph rows={rows} cols={cols} shadows={NONE} />)
    fireEvent.click(r.getByRole('button', { name: 'b0:2' }))
    const other = [{ src_id: 'fn:acme/app:k.go:x:1', dst_id: 'fn:acme/app:k.go:y:2', rel: 'calls' }]
    r.rerender(<Graph rows={other} cols={cols} shadows={NONE} />)
    for (const g of Array.from(r.container.querySelectorAll('svg[aria-label="The result as a graph"] g[role=button]'))) expect(g.getAttribute('class')).not.toMatch(/opacity-25/)
    expect(r.queryByRole('button', { name: 'expand' })).toBeNull()
  })
})

describe('a picked node\'s neighbours', () => {
  it('are counted in its card and shown in the picture, with no list of them', () => {
    const r = render(<Graph rows={rows} cols={cols} shadows={around} />)
    fireEvent.click(r.getByRole('button', { name: 'b0:2' }))
    // b0 is called by a0, a2 and a4, and calls ten of the relations around it.
    expect(r.container.textContent).toMatch(/3\s*in\s*·\s*10\s*out/)
    expect(r.getAllByRole('button', { name: 'a2:1' }).filter((el) => el.tagName === 'BUTTON')).toHaveLength(0)
    expect(r.container.querySelectorAll('circle[data-touched]')).toHaveLength(13)
  })
})
