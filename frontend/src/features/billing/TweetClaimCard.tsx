import { useState } from 'react'
import { ExternalLink } from 'lucide-react'
import { $api } from '@/api/hooks'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { StateBadge, Copy } from '@/components/primitives'
import { toast } from 'sonner'

const claimState = { submitted: 'pending', approved: 'ok', rejected: 'bad', revoked: 'bad' } as const

// One free audit for a post. The nonce is what ties the post to the account.
export function TweetClaimCard() {
  const nonce = $api.useQuery('get', '/v1/tweet-claims/nonce')
  const claims = $api.useQuery('get', '/v1/tweet-claims')
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const latest = claims.data?.items?.[0]
  const pending = latest?.state === 'submitted'
  const done = latest?.state === 'approved'
  if (done) return null

  async function submit() {
    setBusy(true)
    const res = await api.POST('/v1/tweet-claims', { body: { url: url.trim() } })
    setBusy(false)
    if (!res.data) {
      toast.error((res.error as { detail?: string } | undefined)?.detail ?? 'Link not accepted')
      return
    }
    setUrl('')
    claims.refetch()
  }

  const text = nonce.data?.text ?? ''
  const intent = `https://x.com/intent/post?text=${encodeURIComponent(text)}`

  return (
    <div className="grid grid-cols-1 gap-6 rounded-md border border-primary/30 bg-primary/[0.03] p-5 md:grid-cols-[1fr_auto]">
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-3">
          <span className="label-caps text-primary">free audit</span>
          {latest && <StateBadge state={claimState[latest.state]}>{latest.state}</StateBadge>}
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-sm">Post, include your code, paste the link.</span>
          {nonce.data && <Copy value={nonce.data.nonce} className="rounded-sm border border-border px-2 py-1 text-foreground">{nonce.data.nonce}</Copy>}
        </div>
        {!pending && (
          <div className="flex gap-2">
            <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://x.com/you/status/…" spellCheck={false} className="h-9 max-w-[420px] font-mono text-xs" />
            <Button size="sm" className="h-9" disabled={busy || !/^https:\/\/(x\.com|twitter\.com)\/[^/]+\/status\/\d+/.test(url.trim())} onClick={() => void submit()}>Submit</Button>
          </div>
        )}
        {pending && <span className="text-xs text-muted-foreground">Reviewed within 24h.</span>}
        {latest?.state === 'rejected' && <span className="text-xs text-bad">Not accepted. Post again with your code and resubmit.</span>}
      </div>
      <div className="flex items-start">
        <Button variant="outline" size="sm" render={<a href={intent} target="_blank" rel="noreferrer" />} disabled={!text}>
          Post <ExternalLink className="size-3.5" />
        </Button>
      </div>
    </div>
  )
}
