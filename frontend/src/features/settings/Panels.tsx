import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { Plus, Trash2, ExternalLink } from 'lucide-react'
import { $api } from '@/api/hooks'
import { problemOf } from '@/lib/problem'
import { api, docsUrl } from '@/api/client'
import { useMe } from '@/features/auth/useMe'
import { WalletPicker } from '@/features/auth/wallet'
import { useSiws } from '@/features/auth/siws'
import { totpFresh, CODE_RE } from '@/features/auth/totp'
import { Field } from './Settings'
import { useBusy } from '@/lib/useBusy'
import { Qr } from './Qr'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { StateBadge, Secret } from '@/components/primitives'
import { ProblemBadge, SkeletonRows } from '@/components/ui/states'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { relTime, fmtDate } from '@/lib/utils'
import { toast } from 'sonner'

const apiBase = import.meta.env.VITE_API_URL ?? ''

export function ProfilePanel() {
  const { me, refetch } = useMe()
  const [name, setName] = useState(me?.user.display_name ?? '')
  const { busy, run } = useBusy()
  return (
    <div>
      <Field label="Avatar">
        <Avatar className="size-14 border border-border">
          <AvatarImage src={me?.user.avatar_url} />
          <AvatarFallback className="font-mono">{(me?.user.display_name || '?').slice(0, 1).toUpperCase()}</AvatarFallback>
        </Avatar>
      </Field>
      <Field label="Name">
        <div className="flex max-w-[420px] gap-2">
          <Input value={name} onChange={(e) => setName(e.target.value)} className="h-9" />
          <Button className="h-9" disabled={busy || name === me?.user.display_name} onClick={() => run(async () => { const r = await api.PATCH('/v1/me', { body: { display_name: name } }); if (r.error) toast.error('Not saved'); else { toast.success('Saved'); refetch() } })}>Save</Button>
        </div>
      </Field>
      <Field label="Role">
        <StateBadge state={me?.user.role === 'admin' ? 'ok' : 'neutral'} className="w-fit">{me?.user.role}</StateBadge>
      </Field>
      <Field label="Since">
        <span className="num text-sm text-muted-foreground">{fmtDate(me?.user.created_at)}</span>
      </Field>
    </div>
  )
}

const providerWord = { google: 'Google', github: 'GitHub', solana: 'Wallet', discord: 'Discord' } as const

// Sits under the wallet gate: linking a wallet signs a SIWS message while
// signed in, and the server attaches the identity to this account.
export function IdentitiesPanel() {
  const { me, refetch } = useMe()
  const [pick, setPick] = useState(false)
  const { signing, pick: pickWallet } = useSiws({
    onSuccess: async () => {
      await refetch()
      toast.success('Wallet linked')
    },
    onError: (e) => toast.error(e.message),
  })
  const ids = me?.identities ?? []
  return (
    <div>
      {(['google', 'github', 'solana', 'discord'] as const).map((p) => {
        const id = ids.find((i) => i.provider === p)
        return (
          <Field key={p} label={providerWord[p]} hint={id ? id.email || id.handle || id.provider_id : undefined}>
            <div className="flex items-center gap-2">
              {id ? (
                <>
                  <StateBadge state="ok">linked</StateBadge>
                  <span className="num text-xs text-faint">{fmtDate(id.created_at)}</span>
                  <Button size="xs" variant="ghost" disabled={ids.length === 1} onClick={async () => { const r = await api.DELETE('/v1/me/identities/{id}', { params: { path: { id: id.id } } }); if (r.error) toast.error('Could not unlink'); else refetch() }}>Unlink</Button>
                </>
              ) : p === 'solana' ? (
                <Button size="sm" variant="outline" className="text-wallet hover:text-wallet" disabled={signing} onClick={() => setPick(true)}>
                  {signing ? 'Signing' : 'Link a wallet'}
                </Button>
              ) : (
                <Button size="sm" variant="outline" render={<a href={`${apiBase}/v1/auth/${p}/start?next=${encodeURIComponent('/app/settings/identities')}`} />}>
                  Link
                </Button>
              )}
            </div>
            {p === 'github' && id && <GitHubInstallations />}
          </Field>
        )
      })}
      <WalletPicker open={pick} onOpenChange={setPick} onPicked={(w) => void pickWallet(w)} />
    </div>
  )
}

