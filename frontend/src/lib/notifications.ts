import type { components } from '@/api/client'

export type Notification = components['schemas']['Notification']

export function unreadCount(items: Notification[] | null | undefined): number {
  return (items ?? []).filter((n) => !n.read_at).length
}
