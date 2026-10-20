import { useLayoutEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { ArrowRight } from 'lucide-react'
import { Mark } from '@/components/primitives'
import { Seo } from '@/components/primitives/Seo'
import { useMe } from '@/features/auth/useMe'
import { ScanForm } from '@/features/scan/ScanForm'
import { GridBug } from './GridBug'
import { freeNodes } from './grid'

const SHOWCASE = import.meta.env.VITE_SHOWCASE_REPORT ?? 'vault-signer-check'

// Landing archetype: a blueprint. The mark drawn large on a grid, one
// headline, one input. The only thing that moves is the bug.
export function Landing() {
  const { me } = useMe()
  const frame = useRef<HTMLDivElement>(null)
  const [grid, setGrid] = useState({ frameW: 0, frameH: 0, free: new Set<string>() })
  useLayoutEffect(() => {
    const el = frame.current
    if (!el) return
    let live = true
    const measure = () => {
      if (!live) return
      const box = el.getBoundingClientRect()
      // Everything the page has drawn, in frame coordinates. Text is measured
      // as its ink rather than its block, so the point may walk right up to a
      // centred line instead of stopping at the column that holds it.
      const solid = Array.from(el.querySelectorAll('[data-solid]')).map((node) => {
        const range = document.createRange()
        range.selectNodeContents(node)
        const r = range.getBoundingClientRect()
        return { left: r.left - box.left, top: r.top - box.top, right: r.right - box.left, bottom: r.bottom - box.top }
      })
      setGrid({ frameW: el.clientWidth, frameH: el.clientHeight, free: freeNodes(el.clientWidth, el.clientHeight, solid) })
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    // The display face changes what the headline covers when it lands.
    void document.fonts?.ready.then(measure)
    return () => {
      live = false
      ro.disconnect()
    }
  }, [])

  return (
    <div ref={frame} className="relative flex-1 overflow-hidden">
      <Seo title="Security audits, down to the bytecode" description="Agentic security audits that find security bugs other agents miss, pinned to the code that actually runs." path="/" />

      {/* The blueprint layer: behind everything on the page, fading over its last three rows. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 bg-[linear-gradient(var(--color-grid)_1px,transparent_1px),linear-gradient(90deg,var(--color-grid)_1px,transparent_1px)] [background-size:40px_40px] [mask-image:linear-gradient(to_bottom,black_calc(100%-120px),transparent_100%)]"
      />

      <section className="relative min-h-[560px]">
        <div className="relative mx-auto flex max-w-[1100px] flex-col items-center gap-9 px-6 pt-20 pb-16 text-center md:px-10">
          <span data-solid>
            <Mark size={112} className="text-foreground" />
          </span>
          <h1 data-solid className="font-display text-[48px] leading-[1] font-normal md:text-[68px]">
            Find security bugs <em>other agents miss.</em>
          </h1>
          <p data-solid className="-mt-3 text-[16px] text-muted-foreground md:text-[17px]">Agentic security audits, down to the bytecode.</p>
          <span data-solid className="w-full max-w-[600px]">
            <ScanForm />
          </span>
          <Link data-solid to={me ? '/app' : '/login'} className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
            {me ? 'Open dashboard' : 'Sign in'} <ArrowRight className="size-3.5" />
          </Link>
        </div>
      </section>

      <section data-solid className="relative mx-auto grid max-w-[1100px] grid-cols-2 gap-px overflow-hidden rounded-lg border border-border bg-border md:mx-10 md:grid-cols-4 lg:mx-auto">
        <Tile n="24h" k="to findings" />
        <Tile n="1:1" k="source to bytecode" />
        <Tile n="5" k="scans a day" />
        <Tile n="USDC" k="once, no subscription" />
      </section>

      <div className="h-24" />

      {/* Last, so the point and its card sit above the page it walks on. */}
      {grid.free.size > 8 && <GridBug {...grid} slug={SHOWCASE} />}
    </div>
  )
}

function Tile({ n, k }: { n: string; k: string }) {
  return (
    <div className="flex flex-col gap-2 bg-background px-6 py-6">
      <span className="num text-[28px] leading-none">{n}</span>
      <span className="label-caps">{k}</span>
    </div>
  )
}
