import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { useWallet, useConnection } from '@solana/wallet-adapter-react'
import { PublicKey, Transaction, TransactionInstruction } from '@solana/web3.js'
import { Buffer } from 'buffer'
import { createTransferCheckedInstruction, getAssociatedTokenAddressSync } from '@solana/spl-token'
import { $api } from '@/api/hooks'
import { api } from '@/api/client'
import { useMe } from '@/features/auth/useMe'
import { Page, PageHeader } from '@/app/Shell'
import { Stat, StateBadge, Empty, type BadgeState } from '@/components/primitives'
import { ProblemBadge, SkeletonRows } from '@/components/ui/states'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { WalletPicker } from '@/features/auth/wallet'
import { TweetClaimCard } from './TweetClaimCard'
import { usdc, countdown, relTime, short, fmtDate, cn } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { problemDetail } from '@/lib/problem'
import { toast } from 'sonner'
import type { components } from '@/api/client'

type Plan = components['schemas']['Plan']
type Purchase = components['schemas']['Purchase']
const MEMO_PROGRAM = new PublicKey('MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr')
const contactEmail = import.meta.env.VITE_CONTACT_EMAIL ?? ''

export const PURCHASE_POLL_MS = 5_000
const terminal = new Set(['confirmed', 'failed', 'expired'])
const purchaseTone: Record<Purchase['state'], BadgeState> = { pending: 'pending', confirmed: 'ok', failed: 'bad', expired: 'warn' }
const openStates = new Set(['queued', 'claimed', 'cloning', 'programs_verified', 'analysis', 'human_review', 'delayed'])

