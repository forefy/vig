import type { components } from '@/api/client'

export type Notification = components['schemas']['Notification']

export function unreadCount(items: Notification[] | null | undefined): number {
  return (items ?? []).filter((n) => !n.read_at).length
}

// A group is every notification of one kind, newest first: the bell shows
// one row for forty of a kind, with the count, and opens to each of them.
export type NotificationGroup = { kind: string; items: Notification[]; unread: number; newest: string }

export function groupByKind(items: Notification[] | null | undefined): NotificationGroup[] {
  const by = new Map<string, Notification[]>()
  for (const n of items ?? []) {
    const list = by.get(n.kind)
    if (list) list.push(n)
    else by.set(n.kind, [n])
  }
  return [...by.entries()]
    .map(([kind, list]) => {
      const sorted = [...list].sort((a, b) => (a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0))
      return { kind, items: sorted, unread: unreadCount(sorted), newest: sorted[0]!.created_at }
    })
    .sort((a, b) => (a.newest < b.newest ? 1 : a.newest > b.newest ? -1 : 0))
}
