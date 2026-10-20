import { Seo } from '@/components/primitives/Seo'
import { $api } from '@/api/hooks'
import { usdc } from '@/lib/utils'
import { ScanForm } from './ScanForm'

// One input. The result page does the talking.
export function ScanPage() {
  const plans = $api.useQuery('get', '/v1/plans')

  return (
    <div className="px-6 md:px-10">
      <Seo title="Scan a repository" description="Pre-flight facts for any public program: on-chain state, declared ids, verified build, advisories." path="/scan" />
      <section className="mx-auto flex max-w-[760px] flex-col items-center gap-10 pt-16 pb-20 md:pt-24">
        <span className="label-caps">scan a public repository</span>
        <ScanForm autoFocus />
      </section>

      <section className="mx-auto grid max-w-[980px] grid-cols-1 gap-3 pb-24 sm:grid-cols-3">
        {(plans.data?.items ?? []).filter((p) => p.key !== 'team').map((p) => (
          <div key={p.key} className="flex flex-col gap-3 rounded-md border border-border bg-card p-5">
            <span className="label-caps">{p.name}</span>
            <span className="num text-[30px] leading-none">
              {p.display_monthly_usdc ? (
                <>
                  {usdc(p.display_monthly_usdc, { compact: true })}
                  <span className="text-sm text-muted-foreground"> / mo</span>
                </>
              ) : p.price_usdc ? (
                usdc(p.price_usdc, { compact: true })
              ) : (
                <span className="text-primary">1 audit</span>
              )}
            </span>
            <dl className="mt-auto flex flex-col gap-1.5 border-t border-border pt-3 text-xs">
              <div className="flex justify-between"><dt className="label-caps">{p.runs_per_day ? 'runs' : 'audits'}</dt><dd className="num">{p.runs_per_day ? `${p.runs_per_day} / day` : p.credits}</dd></div>
              <div className="flex justify-between"><dt className="label-caps">programs</dt><dd className="num">{p.max_programs}</dd></div>
              <div className="flex justify-between"><dt className="label-caps">lines</dt><dd className="num">{Math.round(p.max_lines / 1000)}k</dd></div>
              {p.term_days ? <div className="flex justify-between"><dt className="label-caps">term</dt><dd className="num">{p.term_days} days · {usdc(p.price_usdc, { compact: true })} once</dd></div> : null}
            </dl>
          </div>
        ))}
      </section>
    </div>
  )
}
