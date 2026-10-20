import { Seo } from '@/components/primitives/Seo'

// The terms, as one page, in the words that matter. Linked from checkout and every report.
export function Terms() {
  return (
    <Doc title="Terms" description="Point-in-time audits pinned to a commit, slot and bytecode hash. USDC payments, final. Twelve-month Pro term." path="/terms">
      <li>Findings are point-in-time against a specific commit, slot and bytecode hash. No guarantee of completeness.</li>
      <li>Audits are run by our agents with human review. Operators access the code for the duration of the audit.</li>
      <li>Code is deleted after delivery. Reports are retained.</li>
      <li>Payments are one-time, in USDC, and final. Pro is a fixed 12-month term with no cancellation.</li>
      <li>Exploit details are provided only to verified owners. Critical findings on live programs not owned by the customer are disclosed to the program owner.</li>
      <li>Free audit credits granted for posts may be revoked if the post is removed.</li>
    </Doc>
  )
}

export function Privacy() {
  return (
    <Doc title="Privacy" description="What is stored, for how long, and what is deleted after an audit is delivered." path="/privacy">
      <li>We store your identity providers' ids, your email when a provider shares it, and your wallet address when you sign in with one.</li>
      <li>Repository clones exist only for the duration of an audit and are deleted on delivery or failure.</li>
      <li>Reports, findings and the credit ledger are kept until you delete the account.</li>
      <li>Anonymous scans are keyed by a hashed IP for rate limiting and expire after seven days.</li>
      <li>No analytics trackers. No third-party data sales.</li>
    </Doc>
  )
}

function Doc({ title, description, path, children }: { title: string; description: string; path: string; children: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-[680px] px-6 py-12 md:px-10">
      <Seo title={title} description={description} path={path} />
      <span className="label-caps">{title.toLowerCase()}</span>
      <h1 className="mt-2 mb-8 text-2xl font-medium">{title}</h1>
      <ol className="flex list-decimal flex-col gap-4 pl-5 text-[15px] leading-relaxed text-foreground/90 marker:font-mono marker:text-xs marker:text-muted-foreground">{children}</ol>
    </div>
  )
}
