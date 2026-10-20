import { QueryClient } from '@tanstack/react-query'

// One cache for the app. Lives outside providers.tsx so pages can reach it
// without importing a component module.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 15_000, refetchOnWindowFocus: false },
  },
})
