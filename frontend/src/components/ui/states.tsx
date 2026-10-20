import { RotateCw } from "lucide-react"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import { problemOf } from "@/lib/problem"
import { cn } from "cn"

// Loading rows for lists and tables. Same height as the rows they stand in for.
function SkeletonRows({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-2", className)} aria-busy="true">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  )
}

// A request that failed, as a fact: the status, the title, one retry.
function ProblemBadge({ error, onRetry, className }: { error: unknown; onRetry?: () => void; className?: string }) {
  const p = problemOf(error)
  return (
    <div role="alert" className={cn("flex items-center gap-2 rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-xs text-bad", className)}>
      <span className="font-mono">{p.status ?? "!"}</span>
      <span className="truncate">{p.title ?? p.detail ?? "Request failed"}</span>
      {onRetry && (
        <Button size="xs" variant="outline" onClick={onRetry} className="ml-auto text-foreground" aria-label="Retry">
          <RotateCw className="size-3" /> Retry
        </Button>
      )}
    </div>
  )
}

export { SkeletonRows, ProblemBadge }
