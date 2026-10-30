import { useParams } from 'react-router'
import { Seo } from '@/components/primitives/Seo'
import { $api } from '@/api/hooks'
import { Loading } from '@/features/auth/guards'
import { Pin, MethodsStrip } from '@/components/primitives'
import { ProblemBadge } from '@/components/ui/states'
import { ReportBody } from '@/features/audits/Report'
import { ExportDialog } from '@/features/audits/export'
import { Revisions } from '@/features/audits/revisions'
import { useReportView } from '@/features/audits/view'
import { problemOf } from '@/lib/problem'
import { fmtDate } from '@/lib/utils'

// Public report page. Indexable, shareable, never shows exploit paths.
export function PublicReport() {
  const { slug = '' } = useParams()
  const q = $api.useQuery('get', '/v1/reports/{slug}', { params: { path: { slug } } })
  const view = useReportView(q.data)
  if (q.isLoading) return <Loading />
  const r = q.data
  // Only a 404 says the slug is not published. Every other way of having no
  // report is the request failing, and "not public" would be a claim about
  // this report that we have no answer for.
  if (!r) {
    if (problemOf(q.error).status === 404) return <div className="px-10 py-20 text-center text-muted-foreground">This report is not public</div>
    return (
      <div className="mx-auto max-w-[420px] px-6 py-20">
        <ProblemBadge error={q.error} onRetry={() => void q.refetch()} />
      </div>
    )
  }
  const s = r.document.report.summary
  const title = `${r.document.report.pin.repo} audit`
  const pinned = r.adapter === 'webapp' ? '' : ` Pinned to ${r.document.report.pin.commit.slice(0, 7)}.`
  const desc = `${s.critical} critical, ${s.high} high, ${s.medium} medium, ${s.low} low.${pinned}`
  return (
    <div className="mx-auto max-w-[1200px] px-6 pb-20 md:px-10">
      <Seo title={title} description={desc} path={`/r/${slug}`} image={`/og/r/${slug}.png`} type="article" />
      <header className="flex flex-col gap-3 border-b border-border py-8">
        <span className="label-caps">audit report · {fmtDate(r.published_at)}</span>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <h1 className="font-mono text-2xl">{r.document.report.pin.repo}<Revisions report={r} className="ml-3 align-middle" /></h1>
          <ExportDialog target={{ slug }} repo={r.document.report.pin.repo} components={(r.document.report.pin.programs ?? []).length + (r.document.report.pin.repos ?? []).length} {...view} />
        </div>
        <Pin pin={r.document.report.pin} adapter={r.adapter} findings={r.document.findings ?? []} />
        <MethodsStrip methods={r.document.report.methods} />
      </header>
      <div className="pt-8">
        <ReportBody report={r} />
      </div>
    </div>
  )
}
