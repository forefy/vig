import { lazy, Suspense } from 'react'
import { Outlet } from 'react-router'
import { Loading } from './guards'

// The Solana provider is heavy. Only routes that sign something sit under it:
// /login, the wizard, billing, and wallet linking in settings.
const SolanaProvider = lazy(async () => ({ default: (await import('./wallet')).SolanaProvider }))

export function WalletGate() {
  return (
    <Suspense fallback={<Loading />}>
      <SolanaProvider>
        <Outlet />
      </SolanaProvider>
    </Suspense>
  )
}