// The app installations behind a linked GitHub identity. Private repos
// are readable only through an active installation.
function GitHubInstallations() {
  const installs = $api.useQuery('get', '/v1/me/github/installations')
  const url = $api.useQuery('get', '/v1/me/github/install-url')
  const items = installs.data?.items ?? []
  const active = items.some((i) => i.status === 'active')
  if (installs.isLoading) return <SkeletonRows rows={1} />
  // An integration nobody has configured is a state, not a failure.
  // Retrying it can only fail again, so it is not offered.
  if (problemOf(installs.error).status === 503) return <StateBadge state="neutral" className="w-fit">app not configured</StateBadge>
  if (installs.isError) return <ProblemBadge error={installs.error} onRetry={() => void installs.refetch()} />
  return (
    <div className="mt-1 flex flex-col gap-2">
      {items.length === 0 ? (
        <div className="flex items-center gap-2">
          <StateBadge state="neutral">private repos unreadable</StateBadge>
          <Button size="xs" variant="outline" render={<a href={url.data?.url ?? '#'} />} disabled={!url.data?.url}>Install app <ExternalLink className="size-3.5" /></Button>
        </div>
      ) : (
        <>
          <StateBadge state={active ? 'ok' : 'neutral'} className="w-fit">{active ? 'private repos readable' : 'private repos unreadable'}</StateBadge>
          <div className="divide-y divide-border rounded-md border border-border">
            {items.map((i) => (
              <div key={i.id} className="flex items-center justify-between gap-3 px-3.5 py-2 text-sm">
                <span className="font-mono">{i.account_login}</span>
                <span className="flex items-center gap-2">
                  <StateBadge state="neutral">{i.account_type}</StateBadge>
                  <StateBadge state={i.status === 'active' ? 'ok' : i.status === 'pending' ? 'warn' : 'bad'}>{i.status}</StateBadge>
                </span>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

export function NotificationsPanel() {
  const { me } = useMe()
  const discord = me?.identities?.find((i) => i.provider === 'discord')
  const email = me?.identities?.find((i) => i.email)?.email
  return (
    <div>
      <Field label="In app">
        <StateBadge state="ok" className="w-fit">always</StateBadge>
      </Field>
      <Field label="Discord" hint={discord?.handle}>
        {discord ? (
          <StateBadge state="ok" className="w-fit">connected</StateBadge>
        ) : (
          <Button size="sm" variant="outline" className="w-fit" render={<a href={`${apiBase}/v1/auth/discord/start?next=${encodeURIComponent('/app/settings/notifications')}`} />}>Connect</Button>
        )}
      </Field>
      <Field label="Email" hint={email}>
        <div className="flex items-center gap-3">
          {/* No preference endpoint in the API yet: the switch states the fact and cannot move. */}
          <Switch disabled checked={!!email} aria-label="Email notifications" />
          <StateBadge state={email ? 'neutral' : 'pending'}>{email ? 'on' : 'no email'}</StateBadge>
        </div>
      </Field>
    </div>
  )
}

// TOTP enrolment and status. Admins need a fresh check to reach /admin.
export function SecurityPanel() {
  const { me, refetch } = useMe()
  const nav = useNavigate()
  const loc = useLocation()
  const from = (loc.state as { from?: string } | null)?.from
  const [setup, setSetup] = useState<{ secret: string; otpauth_url: string } | null>(null)
  const [code, setCode] = useState('')
  const [error, setError] = useState<unknown>(null)
  const { busy, run } = useBusy()
  const enabled = !!me?.totp_enabled
  const fresh = !!me && totpFresh(me)

  return (
    <div>
      <Field label="Authenticator">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={enabled ? 'ok' : 'neutral'}>{enabled ? 'enabled' : 'off'}</StateBadge>
          {enabled && (
            <StateBadge state={fresh ? 'ok' : 'pending'}>{me?.totp_verified_at ? `verified ${relTime(me.totp_verified_at)}` : 'not verified'}</StateBadge>
          )}
          {!enabled && !setup && (
            <Button size="sm" disabled={busy} onClick={() => run(async () => {
              setError(null)
              const r = await api.POST('/v1/me/totp/setup')
              if (!r.data) setError(r.error)
              else setSetup(r.data)
            })}>Enrol</Button>
          )}
        </div>
        {error != null && !setup && <ProblemBadge error={error} className="w-fit" />}
      </Field>
      {setup && !enabled && (
        <>
          <Field label="Scan">
            <div className="flex flex-wrap items-start gap-5">
              <div className="rounded-md border border-border bg-white p-2">
                <Qr value={setup.otpauth_url} />
              </div>
              <div className="flex min-w-0 flex-col gap-2">
                <span className="label-caps">secret</span>
                <Secret value={setup.secret} className="max-w-[360px]" />
              </div>
            </div>
          </Field>
          <Field label="First code">
            <form
              className="flex max-w-[320px] flex-col gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                if (!CODE_RE.test(code)) return
                void run(async () => {
                  setError(null)
                  const r = await api.POST('/v1/me/totp/enable', { body: { code } })
                  if (!r.data) {
                    setError(r.error)
                    setCode('')
                    return
                  }
                  setSetup(null)
                  setCode('')
                  await refetch()
                  toast.success('Authenticator enabled')
                  if (from) nav(from, { replace: true })
                })
              }}
            >
              <div className="flex gap-2">
                <Input aria-label="Code" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" maxLength={6} className="h-9 font-mono tracking-[0.3em]" />
                <Button type="submit" className="h-9" disabled={busy || !CODE_RE.test(code)}>Enable</Button>
              </div>
              {error != null && <ProblemBadge error={error} />}
            </form>
          </Field>
        </>
      )}
    </div>
  )
}

export function ApiKeysPanel() {
  const keys = $api.useQuery('get', '/v1/me/api-keys')
  const [name, setName] = useState('')
  const [created, setCreated] = useState<string | null>(null)
  const { busy, run } = useBusy()
  const items = keys.data?.items ?? []
  return (
    <div>
      <Field label="New key">
        <div className="flex max-w-[420px] gap-2">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="name" className="h-9" />
          <Button className="h-9" disabled={busy || !name.trim()} onClick={() => run(async () => { const r = await api.POST('/v1/me/api-keys', { body: { name: name.trim() } }); if (!r.data) toast.error('Could not create'); else { setCreated(r.data.secret); setName(''); keys.refetch() } })}>
            <Plus className="size-4" /> Create
          </Button>
        </div>
      </Field>
      <Field label="Reference">
        <Button variant="outline" size="sm" className="w-fit" render={<a href={docsUrl} target="_blank" rel="noreferrer" />}>
          <ExternalLink className="size-4" /> API reference
        </Button>
      </Field>
      <Field label="Keys">
        {keys.isLoading ? (
          <SkeletonRows rows={2} />
        ) : keys.isError ? (
          <ProblemBadge error={keys.error} onRetry={() => void keys.refetch()} />
        ) : items.length === 0 ? (
          <StateBadge state="neutral" className="w-fit">none</StateBadge>
        ) : (
          <div className="divide-y divide-border rounded-md border border-border">
            {items.map((k) => (
              <div key={k.id} className="grid grid-cols-[1fr_auto_auto_auto] items-center gap-3 px-3.5 py-2.5 text-sm">
                <span>{k.name}</span>
                <span className="font-mono text-xs text-muted-foreground">{k.prefix}…</span>
                <span className="num text-xs text-faint">{k.last_used_at ? relTime(k.last_used_at) : 'unused'}</span>
                <Button size="icon-xs" variant="ghost" aria-label="Revoke" onClick={async () => { await api.DELETE('/v1/me/api-keys/{id}', { params: { path: { id: k.id } } }); keys.refetch() }}>
                  <Trash2 className="size-3.5" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </Field>
      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogTitle className="label-caps">Shown once</DialogTitle>
          {created && <Secret value={created} />}
        </DialogContent>
      </Dialog>
    </div>
  )
}

export function DataPanel() {
  return (
    <div>
      <Field label="Code">
        <StateBadge state="neutral" className="w-fit">deleted on delivery</StateBadge>
      </Field>
      <Field label="Reports">
        <StateBadge state="neutral" className="w-fit">retained</StateBadge>
      </Field>
      <Field label="Account">
        {/* No DELETE /v1/me in the API yet; the control exists, and cannot fire. */}
        <Button size="sm" variant="destructive" className="w-fit" disabled>Delete account</Button>
      </Field>
    </div>
  )
}
