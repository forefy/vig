import { Navigate, Outlet, useLocation } from 'react-router'
import { useMe } from './useMe'
import { totpFresh } from './totp'
import { Mark } from '@/components/primitives'

export function RequireAuth() {
  const { me, loading } = useMe()
  const loc = useLocation()
  if (loading) return <Loading />
  if (!me) return <Navigate to="/login" replace state={{ from: loc.pathname }} />
  return <Outlet />
}

// Admin needs the role, an enrolled authenticator, and a check within 12h.
export function RequireAdmin() {
  const { me, loading } = useMe()
  const loc = useLocation()
  if (loading) return <Loading />
  if (!me || me.user.role !== 'admin') return <Navigate to="/app" replace />
  if (!me.totp_enabled) return <Navigate to="/app/settings/security" replace state={{ from: loc.pathname }} />
  if (!totpFresh(me)) return <Navigate to="/admin/verify" replace state={{ from: loc.pathname }} />
  return <Outlet />
}

export function Loading() {
  return (
    <div className="flex min-h-[50vh] items-center justify-center">
      <Mark size={28} className="animate-[spin_6s_linear_infinite] text-faint" />
    </div>
  )
}
