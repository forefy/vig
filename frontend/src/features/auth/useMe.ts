import { $api } from '@/api/hooks'

// The signed-in user, or null when the session is missing. Cached app-wide.
export function useMe() {
  const q = $api.useQuery('get', '/v1/me', {}, { retry: false, staleTime: 60_000 })
  const unauthenticated = q.error != null && (q.error as { status?: number }).status === 401
  return { me: unauthenticated ? null : (q.data ?? null), loading: q.isLoading, refetch: q.refetch, error: unauthenticated ? null : q.error }
}
