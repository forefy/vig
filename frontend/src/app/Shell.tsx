import { NavLink, Outlet, Link } from 'react-router'
import { LayoutGrid, ListChecks, Radar, Settings, ShieldCheck, LogOut } from 'lucide-react'
import { Mark } from '@/components/primitives'
import { useMe } from '@/features/auth/useMe'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { api } from '@/api/client'
import { queryClient } from './queryClient'
import { cn } from '@/lib/utils'
import { NotificationsBell } from './NotificationsBell'
import { ViewingAs } from './ViewingAs'

const nav = [
  { to: '/app', label: 'Dashboard', icon: LayoutGrid, end: true },
  { to: '/app/audits', label: 'Audits', icon: ListChecks },
  { to: '/app/atlas', label: 'Atlas', icon: Radar },
  { to: '/app/settings', label: 'Settings', icon: Settings },
]

// The app archetype: a narrow rail, a wide stage. Three destinations, no more.
export function Shell() {
  const { me } = useMe()
  const initials = (me?.user.display_name || '?').slice(0, 1).toUpperCase()
  return (
    <div className="flex min-h-[calc(100dvh/var(--text-scale,1))]">
      <aside className="sticky top-0 flex h-[calc(100dvh/var(--text-scale,1))] w-14 shrink-0 flex-col items-center border-r border-sidebar-border bg-sidebar py-4">
        <Link to="/app" className="mb-6 text-foreground" aria-label="Home">
          <Mark size={26} />
        </Link>
        <nav className="flex flex-col gap-1">
          {nav.map((n) => (
            <Tooltip key={n.to}>
              <TooltipTrigger
                render={
                  <NavLink
                    to={n.to}
                    end={n.end}
                    aria-label={n.label}
                    className={({ isActive }) =>
                      cn('flex size-10 items-center justify-center rounded-md text-muted-foreground transition-colors hover:text-foreground', isActive && 'bg-accent text-foreground')
                    }
                  />
                }
              >
                <n.icon className="size-[18px]" />
              </TooltipTrigger>
              <TooltipContent side="right">{n.label}</TooltipContent>
            </Tooltip>
          ))}
          {me?.user.role === 'admin' && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <NavLink
                    to="/admin"
                    aria-label="Admin"
                    className={({ isActive }) =>
                      cn('mt-3 flex size-10 items-center justify-center rounded-md text-muted-foreground transition-colors hover:text-foreground', isActive && 'bg-accent text-foreground')
                    }
                  />
                }
              >
                <ShieldCheck className="size-[18px]" />
              </TooltipTrigger>
              <TooltipContent side="right">Admin</TooltipContent>
            </Tooltip>
          )}
        </nav>
        <div className="mt-auto flex flex-col items-center gap-2">
          <NotificationsBell />
          <DropdownMenu>
            <DropdownMenuTrigger className="rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <Avatar className="size-8 border border-border">
                <AvatarImage src={me?.user.avatar_url} />
                <AvatarFallback className="font-mono text-xs">{initials}</AvatarFallback>
              </Avatar>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="right" align="end" className="min-w-44">
              <div className="px-2 py-1.5 text-xs text-muted-foreground">{me?.user.display_name}</div>
              <DropdownMenuSeparator />
              <DropdownMenuItem render={<Link to="/app/settings" />}>Settings</DropdownMenuItem>
              <DropdownMenuItem
                onClick={async () => {
                  await api.POST('/v1/auth/logout')
                  queryClient.clear()
                  window.location.href = '/'
                }}
              >
                <LogOut className="size-3.5" /> Sign out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </aside>
      <main className="min-w-0 flex-1">
        {me?.impersonation && <ViewingAs me={me} />}
        <Outlet />
      </main>
    </div>
  )
}

// Standard stage padding and width. Pages compose inside this.
export function Page({ children, className, wide = false }: { children: React.ReactNode; className?: string; wide?: boolean }) {
  return <div className={cn('mx-auto w-full px-6 py-8 md:px-10 md:py-10', wide ? 'max-w-[1400px]' : 'max-w-[1120px]', className)}>{children}</div>
}

export function PageHeader({ eyebrow, title, children, aside, className }: { eyebrow?: string; title: React.ReactNode; children?: React.ReactNode; aside?: React.ReactNode; className?: string }) {
  return (
    <header className={cn('mb-8 flex flex-wrap items-end justify-between gap-4 border-b border-border pb-6', className)}>
      <div className="flex flex-col gap-1.5">
        {eyebrow && <span className="label-caps">{eyebrow}</span>}
        <h1 className="text-[26px] leading-tight font-medium">{title}</h1>
        {children}
      </div>
      {aside && <div className="flex items-center gap-2">{aside}</div>}
    </header>
  )
}
