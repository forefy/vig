import { NavLink, Outlet } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { Page, PageHeader } from '@/app/Shell'
import { cn } from '@/lib/utils'

const tabs = [
  { to: '/app/settings', label: 'Profile', end: true },
  { to: '/app/settings/identities', label: 'Identities' },
  { to: '/app/settings/notifications', label: 'Notifications' },
  { to: '/app/settings/security', label: 'Security' },
  { to: '/app/settings/api-keys', label: 'API keys' },
  { to: '/app/settings/data', label: 'Data' },
]

// Settings: a left index, one panel. Each panel is a form, not a page.
export function Settings() {
  return (
    <Page>
      <Helmet>
        <title>Settings</title>
      </Helmet>
      <PageHeader eyebrow="settings" title="Settings" />
      <div className="grid grid-cols-1 gap-8 md:grid-cols-[180px_minmax(0,1fr)]">
        <nav className="flex flex-row gap-1 overflow-x-auto md:flex-col">
          {tabs.map((t) => (
            <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => cn('rounded-md px-3 py-1.5 text-sm whitespace-nowrap text-muted-foreground hover:text-foreground', isActive && 'bg-accent text-foreground')}>
              {t.label}
            </NavLink>
          ))}
        </nav>
        <div className="min-w-0">
          <Outlet />
        </div>
      </div>
    </Page>
  )
}

export function Field({ label, children, hint }: { label: string; children: React.ReactNode; hint?: React.ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-2 border-b border-border py-5 last:border-0 md:grid-cols-[200px_minmax(0,1fr)] md:gap-6">
      <div className="flex flex-col gap-1">
        <span className="text-sm">{label}</span>
        {hint && <span className="font-mono text-xs text-muted-foreground">{hint}</span>}
      </div>
      <div className="flex min-w-0 flex-col gap-2">{children}</div>
    </div>
  )
}
