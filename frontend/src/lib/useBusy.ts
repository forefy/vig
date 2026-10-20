import { useState } from 'react'

// One in-flight action per form. `run` guards the button while it awaits.
export function useBusy() {
  const [busy, setBusy] = useState(false)
  return {
    busy,
    run: async (fn: () => Promise<void>) => {
      setBusy(true)
      try {
        await fn()
      } finally {
        setBusy(false)
      }
    },
  }
}
