import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router'
import { Eye } from 'lucide-react'
import { api } from '@/api/client'
import { queryClient } from './queryClient'
import { StateBadge } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { countdown } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import type { components } from '@/api/client'

type Me = components['schemas']['Me']

// The strip an admin sees while the session is switched to a customer. It
// ends the view early, and returns to where the admin came from when the
// server-side cap runs out.
export function ViewingAs({ me }: { me: Me }) {
  const imp = me.impersonation
  const nav = useNavigate()
  const now = useNow(1000)
  const done = useRef(false)
  const back = imp?.audit_id ? `/admin/audits/${imp.audit_id}` : `/admin/users/${imp?.user_id ?? ''}`
  const end = async () => {
    if (done.current) return
    done.current = true
    await api.POST('/v1/me/impersonation/end')
    queryClient.clear()
    nav(back)
  }
  const expired = !!imp && new Date(imp.expires_at).getTime() <= now
  useEffect(() => {
    if (expired) void end()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expired])
  if (!imp) return null
  return (
    <div className="sticky top-0 z-20 flex items-center gap-3 border-b border-warn/40 bg-warn-soft px-4 py-2 text-sm">
      <Eye className="size-4 text-warn" />
      <StateBadge state="warn">viewing as</StateBadge>
      <span className="font-medium">{me.user.display_name}</span>
      <span className="num ml-auto font-mono text-xs text-warn">{countdown(imp.expires_at, now)}</span>
      <Button size="xs" variant="outline" onClick={() => void end()}>End</Button>
    </div>
  )
}
