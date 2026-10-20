import { useEffect, useRef, useState } from 'react'
import { Link, Outlet } from 'react-router'
import { Sun, Moon, Accessibility, Minus, Plus } from 'lucide-react'
import { Mark } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/useMe'
import { useTheme } from './theme'
import { Switch } from '@/components/ui/switch'
import { useA11y } from './a11y'

// Public archetype: a thin bar, the page, a quiet footer.
export function PublicShell() {
  const { me } = useMe()
  const { theme, toggle } = useTheme()
  return (
    <div className="flex min-h-[calc(100dvh/var(--text-scale,1))] flex-col">
      <header className="flex items-center justify-between px-6 py-5 md:px-10">
        <Link to="/" aria-label="Home" className="text-foreground">
          <Mark size={26} />
        </Link>
        <nav className="flex items-center gap-2">
          {me ? (
            <Button variant="outline" size="sm" render={<Link to="/app" />}>
              Dashboard
            </Button>
          ) : (
            <Button variant="outline" size="sm" render={<Link to="/login" />}>
              Sign in
            </Button>
          )}
        </nav>
      </header>
      <main className="flex flex-1 flex-col">
        <Outlet />
      </main>
      <footer className="flex flex-wrap items-center justify-between gap-3 px-6 py-6 text-xs text-faint md:px-10">
        <span className="num">{new Date().getFullYear()}</span>
        <div className="flex items-center gap-4">
          <Link to="/terms" className="hover:text-foreground">Terms</Link>
          <Link to="/privacy" className="hover:text-foreground">Privacy</Link>
          <ReaderSettings />
          <button type="button" onClick={toggle} aria-label="Toggle theme" className="flex size-6 items-center justify-center rounded-md hover:text-foreground">
            {theme === 'dark' ? <Sun className="size-3.5" /> : <Moon className="size-3.5" />}
          </button>
        </div>
      </footer>
    </div>
  )
}

// Reader settings: the accessibility icon opens a small card above it with
// text size, contrast and motion. Anchored in the footer's own DOM rather
// than floated, so a zoom change under it leaves it where it is.
function ReaderSettings() {
  const a = useA11y()
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLSpanElement>(null)
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])
  return (
    <span ref={root} className="relative flex items-center">
      <button
        type="button"
        aria-label="Reader settings"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={`flex size-6 items-center justify-center rounded-md hover:text-foreground ${open || a.changed ? 'text-foreground' : ''}`}
      >
        <Accessibility className="size-3.5" />
      </button>
      {open && (
        <div role="dialog" aria-label="Reader settings" className="absolute right-0 bottom-full z-50 mb-3 w-56 rounded-lg border border-border bg-popover p-3.5 text-popover-foreground shadow-lg">
          <div className="grid grid-cols-[1fr_auto] items-center gap-x-5 gap-y-3">
            <span className="label-caps text-faint">text</span>
            <span className="flex items-center gap-0.5 rounded-md border border-border">
              <button type="button" aria-label="Smaller text" disabled={a.min} onClick={() => a.step(-1)} className="flex size-6 items-center justify-center rounded-md text-muted-foreground hover:text-foreground disabled:opacity-40">
                <Minus className="size-3.5" />
              </button>
              <button type="button" onClick={a.resetScale} aria-label="Reset text size" className="num min-w-11 px-1 font-mono text-[11.5px] text-foreground">
                {Math.round(a.scale * 100)}%
              </button>
              <button type="button" aria-label="Larger text" disabled={a.max} onClick={() => a.step(1)} className="flex size-6 items-center justify-center rounded-md text-muted-foreground hover:text-foreground disabled:opacity-40">
                <Plus className="size-3.5" />
              </button>
            </span>
            <span className="label-caps text-faint">contrast</span>
            <span className="flex items-center gap-2">
              <span className="font-mono text-[11px] text-muted-foreground">{a.contrast ? 'high' : 'normal'}</span>
              <Switch size="sm" aria-label="High contrast" checked={a.contrast} onCheckedChange={a.setContrast} />
            </span>
            <span className="label-caps text-faint">motion</span>
            <span className="flex items-center gap-2">
              <span className="font-mono text-[11px] text-muted-foreground">{a.reduceMotion ? 'reduced' : 'full'}</span>
              <Switch size="sm" aria-label="Reduce motion" checked={a.reduceMotion} onCheckedChange={a.setReduceMotion} />
            </span>
          </div>
          <span aria-hidden="true" className="absolute right-[7px] -bottom-[5px] size-2.5 rotate-45 border-r border-b border-border bg-popover" />
        </div>
      )}
    </span>
  )
}
