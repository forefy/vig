import { isRouteErrorResponse, Link, useRouteError } from 'react-router'
import { Mark } from '@/components/primitives'
import { Button } from '@/components/ui/button'

// Route-level failure: the mark, the status as a fact, one way out.
export function RouteError() {
  const err = useRouteError()
  const status = isRouteErrorResponse(err) ? err.status : null
  const word = status === 404 ? 'Nothing at this address' : status ? `${status}` : 'Something broke'
  return (
    <div className="flex min-h-[calc(100dvh/var(--text-scale,1))] flex-col items-center justify-center gap-4 px-6 text-center">
      <Mark size={28} className="text-faint" />
      <span className="font-mono text-sm text-muted-foreground">{word}</span>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
          Reload
        </Button>
        <Button variant="outline" size="sm" render={<Link to="/" reloadDocument />}>
          Home
        </Button>
      </div>
    </div>
  )
}
