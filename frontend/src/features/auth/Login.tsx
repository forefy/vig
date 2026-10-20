import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { Mark } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { queryClient } from '@/app/queryClient'
import { useMe } from './useMe'
import { WalletPicker } from './wallet'
import { useSiws } from './siws'
import { toast } from 'sonner'

const apiBase = import.meta.env.VITE_API_URL ?? ''

// Three ways in. Nothing else on the page.
export function Login() {
  const { me } = useMe()
  const nav = useNavigate()
  const loc = useLocation()
  const from = (loc.state as { from?: string } | null)?.from ?? '/app'
  const [pick, setPick] = useState(false)
  const { signing, pick: pickWallet } = useSiws({
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['get', '/v1/me'] })
      nav(from, { replace: true })
    },
    onError: (e) => toast.error(e.message),
  })

  useEffect(() => {
    if (me) nav(from, { replace: true })
  }, [me, from, nav])

  return (
    <div className="flex min-h-[calc(100dvh/var(--text-scale,1))] items-center justify-center px-6">
      <Helmet>
        <title>Sign in</title>
        <meta name="robots" content="noindex" />
      </Helmet>
      <div className="flex w-full max-w-[320px] flex-col items-center gap-8">
        <Mark size={40} className={signing ? 'animate-[spin_6s_linear_infinite]' : undefined} />
        <div className="flex w-full flex-col gap-2">
          <Button size="lg" variant="outline" className="h-11 justify-start gap-3 px-4" render={<a href={`${apiBase}/v1/auth/google/start?next=${encodeURIComponent(from)}`} />}>
            <GoogleIcon /> Google
          </Button>
          <Button size="lg" variant="outline" className="h-11 justify-start gap-3 px-4" render={<a href={`${apiBase}/v1/auth/github/start?next=${encodeURIComponent(from)}`} />}>
            <GitHubIcon /> GitHub
          </Button>
          <Button size="lg" variant="outline" className="h-11 justify-start gap-3 px-4 text-wallet hover:text-wallet" disabled={signing} onClick={() => setPick(true)}>
            <WalletIcon /> {signing ? 'Signing' : 'Wallet'}
          </Button>
        </div>
        {import.meta.env.DEV && <DevLogins next={from} />}
      </div>
      <WalletPicker open={pick} onOpenChange={setPick} onPicked={(w) => void pickWallet(w)} />
    </div>
  )
}

// Seeded users, dev server only. `import.meta.env.DEV` is compiled away in
// the production bundle, and the backend answers 404 outside VIG_ENV=dev.
function DevLogins({ next }: { next: string }) {
  return (
    <div className="flex items-center gap-3 rounded-md border border-dashed border-border px-3 py-1.5">
      <span className="label-caps">dev</span>
      {['admin', 'pro', 'free'].map((u) => (
        <a key={u} href={`${apiBase}/v1/auth/dev/login?user=${u}&next=${encodeURIComponent(next)}`} className="font-mono text-xs text-muted-foreground hover:text-foreground">
          {u}
        </a>
      ))}
    </div>
  )
}

function GoogleIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden="true">
      <path fill="currentColor" d="M21.6 12.2c0-.7-.1-1.3-.2-1.9H12v3.7h5.4c-.2 1.2-.9 2.3-2 3v2.5h3.2c1.9-1.7 3-4.3 3-7.3z" opacity=".9" />
      <path fill="currentColor" d="M12 22c2.7 0 5-.9 6.6-2.4l-3.2-2.5c-.9.6-2 1-3.4 1-2.6 0-4.8-1.8-5.6-4.1H3.1v2.6C4.8 19.8 8.1 22 12 22z" opacity=".7" />
      <path fill="currentColor" d="M6.4 14c-.2-.6-.3-1.3-.3-2s.1-1.4.3-2V7.4H3.1C2.4 8.8 2 10.4 2 12s.4 3.2 1.1 4.6L6.4 14z" opacity=".5" />
      <path fill="currentColor" d="M12 5.9c1.5 0 2.8.5 3.8 1.5l2.9-2.9C17 2.9 14.7 2 12 2 8.1 2 4.8 4.2 3.1 7.4L6.4 10c.8-2.3 3-4.1 5.6-4.1z" opacity=".8" />
    </svg>
  )
}

function GitHubIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden="true">
      <path fill="currentColor" d="M12 2a10 10 0 0 0-3.2 19.5c.5.1.7-.2.7-.5v-1.8c-2.8.6-3.4-1.2-3.4-1.2-.4-1.1-1.1-1.5-1.1-1.5-.9-.6.1-.6.1-.6 1 .1 1.5 1 1.5 1 .9 1.6 2.4 1.1 3 .9.1-.7.4-1.1.6-1.4-2.2-.2-4.6-1.1-4.6-4.9 0-1.1.4-2 1-2.7-.1-.3-.4-1.3.1-2.7 0 0 .8-.3 2.8 1a9.5 9.5 0 0 1 5 0c1.9-1.3 2.7-1 2.7-1 .5 1.4.2 2.4.1 2.7.6.7 1 1.6 1 2.7 0 3.8-2.3 4.7-4.6 4.9.4.3.7.9.7 1.9v2.8c0 .3.2.6.7.5A10 10 0 0 0 12 2z" />
    </svg>
  )
}

function WalletIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
      <path d="M3 7.5A2.5 2.5 0 0 1 5.5 5H18a2 2 0 0 1 2 2v2H5.5A2.5 2.5 0 0 1 3 6.5v1z" />
      <path d="M3 7.5V17a2 2 0 0 0 2 2h15a1 1 0 0 0 1-1v-8a1 1 0 0 0-1-1H5.5A2.5 2.5 0 0 1 3 6.5" />
      <circle cx="16.5" cy="14" r="1.2" fill="currentColor" stroke="none" />
    </svg>
  )
}
