import { useMemo } from 'react'
import { ConnectionProvider, WalletProvider, useWallet } from '@solana/wallet-adapter-react'
import type { Wallet } from '@solana/wallet-adapter-react'
import { WalletReadyState } from '@solana/wallet-adapter-base'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'

// Wallet-standard wallets (Phantom, Solflare, Backpack, MetaMask Solana) are
// auto-detected; no per-wallet adapters are registered.
export function SolanaProvider({ children }: { children: React.ReactNode }) {
  const endpoint = useMemo(() => import.meta.env.VITE_RPC_URL ?? 'https://api.mainnet-beta.solana.com', [])
  return (
    <ConnectionProvider endpoint={endpoint}>
      <WalletProvider wallets={[]} autoConnect={false}>
        {children}
      </WalletProvider>
    </ConnectionProvider>
  )
}

// Where to get one when none is installed. Names only, no icons.
const installs = [
  { name: 'Phantom', href: 'https://phantom.com/download' },
  { name: 'Solflare', href: 'https://solflare.com/download' },
  { name: 'Backpack', href: 'https://backpack.app/download' },
  { name: 'MetaMask', href: 'https://metamask.io/download' },
]

// Our own picker so the wallet modal speaks the same design language.
export function WalletPicker({ open, onOpenChange, onPicked }: { open: boolean; onOpenChange: (o: boolean) => void; onPicked?: (w: Wallet) => void }) {
  const { wallets, select } = useWallet()
  const ready = wallets.filter((w) => w.readyState === WalletReadyState.Installed || w.readyState === WalletReadyState.Loadable)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm gap-2 p-2">
        <DialogTitle className="label-caps px-2 pt-2 pb-1">Wallet</DialogTitle>
        {ready.length === 0 && (
          <div className="flex flex-col gap-1 px-2 pb-2">
            <span className="label-caps py-2 text-[10px]">install</span>
            {installs.map((i) => (
              <a key={i.name} href={i.href} target="_blank" rel="noreferrer" className="flex items-center justify-between rounded-md px-2 py-2 font-mono text-xs text-muted-foreground hover:bg-accent hover:text-foreground">
                {i.name}
                <span className="text-faint">{new URL(i.href).host}</span>
              </a>
            ))}
          </div>
        )}
        {ready.map((w) => (
          <button
            key={w.adapter.name}
            type="button"
            onClick={() => {
              select(w.adapter.name)
              onPicked?.(w)
              onOpenChange(false)
            }}
            className={cn('flex items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm hover:bg-accent')}
          >
            <img src={w.adapter.icon} alt="" className="size-6 rounded-sm" />
            <span className="flex-1">{w.adapter.name}</span>
            <span className="label-caps text-[10px]">detected</span>
          </button>
        ))}
      </DialogContent>
    </Dialog>
  )
}
