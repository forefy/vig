import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { api } from '@/api/client'
import { queryClient } from '@/app/queryClient'
import { useMe } from '@/features/auth/useMe'
import { totpFresh, CODE_RE } from '@/features/auth/totp'
import { Mark, StateBadge } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ProblemBadge } from '@/components/ui/states'

// The gate in front of /admin: one six-digit code, then on to where you were.
export function AdminVerify() {
  const { me } = useMe()
  const nav = useNavigate()
  const loc = useLocation()
  const from = (loc.state as { from?: string } | null)?.from ?? '/admin'
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  useEffect(() => {
    if (me && me.user.role === 'admin' && totpFresh(me)) nav(from, { replace: true })
  }, [me, from, nav])

  async function submit() {
    if (!CODE_RE.test(code) || busy) return
    setBusy(true)
    setError(null)
    const r = await api.POST('/v1/me/totp/verify', { body: { code } })
    setBusy(false)
    if (r.error) {
      setError(r.error)
      setCode('')
      return
    }
    await queryClient.invalidateQueries({ queryKey: ['get', '/v1/me'] })
    nav(from, { replace: true })
  }

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6">
      <Helmet>
        <title>Verify</title>
      </Helmet>
      <form
        className="flex w-full max-w-[280px] flex-col items-center gap-6"
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        <Mark size={36} className={busy ? 'animate-[spin_6s_linear_infinite]' : undefined} />
        <div className="flex items-center gap-2">
          <span className="label-caps">admin</span>
          <StateBadge state="pending">totp</StateBadge>
        </div>
        <Input
          aria-label="Code"
          value={code}
          onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
          inputMode="numeric"
          autoComplete="one-time-code"
          autoFocus
          maxLength={6}
          spellCheck={false}
          className="h-12 text-center font-mono text-2xl tracking-[0.4em]"
        />
        {error != null && <ProblemBadge error={error} className="w-full" />}
        <Button type="submit" className="h-10 w-full" disabled={!CODE_RE.test(code) || busy}>
          Verify
        </Button>
      </form>
    </div>
  )
}