// Ledger archetype. The pay button always says the total.
export function Billing() {
  const { me, refetch } = useMe()
  const now = useNow(1000)
  const plans = $api.useQuery('get', '/v1/plans')
  const ledger = $api.useQuery('get', '/v1/me/ledger')
  const audits = $api.useQuery('get', '/v1/audits')
  const [sku, setSku] = useState<'mini' | 'pro'>('pro')
  const [promo, setPromo] = useState('')
  const [pick, setPick] = useState(false)
  const [paying, setPaying] = useState(false)
  const [purchaseId, setPurchaseId] = useState<string | null>(null)
  const wallet = useWallet()
  const { connection } = useConnection()
  const planItems = useMemo(() => plans.data?.items ?? [], [plans.data])
  const plan = useMemo(() => planItems.find((p) => p.sku === sku), [planItems, sku])
  const buyable = planItems.filter((p) => p.key !== 'free')

  // After a signature the purchase is watched until the chain settles it.
  const purchase = $api.useQuery('get', '/v1/purchases/{id}', { params: { path: { id: purchaseId ?? '' } } }, {
    enabled: !!purchaseId,
    refetchInterval: (q) => (q.state.data && terminal.has(q.state.data.state) ? false : PURCHASE_POLL_MS),
  })
  const purchaseState = purchase.data?.state
  useEffect(() => {
    if (purchaseState === 'confirmed') {
      refetch()
      ledger.refetch()
    }
  }, [purchaseState, refetch, ledger])

  async function pay() {
    if (!plan) return
    if (!wallet.connected || !wallet.publicKey || !wallet.sendTransaction) {
      setPick(true)
      return
    }
    setPaying(true)
    try {
      const p = await api.POST('/v1/purchases', { body: { sku, promo: promo.trim() || undefined } })
      if (!p.data) throw new Error(problemDetail(p.error, 'Purchase could not start'))
      const mint = new PublicKey(p.data.usdc_mint)
      const treasury = new PublicKey(p.data.treasury)
      const from = getAssociatedTokenAddressSync(mint, wallet.publicKey)
      const to = getAssociatedTokenAddressSync(mint, treasury, true)
      const tx = new Transaction()
      tx.add(new TransactionInstruction({ keys: [], programId: MEMO_PROGRAM, data: Buffer.from(p.data.memo_nonce, 'utf8') }))
      tx.add(createTransferCheckedInstruction(from, mint, to, wallet.publicKey, BigInt(p.data.amount_usdc), 6))
      const sig = await wallet.sendTransaction(tx, connection)
      setPurchaseId(p.data.id)
      const c = await api.POST('/v1/purchases/{id}/confirm', { params: { path: { id: p.data.id } }, body: { tx_signature: sig } })
      if (c.error) toast.error(problemDetail(c.error, 'Not confirmed'))
      void purchase.refetch()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setPaying(false)
    }
  }

  const credits = me?.credits ?? 0
  const active = me?.plan
  const slotOpen = active ? new Date(active.next_slot_at).getTime() <= now : false
  const waiting = (audits.data?.items ?? []).filter((a) => openStates.has(a.state)).length

  return (
    <Page wide>
      <Helmet>
        <title>Billing</title>
      </Helmet>
      <PageHeader eyebrow="billing" title="Credits" />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="credits" value={credits} />
        {active ? (
          <>
            <Stat label="plan" value={active.kind} hint={`until ${fmtDate(active.ends_at)}`} tone="ok" />
            <Stat label="next run" value={slotOpen ? 'now' : countdown(active.next_slot_at, now)} tone={slotOpen ? 'ok' : undefined} />
          </>
        ) : (
          <Stat label="plan" value="none" className="md:col-span-2" />
        )}
        {audits.isLoading ? (
          <Skeleton className="h-[86px]" />
        ) : audits.isError ? (
          <Stat label="waiting" value="-" tone="bad"><ProblemBadge error={audits.error} onRetry={() => void audits.refetch()} /></Stat>
        ) : (
          <Stat label="waiting" value={waiting} tone={waiting > 0 ? 'warn' : undefined}>
            {waiting > 0 && <Link to="/app/audits" className="w-fit font-mono text-[11px] text-muted-foreground hover:text-foreground">audits</Link>}
          </Stat>
        )}
      </div>

      {credits === 0 && !active && (
        <div className="mt-6">
          <TweetClaimCard />
        </div>
      )}

      <div className="mt-10 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <section className="flex flex-col gap-3">
          <span className="label-caps">packs</span>
          {plans.isLoading ? (
            <SkeletonRows rows={3} />
          ) : plans.isError ? (
            <ProblemBadge error={plans.error} onRetry={() => void plans.refetch()} className="w-fit" />
          ) : buyable.length === 0 ? (
            <Empty title="No packs" />
          ) : (
            <div className="flex flex-col gap-2">
              {buyable.map((p) => (
                <PlanRow key={p.key} plan={p} selected={p.sku === sku} onSelect={() => p.sku && setSku(p.sku)} />
              ))}
            </div>
          )}
        </section>
        <aside className="lg:sticky lg:top-8 lg:self-start">
          <div className="flex flex-col gap-4 rounded-md border border-border bg-card p-5">
            <span className="label-caps">checkout</span>
            <div className="flex items-baseline gap-2">
              <span className="num text-3xl">{plan ? usdc(plan.price_usdc, { compact: true }) : '-'}</span>
              <span className="text-xs text-muted-foreground">USDC</span>
            </div>
            {plan && (
              <div className="flex flex-wrap gap-1.5">
                <Fact label={plan.term_days ? 'days' : 'audits'} value={plan.term_days ?? plan.credits ?? 0} />
                {plan.runs_per_day ? <Fact label="runs / day" value={plan.runs_per_day} /> : null}
                <Fact label="programs" value={plan.max_programs} />
                <Fact label="lines" value={`${Math.round(plan.max_lines / 1000)}k`} />
              </div>
            )}
            <Input value={promo} onChange={(e) => setPromo(e.target.value)} placeholder="promo" spellCheck={false} className="h-9 font-mono text-xs" />
            <Button size="lg" className="h-11 w-full bg-wallet text-background hover:bg-wallet/90" disabled={!plan || paying} onClick={() => void pay()}>
              {paying ? 'Waiting for the wallet' : wallet.connected ? `Pay ${plan ? usdc(plan.price_usdc, { compact: true }) : ''}` : 'Select wallet'}
            </Button>
            {purchaseId && (
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-[11px] text-faint">{short(purchaseId, 8, 4)}</span>
                {purchase.isError ? (
                  <ProblemBadge error={purchase.error} onRetry={() => void purchase.refetch()} />
                ) : purchase.data ? (
                  <StateBadge state={purchaseTone[purchase.data.state]}>{purchase.data.state}{purchase.data.state === 'pending' ? ` · ${countdown(purchase.data.expires_at, now)}` : ''}</StateBadge>
                ) : (
                  <StateBadge state="pending">pending</StateBadge>
                )}
              </div>
            )}
            <div className="flex items-center justify-between text-[11px] text-muted-foreground">
              <span>Billed once, no refunds.</span>
              <Link to="/terms" className="font-mono hover:text-foreground">terms</Link>
            </div>
            {wallet.connected && wallet.publicKey && (
              <button type="button" onClick={() => wallet.disconnect()} className="text-center font-mono text-[11px] text-faint hover:text-foreground">{short(wallet.publicKey.toBase58(), 4, 4)} · disconnect</button>
            )}
          </div>
        </aside>
      </div>

      <section className="mt-12 flex flex-col gap-3">
        <span className="label-caps">ledger</span>
        {ledger.isLoading ? (
          <SkeletonRows rows={3} />
        ) : ledger.isError ? (
          <ProblemBadge error={ledger.error} onRetry={() => void ledger.refetch()} className="w-fit" />
        ) : (ledger.data?.items ?? []).length === 0 ? (
          <Empty title="No movements yet" />
        ) : (
          <div className="overflow-x-auto rounded-md border border-border bg-card">
            <table className="w-full min-w-[520px] text-sm">
              <tbody className="divide-y divide-border">
                {(ledger.data?.items ?? []).map((row) => (
                  <tr key={row.id}>
                    <td className="px-4 py-2.5"><StateBadge state={row.delta > 0 ? 'ok' : 'neutral'}>{row.kind}</StateBadge></td>
                    <td className="px-4 py-2.5 text-xs text-muted-foreground">{row.reason ?? row.ref_kind ?? ''}</td>
                    <td className={cn('num px-4 py-2.5 text-right', row.delta > 0 ? 'text-ok' : '')}>{row.delta > 0 ? `+${row.delta}` : row.delta}</td>
                    <td className="num px-4 py-2.5 text-right text-muted-foreground">{row.balance_after}</td>
                    <td className="num px-4 py-2.5 text-right text-xs text-faint">{relTime(row.created_at, now)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      <WalletPicker open={pick} onOpenChange={setPick} onPicked={async (w) => { try { await w.adapter.connect() } catch { /* closed */ } }} />
    </Page>
  )
}

// A cap as a fact: the number in mono, the label after it.
function Fact({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <span className="inline-flex h-6 items-center gap-1.5 rounded-sm border border-border px-2 font-mono text-[11.5px] whitespace-nowrap">
      <span className="num text-foreground">{value}</span>
      <span className="text-muted-foreground">{label}</span>
    </span>
  )
}

function PlanRow({ plan, selected, onSelect }: { plan: Plan; selected: boolean; onSelect: () => void }) {
  const contact = plan.gate === 'contact'
  if (contact && !contactEmail) return null
  return (
    <button
      type="button"
      onClick={contact ? undefined : onSelect}
      aria-pressed={selected}
      className={cn('grid grid-cols-[1fr_auto] items-center gap-4 rounded-md border px-5 py-4 text-left transition-colors', selected ? 'border-primary bg-primary/5' : 'border-border bg-card hover:border-foreground/25', contact && 'cursor-default hover:border-border')}
    >
      <div className="flex flex-col gap-2">
        <span className={cn('text-[15px] font-medium', selected && 'text-primary')}>{plan.name}</span>
        <div className="flex flex-wrap gap-1.5">
          {plan.runs_per_day ? <Fact label="runs / day" value={plan.runs_per_day} /> : <Fact label="audits" value={plan.credits ?? 0} />}
          {plan.term_days ? <Fact label="days" value={plan.term_days} /> : null}
          <Fact label="programs" value={plan.max_programs} />
          <Fact label="lines" value={`${Math.round(plan.max_lines / 1000)}k`} />
        </div>
      </div>
      <div className="flex flex-col items-end gap-0.5">
        {contact ? (
          <a href={`mailto:${contactEmail}?subject=${encodeURIComponent(plan.name)}`} className="text-sm text-primary hover:underline" onClick={(e) => e.stopPropagation()}>Talk to us</a>
        ) : (
          <>
            <span className="num text-xl">{plan.display_monthly_usdc ? usdc(plan.display_monthly_usdc, { compact: true }) : usdc(plan.price_usdc, { compact: true })}</span>
            <span className="text-xs text-muted-foreground">{plan.display_monthly_usdc ? `/ month · ${usdc(plan.price_usdc, { compact: true })} once` : 'once'}</span>
          </>
        )}
      </div>
    </button>
  )
}
