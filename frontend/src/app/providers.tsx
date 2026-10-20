import { useEffect } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { HelmetProvider } from 'react-helmet-async'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { applyTheme, useTheme } from './theme'
import { queryClient } from './queryClient'

// Keeps the html class in step with the store on every route, public or app.
function ThemeSync() {
  const { theme } = useTheme()
  useEffect(() => applyTheme(theme), [theme])
  return null
}

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <HelmetProvider>
      <QueryClientProvider client={queryClient}>
        <ThemeSync />
        <TooltipProvider>{children}</TooltipProvider>
        <Toaster position="bottom-right" />
      </QueryClientProvider>
    </HelmetProvider>
  )
}
