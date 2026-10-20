import { useEffect, useRef, useState } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import type { Wallet } from '@solana/wallet-adapter-react'
import bs58 from 'bs58'
import { api } from '@/api/client'

export class SiwsError extends Error {
  status?: number
  constructor(message: string, status?: number) {
    super(message)
    this.status = status
  }
}

// Sign-In-With-Solana against the API. Signed out, this starts a session;
// signed in, the server links the wallet to the current account.
export async function siws(publicKey: { toBase58(): string }, signMessage: (m: Uint8Array) => Promise<Uint8Array>) {
  const pk = publicKey.toBase58()
  const nonce = await api.POST('/v1/auth/siws/nonce', { body: { public_key: pk } })
  if (!nonce.data) throw new SiwsError(nonce.error?.detail ?? 'nonce', nonce.error?.status)
  const sig = await signMessage(new TextEncoder().encode(nonce.data.message))
  const res = await api.POST('/v1/auth/siws/verify', { body: { public_key: pk, signature: bs58.encode(sig), message: nonce.data.message } })
  if (!res.data) throw new SiwsError(res.error?.detail ?? 'verify', res.error?.status)
  return res.data
}

// Runs SIWS once the wallet the user picked through us connects.
export function useSiws({ onSuccess, onError }: { onSuccess: () => void | Promise<void>; onError: (e: SiwsError) => void }) {
  const wallet = useWallet()
  const [signing, setSigning] = useState(false)
  const armed = useRef(false)
  const cb = useRef({ onSuccess, onError })
  useEffect(() => {
    cb.current = { onSuccess, onError }
  })
  const { connected, publicKey, signMessage, disconnect } = wallet

  useEffect(() => {
    if (!armed.current || !connected || !publicKey || !signMessage) return
    armed.current = false
    let cancelled = false
    setSigning(true)
    siws(publicKey, signMessage)
      .then(() => {
        if (!cancelled) return cb.current.onSuccess()
      })
      .catch(async (e: unknown) => {
        if (cancelled) return
        cb.current.onError(e instanceof SiwsError ? e : new SiwsError('Wallet sign-in did not complete'))
        await disconnect().catch(() => {})
      })
      .finally(() => {
        if (!cancelled) setSigning(false)
      })
    return () => {
      cancelled = true
    }
  }, [connected, publicKey, signMessage, disconnect])

  return {
    signing,
    // Call from WalletPicker's onPicked, then connect the adapter.
    pick: async (w: Wallet) => {
      armed.current = true
      try {
        await w.adapter.connect()
      } catch {
        armed.current = false
      }
    },
  }
}
