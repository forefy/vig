import { useState } from 'react'
import { useNavigate } from 'react-router'
import { ArrowRight } from 'lucide-react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { toast } from 'sonner'

const repoRe = /^https:\/\/github\.com\/[\w.-]+\/[\w.-]+(\/tree\/[^\s]+)?\/?$/
// A Solana program id or an EVM contract address; the backend reads the shape.
const pidRe = /^([1-9A-HJ-NP-Za-km-z]{32,44}|0x[0-9a-fA-F]{40})$/

// The one input. A program id field appears once the URL validates.
// Submitting starts a scan and moves to its result page.
export function ScanForm({ autoFocus = false, className }: { autoFocus?: boolean; className?: string }) {
  const nav = useNavigate()
  const [url, setUrl] = useState('')
  const [pid, setPid] = useState('')
  const [busy, setBusy] = useState(false)
  const urlOk = repoRe.test(url.trim())
  const pidOk = pid.trim() === '' || pidRe.test(pid.trim())

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!urlOk || !pidOk || busy) return
    setBusy(true)
    const res = await api.POST('/v1/scans', { body: { repo_url: url.trim(), program_id: pid.trim() || undefined } })
    setBusy(false)
    if (res.error || !res.data) {
      toast.error((res.error as { detail?: string } | undefined)?.detail ?? 'Scan could not start')
      return
    }
    nav(`/s/${res.data.token}`)
  }

  return (
    <form onSubmit={submit} className={cn('flex w-full flex-col gap-2', className)}>
      <div className={cn('flex h-14 items-center gap-2 rounded-md border bg-card pr-2 pl-4 transition-colors focus-within:border-primary', url && !urlOk ? 'border-bad' : 'border-border')}>
        <span className="label-caps hidden sm:inline">github</span>
        <input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://github.com/org/repo"
          spellCheck={false}
          autoComplete="off"
          autoFocus={autoFocus}
          aria-label="GitHub repository URL"
          className="h-full min-w-0 flex-1 bg-transparent font-mono text-sm outline-none placeholder:text-faint"
        />
        <Button type="submit" size="lg" disabled={!urlOk || !pidOk || busy} className="h-10 px-4">
          Scan <ArrowRight className="size-4" />
        </Button>
      </div>
      <div
        className={cn(
          'flex h-11 items-center gap-2 overflow-hidden rounded-md border bg-card pl-4 transition-all',
          urlOk ? 'max-h-11 opacity-100' : 'pointer-events-none max-h-0 border-transparent opacity-0',
          pid && !pidOk ? 'border-bad' : 'border-border focus-within:border-primary',
        )}
      >
        <span className="label-caps hidden sm:inline">target</span>
        <input
          value={pid}
          onChange={(e) => setPid(e.target.value)}
          placeholder="program id or contract address"
          spellCheck={false}
          autoComplete="off"
          tabIndex={urlOk ? 0 : -1}
          aria-label="Program id"
          className="h-full min-w-0 flex-1 bg-transparent font-mono text-sm outline-none placeholder:text-faint"
        />
      </div>
    </form>
  )
}
