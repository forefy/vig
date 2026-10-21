import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { chainWords } from '@/lib/chain'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, ArrowRight, Plus, X } from 'lucide-react'
import { api } from '@/api/client'
import { $api } from '@/api/hooks'
import { useMe } from '@/features/auth/useMe'
import { useNow } from '@/lib/useNow'
import { Page, PageHeader } from '@/app/Shell'
import { Stepper, OptionCards, Meter, StateBadge, Stat, Pin, Mark } from '@/components/primitives'
import { Skeleton } from '@/components/ui/skeleton'
import { ProblemBadge } from '@/components/ui/states'
import { Tree, collectFiles, autoSelection } from './Tree'
import { ProgramCard } from '@/adapters/solana/ProgramCard'
import { ContractCard } from '@/adapters/ethereum/ContractCard'
import { SiteCard } from '@/adapters/webapp/SiteCard'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { cn, short, fmtDate } from '@/lib/utils'
import { loadDraft, saveDraft, clearDraft } from '@/lib/wizardDraft'
import { toast } from 'sonner'
import type { components } from '@/api/client'
import type { WizardDraft } from '@/lib/wizardDraft'

type Target = components['schemas']['Target']
type Program = components['schemas']['Program']
type TreeT = components['schemas']['Tree']
type Installation = components['schemas']['Installation']
type PinT = components['schemas']['Pin']
type ProgramSource = components['schemas']['ProgramSource']
type AdapterInfo = components['schemas']['AdapterInfo']

const repoRe = /^https:\/\/github\.com\/[\w.-]+\/[\w.-]+(\/tree\/[^\s]+)?\/?$/
const pidRe = /^[1-9A-HJ-NP-Za-km-z]{32,44}$/
const evmRe = /^0x[0-9a-fA-F]{40}$/

// The target card names the adapter, and the adapter says what an address
// looks like.
const isId = (adapter: string, id: string): boolean => (adapter === 'ethereum' ? evmRe : pidRe).test(id)

type TargetCard = { value: string; label: string; hint?: string; soon?: boolean; disabled?: boolean; adapter: string }

// The target cards are every adapter's target options, live ones first and
// each value once, so a new adapter is a new card and nothing here changes.
function cardsOf(adapters: AdapterInfo[]): TargetCard[] {
  const seen = new Map<string, TargetCard>()
  for (const a of adapters) {
    const opts = (a.steps ?? []).find((s) => s.key === 'target')?.fields?.find((f) => f.key === 'kind')?.options ?? []
    for (const o of opts) {
      const prev = seen.get(o.value)
      if (prev && !prev.soon) continue
      seen.set(o.value, { value: o.value, label: o.label, hint: o.hint ?? undefined, soon: o.soon ?? undefined, disabled: o.disabled ?? undefined, adapter: a.key })
    }
  }
  const all = [...seen.values()]
  return [...all.filter((c) => !c.soon), ...all.filter((c) => c.soon)]
}

// The adapter's pattern for a field, or the engine's own when it gives none.
const reOf = (pattern: string | undefined, fallback: RegExp): RegExp => (pattern ? new RegExp(pattern) : fallback)

const hostOf = (origin: string): string => {
  try {
    return new URL(origin.trim()).hostname.toLowerCase()
  } catch {
    return ''
  }
}
const underHost = (originHost: string, host: string): boolean => !!originHost && (host === originHost || host.endsWith('.' + originHost))

// Where a program's repo was found, when nothing verified it.
const originWord: Record<string, string> = { security_txt: 'security.txt', idl: 'idl metadata', search: 'code search' }

// What a target was resolved from. A program id resolves as a public repo.
function sourceKeyOf(kind: string, repoUrl: string, installationId: string, installRepo: string, selector: string, revision: string, baseUrl = '') {
  return [kind === 'program' ? 'public' : kind, repoUrl.trim(), installationId, installRepo.trim(), selector, revision.trim(), baseUrl.trim()].join('|')
}
const problem = (e: unknown) => (e as { detail?: string } | undefined)?.detail

// What the URL carries in: a re-audit from a report, or a scan's repo and commit.
function fromParams(p: URLSearchParams) {
  const repo = p.get('repo') ?? ''
  const commit = p.get('commit') ?? ''
  const ids = (p.get('programs') ?? p.get('program') ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter((s) => pidRe.test(s))
  return {
    present: !!(repo || commit || p.get('from') || ids.length),
    coverage: (p.get('coverage') === 'diff' || p.get('from') ? 'diff' : 'full') as 'full' | 'diff',
    repoUrl: repo ? (repo.startsWith('https://') ? repo : `https://github.com/${repo}`) : '',
    selector: (commit ? 'commit' : 'branch') as 'branch' | 'tag' | 'commit',
    revision: commit,
    base: p.get('base') ?? '',
    programs: ids,
  }
}

// Wizard archetype. Steps come from the adapter; the order summary on the
// right is the running pin. The draft lives in sessionStorage for an hour so a
// detour to billing comes back to the same step.
export function NewAudit() {
  const nav = useNavigate()
  const qc = useQueryClient()
  const [params] = useSearchParams()
  const { me } = useMe()
  const userId = me?.user.id ?? ''

  const [seed] = useState(() => {
    const p = fromParams(params)
    const draft = !p.present && userId ? loadDraft(userId) : null
    return { p, draft }
  })
  const d: Partial<WizardDraft> = seed.draft ?? {}
  const p = seed.p

  // target
  const [kind, setKind] = useState<string>(d.kind ?? 'solana_program')
  const adapters = $api.useQuery('get', '/v1/adapters', {}, { staleTime: Infinity })
  const cards = useMemo(() => cardsOf(adapters.data?.items ?? []), [adapters.data])
  const card = cards.find((c) => c.value === kind)
  const adapterKey = card?.adapter ?? ''
  const adapter = $api.useQuery('get', '/v1/adapters/{key}', { params: { path: { key: adapterKey } } }, { staleTime: Infinity, enabled: !!adapterKey })
  const steps = useMemo(() => (adapter.data?.steps ?? []).map((s) => ({ key: s.key, title: s.title })), [adapter.data])
  const targetStep = useMemo(() => adapter.data?.steps?.find((s) => s.key === 'target'), [adapter.data])
  const hasCoverage = !!targetStep?.fields?.some((f) => f.key === 'coverage')
  // A site step marks a live target: an origin where a repository was, a
  // scope of hosts and prefixes where a file tree was.
  const siteStep = useMemo(() => adapter.data?.steps?.find((s) => s.key === 'site'), [adapter.data])
  const isSite = !!siteStep
  const originField = siteStep?.fields?.find((f) => f.key === 'base_url')
  const originRe = reOf(originField?.pattern, /^https?:\/\/[^\s/]+\/?$/)
  const scopeStep = useMemo(() => adapter.data?.steps?.find((s) => s.key === 'scope'), [adapter.data])
  const hostField = scopeStep?.fields?.find((f) => f.key === 'hosts')
  const pathField = scopeStep?.fields?.find((f) => f.key === 'paths')
  const hostRe = reOf(hostField?.pattern, /^[a-z0-9.-]+$/)
  const pathRe = reOf(pathField?.pattern, /^\//)
  const programsStep = useMemo(() => adapter.data?.steps?.find((s) => s.key === 'programs'), [adapter.data])
  const chainField = programsStep?.fields?.find((f) => f.key === 'chain')
  const idField = programsStep?.fields?.find((f) => f.key === 'program_ids')
  const [chain, setChain] = useState<string>(d.chain ?? '')
  const chainId = chainField ? chain || chainField.options?.[0]?.value || '' : ''
  // The program adds read the chain through a ref, so the callback that
  // effects depend on keeps its identity while the chain changes.
  const chainRef = useRef(chainId)
  useEffect(() => {
    chainRef.current = chainId
  }, [chainId])

  const [step, setStep] = useState(d.step ?? 0)
  const stepKey = steps[step]?.key
  const [coverage, setCoverage] = useState<'full' | 'diff'>(d.coverage ?? p.coverage)
  // source
  const [sourceKind, setSourceKind] = useState<'public' | 'installation' | 'program'>(d.source_kind ?? 'public')
  const [repoUrl, setRepoUrl] = useState(d.repo_url ?? p.repoUrl)
  const [sourcePid, setSourcePid] = useState(d.source_pid ?? '')
  const [looking, setLooking] = useState(false)
  const [lookup, setLookup] = useState<ProgramSource | null>(null)
  const [selector, setSelector] = useState<'branch' | 'tag' | 'commit'>(d.selector ?? p.selector)
  const [revision, setRevision] = useState(d.revision ?? p.revision)
  const [base, setBase] = useState(d.base ?? p.base)
  const [installationId, setInstallationId] = useState(d.installation_id ?? '')
  const [installRepo, setInstallRepo] = useState(d.install_repo ?? '')
  // site
  const [baseUrl, setBaseUrl] = useState(d.base_url ?? '')
  const [hosts, setHosts] = useState<string[]>(d.hosts ?? [])
  const [paths, setPaths] = useState<string[]>(d.paths ?? [])
  const [savingScope, setSavingScope] = useState(false)
  const [target, setTarget] = useState<Target | null>(d.target ?? null)
  const [resolving, setResolving] = useState(false)
  // The inputs the current target was resolved from; anything else is stale.
  const [resolvedKey, setResolvedKey] = useState(() =>
    d.target ? sourceKeyOf(d.source_kind ?? 'public', d.repo_url ?? '', d.installation_id ?? '', d.install_repo ?? '', d.selector ?? 'branch', d.revision ?? '', d.base_url ?? '') : '',
  )
  // programs
  const [pid, setPid] = useState('')
  const [pending, setPending] = useState<string[]>(seed.draft ? [] : p.programs)
  const [programs, setPrograms] = useState<Program[]>(d.programs ?? [])
  const [selectedPid, setSelectedPid] = useState('')
  const [fetching, setFetching] = useState(false)
  // scope
  const [tree, setTree] = useState<TreeT | null>(null)
  const [selected, setSelected] = useState<Set<string>>(() => new Set(d.selected ?? []))
  // review
  const [queueing, setQueueing] = useState(false)

  const installs = $api.useQuery('get', '/v1/me/github/installations', {}, { enabled: sourceKind === 'installation' })
  const plans = $api.useQuery('get', '/v1/plans')
  const tier = useMemo(() => {
    const key = me?.plan?.kind === 'team' ? 'team' : me?.plan?.kind === 'pro' ? 'pro' : 'mini'
    return (plans.data?.items ?? []).find((x) => x.key === key)
  }, [plans.data, me])
  const maxPrograms = tier?.max_programs ?? 1

  useEffect(() => {
    if (!userId) return
    saveDraft(userId, {
      step,
      kind,
      coverage,
      source_kind: sourceKind,
      source_pid: sourcePid,
      chain,
      repo_url: repoUrl,
      installation_id: installationId,
      install_repo: installRepo,
      selector,
      revision,
      base,
      base_url: baseUrl,
      hosts,
      paths,
      target,
      programs,
      selected: [...selected],
    })
  }, [userId, step, kind, coverage, sourceKind, sourcePid, chain, repoUrl, installationId, installRepo, selector, revision, base, baseUrl, hosts, paths, target, programs, selected])

  const selectedLines = useMemo(() => {
    if (!tree) return 0
    let n = 0
    const walk = (nodes: TreeT['nodes'] | undefined) =>
      (nodes ?? []).forEach((x) => {
        if (!x.dir && selected.has(x.path)) n += x.code ?? x.lines ?? 0
        walk(x.children)
      })
    walk(tree.nodes)
    return n
  }, [tree, selected])

  const now = useNow(30_000)
  const slotOpen = !!me?.plan && new Date(me.plan.next_slot_at).getTime() <= now
  const canRun = (me?.credits ?? 0) > 0 || slotOpen
  const costKind = slotOpen ? 'slot' : 'credit'

  // A program id resolves through the verified-build registry to a repo and
  // commit; the source step then continues as a public repository pinned there.
  const lookupProgram = useCallback(async (id: string) => {
    setLooking(true)
    const res = await api.GET('/v1/programs/{pid}/source', { params: { path: { pid: id } } })
    setLooking(false)
    if (!res.data) {
      toast.error(problem(res.error) ?? 'Registry unavailable')
      return
    }
    setLookup(res.data)
    setRepoUrl(res.data.repo_url ?? '')
    if (res.data.repo_url) {
      setSelector(res.data.selector ?? 'branch')
      setRevision(res.data.revision ?? '')
    }
  }, [])

  const originHost = hostOf(target?.site?.origin ?? baseUrl)
  const siteValid = isSite && originRe.test(baseUrl.trim()) && (repoUrl.trim() === '' || repoRe.test(repoUrl.trim()))
  const sourceValid = isSite ? siteValid : sourceKind === 'installation' ? !!installationId && !!installRepo.trim() : repoRe.test(repoUrl.trim())
  const sourceKey = isSite ? sourceKeyOf('site', repoUrl, '', '', selector, revision, baseUrl) : sourceKeyOf(sourceKind, repoUrl, installationId, installRepo, selector, revision)
  const resolved = !!target && resolvedKey === sourceKey

  const resolveSource = useCallback(async () => {
    setResolving(true)
    const fromProgram = sourceKind === 'program'
    const snapshot = repoUrl.trim() !== ''
    const res = await api.POST('/v1/targets', {
      body: isSite
        ? {
            adapter: adapterKey,
            base_url: baseUrl.trim(),
            repo_url: snapshot ? repoUrl.trim() : undefined,
            selector: snapshot ? selector : undefined,
            revision: snapshot ? revision.trim() : undefined,
            hosts: hosts.length ? hosts : undefined,
            paths: paths.length ? paths : undefined,
          }
        : {
            adapter: adapterKey,
            source_kind: fromProgram ? 'public' : sourceKind,
            repo_url: sourceKind === 'installation' ? undefined : repoUrl.trim(),
            installation_id: sourceKind === 'installation' ? installationId : undefined,
            repo: sourceKind === 'installation' ? installRepo.trim() : undefined,
            selector,
            revision: revision.trim(),
          },
    })
    setResolving(false)
    if (!res.data) {
      toast.error(problem(res.error) ?? (isSite ? 'Could not read the site' : 'Could not resolve the repository'))
      return false
    }
    setTarget(res.data)
    if (isSite) {
      setResolvedKey(sourceKeyOf('site', repoUrl, '', '', snapshot ? res.data.revision.selector : selector, snapshot ? res.data.revision.value : revision, baseUrl))
      if (snapshot) {
        setSelector(res.data.revision.selector)
        setRevision(res.data.revision.value)
      }
      setPrograms([])
      setPending([])
      setTree(null)
      setSelected(new Set())
      return true
    }
    setResolvedKey(sourceKeyOf(sourceKind, repoUrl, installationId, installRepo, res.data.revision.selector, res.data.revision.value))
    setSelector(res.data.revision.selector)
    setRevision(res.data.revision.value)
    setPrograms(res.data.programs ?? [])
    setPending(fromProgram && isId(adapterKey, sourcePid.trim()) ? [sourcePid.trim()] : [])
    setTree(null)
    setSelected(new Set())
    return true
  }, [isSite, baseUrl, hosts, paths, sourceKind, repoUrl, installationId, installRepo, selector, revision, sourcePid, adapterKey, setTree, setSelected])

  // The scope is what the customer authorises; it is written to the target
  // before the review so the pin and the audit read the same thing.
  const saveScope = useCallback(async () => {
    if (!target) return false
    setSavingScope(true)
    const res = await api.PUT('/v1/targets/{id}/scope', { params: { path: { id: target.id } }, body: { hosts: [originHost, ...hosts], paths } })
    setSavingScope(false)
    if (!res.data) {
      toast.error(problem(res.error) ?? 'Could not save the scope')
      return false
    }
    setTarget(res.data)
    return true
  }, [target, originHost, hosts, paths])

  // Typing is the trigger: a valid program id is looked up, and a valid source
  // is resolved, each after a short pause. Continue only moves on.
  useEffect(() => {
    if (stepKey !== 'source' || sourceKind !== 'program' || looking) return
    const id = sourcePid.trim()
    if (!isId(adapterKey, id) || lookup?.id === id) return
    const t = setTimeout(() => void lookupProgram(id), 400)
    return () => clearTimeout(t)
  }, [stepKey, sourceKind, sourcePid, lookup, looking, lookupProgram, adapterKey])
  useEffect(() => {
    if ((stepKey !== 'source' && stepKey !== 'site') || !sourceValid || resolving || looking || resolvedKey === sourceKey) return
    const t = setTimeout(() => void resolveSource(), 700)
    return () => clearTimeout(t)
  }, [stepKey, sourceValid, resolving, looking, resolvedKey, sourceKey, resolveSource])

  // The declared-id scan runs on a background checkout; poll until it lands.
  const targetQ = $api.useQuery(
    'get',
    '/v1/targets/{id}',
    { params: { path: { id: target?.id ?? '' } } },
    {
      enabled: !!target && !target.scanned,
      refetchInterval: (q) => (q.state.data?.scanned ? false : 1500),
    },
  )
  const scan = targetQ.data?.scanned ? targetQ.data : target
  const declared = scan?.declared ?? []
  const scanned = !!scan?.scanned
  const liveDeclared = declared.filter((x) => x.on_chain === 'live')
  const current = programs.find((x) => x.id === selectedPid) ?? programs[0]

  const addProgram = useCallback(
    async (id: string): Promise<boolean> => {
      if (!target) return false
      setFetching(true)
      const res = await api.POST('/v1/targets/{id}/programs', { params: { path: { id: target.id } }, body: { program_id: id, chain: chainRef.current || undefined } })
      setFetching(false)
      if (!res.data) {
        toast.error(problem(res.error) ?? 'Not found on chain')
        return false
      }
      const np = res.data
      setPrograms((ps) => [...ps.filter((x) => x.id !== np.id), np])
      setSelectedPid(np.id)
      void qc.invalidateQueries({ queryKey: ['get', '/v1/targets/{id}'] })
      return true
    },
    [target, qc, chainRef],
  )

  // Program ids carried in by the URL are fetched one at a time, in order,
  // once the wizard reaches the programs step with a resolved target.
  const programsRef = useRef(programs)
  useEffect(() => {
    programsRef.current = programs
  }, [programs])
  // Live declared ids are fetched on their own the first time the programs
  // step sees a scanned target, up to the tier's cap.
  const autoQueued = useRef('')
  useEffect(() => {
    if (stepKey !== 'programs' || !target || !scanned || pending.length > 0 || autoQueued.current === target.id) return
    autoQueued.current = target.id
    const room = maxPrograms - programsRef.current.length
    const ids = liveDeclared
      .map((x) => x.id)
      .filter((id) => !programsRef.current.some((x) => x.id === id))
      .slice(0, Math.max(0, room))
    if (ids.length > 0) setPending(ids)
  }, [stepKey, target, scanned, pending.length, liveDeclared, maxPrograms])
  useEffect(() => {
    if (stepKey !== 'programs' || !target || pending.length === 0) return
    const ids = pending
    void (async () => {
      for (const id of ids) {
        if (!programsRef.current.some((x) => x.id === id)) await addProgram(id)
      }
      setPending([])
    })()
  }, [stepKey, target, pending, addProgram])

  async function fetchProgram() {
    const id = pid.trim()
    if (!isId(adapterKey, id)) return
    if (await addProgram(id)) setPid('')
  }

  async function removeProgram(id: string): Promise<boolean> {
    if (!target) return false
    const res = await api.DELETE('/v1/targets/{id}/programs/{pid}', { params: { path: { id: target.id, pid: id } } })
    if (res.error) {
      toast.error('Could not remove')
      return false
    }
    setPrograms((ps) => ps.filter((x) => x.id !== id))
    void qc.invalidateQueries({ queryKey: ['get', '/v1/targets/{id}'] })
    return true
  }

  const loadTree = useCallback(
    async (keepSelection: boolean) => {
      if (!target) return false
      const res = await api.GET('/v1/targets/{id}/tree', { params: { path: { id: target.id } } })
      if (!res.data) {
        toast.error('Could not read the repository tree')
        return false
      }
      setTree(res.data)
      if (!keepSelection) setSelected(autoSelection(res.data.nodes))
      return true
    },
    [target, setTree, setSelected],
  )

  // A restored draft parked on scope or review has no tree in storage.
  const needsTree = !isSite && (stepKey === 'scope' || stepKey === 'review') && !!target && !tree
  useEffect(() => {
    if (!needsTree) return
    const t = setTimeout(() => void loadTree(true), 0)
    return () => clearTimeout(t)
  }, [needsTree, loadTree])

  async function queue() {
    if (!target) return
    setQueueing(true)
    const res = await api.POST('/v1/audits', {
      body: isSite
        ? { target_id: target.id, coverage: 'full', program_ids: [], files: [] }
        : { target_id: target.id, coverage, base_sha: coverage === 'diff' ? base.trim() || undefined : undefined, program_ids: programs.map((x) => x.id), files: [...selected] },
    })
    setQueueing(false)
    if (!res.data) {
      toast.error(problem(res.error) ?? 'Could not queue')
      return
    }
    if (userId) clearDraft(userId)
    void qc.invalidateQueries({ queryKey: ['get', '/v1/audits'] })
    void qc.invalidateQueries({ queryKey: ['get', '/v1/me'] })
    nav(`/app/audits/${res.data.id}`)
  }

  const maxLines = tree?.max_lines ?? tier?.max_lines ?? 10_000
  const overCap = programs.length > maxPrograms || selectedLines > maxLines
  const last = step >= steps.length - 1
  const returnTo = `/app/billing?${new URLSearchParams({ return: '/app/audits/new' }).toString()}`

  const pin: PinT | null = target
    ? {
        repo: target.repo,
        commit: target.revision.commit,
        base_sha: !isSite && coverage === 'diff' && base.trim() ? base.trim() : undefined,
        programs: isSite ? [] : programs.map((x) => ({ id: x.id, slot: x.last_deploy_slot ?? 0, match: x.verified_build, bytecode_sha256: x.bytecode_sha256 ?? '' })),
      }
    : null
  // The scope a site is pinned to, as the backend will write it: every host
  // under every prefix, the origin's host first.
  const scopeHosts = useMemo(() => (originHost ? [originHost, ...hosts.filter((h) => h !== originHost)] : hosts), [originHost, hosts])
  const scopeUrls = useMemo(() => {
    const scheme = (target?.site?.origin ?? baseUrl).trim().startsWith('http://') ? 'http' : 'https'
    return scopeHosts.flatMap((h) => (paths.length ? paths.map((p) => `${scheme}://${h}${p}`) : [`${scheme}://${h}/`]))
  }, [scopeHosts, paths, target, baseUrl])
  const words = chainWords(adapterKey)

  return (
    <Page wide>
      <Helmet>
        <title>New audit</title>
      </Helmet>
      <PageHeader eyebrow="new audit" title="Start an audit" />
      <div className="grid grid-cols-1 gap-8 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="flex flex-col rounded-md border border-border bg-card">
          <div className="border-b border-border px-6 py-4">
            {adapters.isLoading || adapter.isLoading ? (
              <Skeleton className="h-5 w-2/3" />
            ) : adapters.isError ? (
              <ProblemBadge error={adapters.error} onRetry={() => void adapters.refetch()} />
            ) : adapter.isError ? (
              <ProblemBadge error={adapter.error} onRetry={() => void adapter.refetch()} />
            ) : (
              <Stepper steps={steps} current={step} onSelect={setStep} />
            )}
          </div>
          <div className="flex flex-col gap-8 px-6 py-6">
            {stepKey === 'target' && (
              <>
                <Section label="target">
                  <OptionCards value={kind} onChange={setKind} options={cards.map((c) => ({ value: c.value, label: c.label, hint: c.hint, soon: c.soon, disabled: c.disabled }))} />
                </Section>
                {hasCoverage && (
                  <Section label="coverage">
                    <OptionCards
                      columns={2}
                      value={coverage}
                      onChange={setCoverage}
                      options={[
                        { value: 'full', label: 'Full audit', hint: 'one commit' },
                        { value: 'diff', label: 'Diff review', hint: 'base..head' },
                      ]}
                    />
                  </Section>
                )}
              </>
            )}

            {stepKey === 'source' && (
              <>
                <Section label="source">
                  <OptionCards
                    columns={3}
                    value={sourceKind}
                    onChange={(v) => setSourceKind(v as 'public' | 'installation' | 'program')}
                    options={[
                      { value: 'public', label: 'Public repository', hint: 'github.com' },
                      { value: 'installation', label: 'Connect GitHub', hint: 'GitHub App' },
                      ...(adapterKey === 'solana' ? [{ value: 'program', label: 'Program id', hint: 'verified build' }] : []),
                    ]}
                  />
                </Section>
                {sourceKind === 'program' && (
                  <Section label="program id">
                    <Input
                      value={sourcePid}
                      onChange={(e) => {
                        setSourcePid(e.target.value)
                        setLookup(null)
                        setRepoUrl('')
                      }}
                      placeholder="program id"
                      spellCheck={false}
                      aria-label="Program id"
                      className={cn('h-10 font-mono', sourcePid && !isId(adapterKey, sourcePid.trim()) && 'border-bad')}
                    />
                    {(looking || lookup) && (
                      <div className="mt-3 flex flex-wrap items-center gap-3 font-mono text-xs">
                        {looking || !lookup ? (
                          <StateBadge state="pending">registry</StateBadge>
                        ) : (
                          <>
                            <StateBadge state={lookup.verified ? 'ok' : lookup.origin === 'registry' ? 'warn' : lookup.repo_url ? 'neutral' : 'bad'}>
                              {lookup.verified ? 'verified build' : lookup.origin === 'registry' ? 'unverified' : (originWord[lookup.origin ?? ''] ?? 'no source')}
                            </StateBadge>
                            {lookup.repo_url && <span className="text-muted-foreground">{lookup.repo_url.replace(/^https:\/\/github\.com\//, '')}</span>}
                            {lookup.revision && <span className="text-faint">{lookup.selector === 'commit' ? short(lookup.revision, 7, 0) : lookup.revision}</span>}
                          </>
                        )}
                      </div>
                    )}
                  </Section>
                )}
                {sourceKind === 'installation' ? (
                  <InstallationPicker installs={installs.data?.items ?? []} value={installationId} onChange={setInstallationId} repo={installRepo} onRepo={setInstallRepo} />
                ) : (
                  <Section label="repository">
                    <Input
                      value={repoUrl}
                      onChange={(e) => setRepoUrl(e.target.value)}
                      placeholder="https://github.com/org/repo"
                      spellCheck={false}
                      aria-label="Repository URL"
                      className={cn('h-10 font-mono', repoUrl && !repoRe.test(repoUrl.trim()) && 'border-bad')}
                    />
                  </Section>
                )}
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[160px_1fr]">
                  <Section label="selector">
                    <Select value={selector} onValueChange={(v) => v && setSelector(v as typeof selector)}>
                      <SelectTrigger className="h-10" aria-label="Revision selector">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="branch">Branch</SelectItem>
                        <SelectItem value="tag">Tag</SelectItem>
                        <SelectItem value="commit">Commit</SelectItem>
                      </SelectContent>
                    </Select>
                  </Section>
                  <Section label={selector}>
                    <Input
                      value={revision}
                      onChange={(e) => setRevision(e.target.value)}
                      placeholder={selector === 'commit' ? 'sha' : selector === 'tag' ? 'v1.0.0' : 'main'}
                      spellCheck={false}
                      aria-label={`${selector} value`}
                      className="h-10 font-mono"
                    />
                  </Section>
                </div>
                {coverage === 'diff' && (
                  <Section label="base">
                    <Input value={base} onChange={(e) => setBase(e.target.value)} placeholder="sha" spellCheck={false} aria-label="Base commit" className="h-10 font-mono" />
                  </Section>
                )}
                {(resolving || (target && resolved)) && (
                  <div className="flex flex-wrap items-center gap-3 rounded-md border border-border px-4 py-3 font-mono text-xs">
                    {resolving || !target ? (
                      <StateBadge state="pending">resolving</StateBadge>
                    ) : (
                      <>
                        <StateBadge state="ok">{target.repo}</StateBadge>
                        <span className="text-muted-foreground">{short(target.revision.commit, 7, 0)}</span>
                        {target.revision.author && <span className="text-muted-foreground">{target.revision.author}</span>}
                        <span className="text-faint">{fmtDate(target.revision.date)}</span>
                        {!scanned ? (
                          <StateBadge state="pending">scanning</StateBadge>
                        ) : (
                          <StateBadge state={liveDeclared.length > 0 ? 'ok' : 'neutral'}>
                            {declared.length === 0 ? 'no declared ids' : `${liveDeclared.length} live · ${declared.length} declared`}
                          </StateBadge>
                        )}
                      </>
                    )}
                  </div>
                )}
              </>
            )}

            {stepKey === 'site' && (
              <>
                <Section label={(originField?.label ?? 'origin').toLowerCase()}>
                  <Input
                    value={baseUrl}
                    onChange={(e) => setBaseUrl(e.target.value)}
                    placeholder="https://app.example.com"
                    spellCheck={false}
                    aria-label="Origin"
                    className={cn('h-10 font-mono', baseUrl && !originRe.test(baseUrl.trim()) && 'border-bad')}
                  />
                </Section>
                <Section label="repository">
                  <Input
                    value={repoUrl}
                    onChange={(e) => setRepoUrl(e.target.value)}
                    placeholder="https://github.com/org/repo"
                    spellCheck={false}
                    aria-label="Repository URL"
                    className={cn('h-10 font-mono', repoUrl && !repoRe.test(repoUrl.trim()) && 'border-bad')}
                  />
                </Section>
                {repoUrl.trim() !== '' && (
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-[160px_1fr]">
                    <Section label="selector">
                      <Select value={selector} onValueChange={(v) => v && setSelector(v as typeof selector)}>
                        <SelectTrigger className="h-10" aria-label="Revision selector">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="branch">Branch</SelectItem>
                          <SelectItem value="tag">Tag</SelectItem>
                          <SelectItem value="commit">Commit</SelectItem>
                        </SelectContent>
                      </Select>
                    </Section>
                    <Section label={selector}>
                      <Input
                        value={revision}
                        onChange={(e) => setRevision(e.target.value)}
                        placeholder={selector === 'commit' ? 'sha' : selector === 'tag' ? 'v1.0.0' : 'main'}
                        spellCheck={false}
                        aria-label={`${selector} value`}
                        className="h-10 font-mono"
                      />
                    </Section>
                  </div>
                )}
                {resolving || !target || !resolved ? (
                  (resolving || (target && resolved)) && (
                    <div className="flex flex-wrap items-center gap-3 rounded-md border border-border px-4 py-3 font-mono text-xs">
                      <StateBadge state="pending">reading</StateBadge>
                    </div>
                  )
                ) : (
                  <>
                    {target.site && target.revision.commit && (
                      <div className="flex flex-wrap items-center gap-3 rounded-md border border-border px-4 py-3 font-mono text-xs">
                        <StateBadge state="ok">{target.repo}</StateBadge>
                        <span className="text-muted-foreground">{short(target.revision.commit, 7, 0)}</span>
                        {target.revision.author && <span className="text-muted-foreground">{target.revision.author}</span>}
                        <span className="text-faint">{fmtDate(target.revision.date)}</span>
                      </div>
                    )}
                    <SiteCard target={target} onChange={setTarget} />
                  </>
                )}
              </>
            )}

            {stepKey === 'scope' && isSite && target && (
              <>
                <ListField
                  label={(hostField?.label ?? 'hosts').toLowerCase()}
                  fixed={originHost}
                  fixedWord="origin"
                  itemWord="subdomain"
                  items={hosts}
                  placeholder={`api.${originHost}`}
                  valid={(h) => hostRe.test(h) && underHost(originHost, h) && h !== originHost}
                  normalize={(h) => h.trim().toLowerCase()}
                  onChange={setHosts}
                />
                <ListField
                  label={(pathField?.label ?? 'path prefixes').toLowerCase()}
                  itemWord="prefix"
                  emptyWord="whole host"
                  items={paths}
                  placeholder="/api/"
                  valid={(p) => pathRe.test(p) && p !== '/'}
                  normalize={(p) => p.trim()}
                  onChange={setPaths}
                />
                <div className="grid grid-cols-3 gap-3">
                  <Stat label={words.programs} value={scopeHosts.length} hint={scopeHosts.length > 1 ? `${scopeHosts.length - 1} under ${originHost}` : originHost} />
                  <Stat label={words.instructions} value={paths.length || 'all'} hint={paths.length ? 'prefixes' : 'every path'} />
                  <Stat label="pinned" value={scopeUrls.length} hint="url prefixes" />
                </div>
              </>
            )}

            {stepKey === 'programs' && target && (
              <>
                {chainField && (
                  <Section label={chainField.label.toLowerCase()}>
                    <OptionCards columns={3} value={chainId} onChange={setChain} options={(chainField.options ?? []).map((o) => ({ value: o.value, label: o.label }))} />
                  </Section>
                )}
                <Section label={(idField?.label ?? 'program id').toLowerCase()}>
                  <div className="flex gap-2">
                    <Input
                      value={pid}
                      onChange={(e) => setPid(e.target.value)}
                      placeholder={(idField?.label ?? 'program id').toLowerCase()}
                      spellCheck={false}
                      aria-label="Program id"
                      className={cn('h-10 font-mono', pid && !isId(adapterKey, pid.trim()) && 'border-bad')}
                      onKeyDown={(e) => {
                        if (e.key !== 'Enter') return
                        e.preventDefault()
                        void fetchProgram()
                      }}
                    />
                    <Button className="h-10" disabled={!isId(adapterKey, pid.trim()) || fetching || programs.length >= maxPrograms} onClick={() => void fetchProgram()}>
                      {fetching ? <Mark size={16} className="animate-[spin_6s_linear_infinite]" /> : <Plus className="size-4" />} Fetch
                    </Button>
                  </div>
                </Section>
                {pending.length > 0 && (
                  <StateBadge state="pending" className="w-fit">
                    {pending.length} queued
                  </StateBadge>
                )}
                <div className="grid grid-cols-1 gap-4 lg:grid-cols-[260px_minmax(0,1fr)]">
                  <ProgramRail
                    programs={programs}
                    declared={declared}
                    scanned={scanned}
                    selected={current?.id ?? ''}
                    busy={fetching}
                    max={maxPrograms}
                    onSelect={setSelectedPid}
                    onAdd={(id) => void addProgram(id)}
                    onRemove={(id) => void removeProgram(id)}
                  />
                  {current && adapterKey === 'ethereum' ? (
                    <ContractCard key={current.id} program={current} onRemove={() => void removeProgram(current.id)} />
                  ) : current ? (
                    <ProgramCard
                      key={current.id}
                      targetId={target.id}
                      program={current}
                      onChange={(np) => setPrograms((ps) => ps.map((y) => (y.id === np.id ? np : y)))}
                      onRemove={() => void removeProgram(current.id)}
                    />
                  ) : (
                    <div className="flex min-h-40 items-center justify-center rounded-md border border-dashed border-border">
                      <StateBadge state="neutral">{pending.length > 0 ? 'fetching' : 'source only'}</StateBadge>
                    </div>
                  )}
                </div>
              </>
            )}

            {stepKey === 'scope' &&
              !isSite &&
              (tree ? (
                <>
                  <div className="flex flex-wrap items-end justify-between gap-4">
                    <Meter value={selectedLines} max={maxLines} label="code lines in scope" className="w-full max-w-[320px]" />
                    <div className="flex gap-2">
                      <Button size="sm" variant="outline" onClick={() => setSelected(new Set())}>
                        Clear
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => setSelected(new Set((tree.nodes ?? []).flatMap((n) => collectFiles(n, []))))}>
                        Select all
                      </Button>
                    </div>
                  </div>
                  <Tree nodes={tree.nodes} selected={selected} onChange={setSelected} />
                </>
              ) : (
                <div className="flex flex-col gap-2" aria-busy="true">
                  <Skeleton className="h-5 w-64" />
                  <Skeleton className="h-[320px] w-full" />
                </div>
              ))}

            {stepKey === 'review' && target && pin && isSite && (
              <div className="flex flex-col gap-6">
                <div className="rounded-md border border-border px-4 py-3">
                  <Pin pin={pin} adapter={adapterKey} />
                </div>
                <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                  <Stat label={words.programs} value={scopeHosts.length} hint={scopeHosts.length > 1 ? `${scopeHosts.length - 1} under ${originHost}` : originHost} />
                  <Stat label={words.instructions} value={paths.length || 'all'} hint={paths.length ? 'prefixes' : 'every path'} />
                  <Stat label="cost" value={costKind === 'slot' ? 'slot' : '1'} hint={costKind === 'slot' ? 'today' : 'credit'} tone={canRun ? undefined : 'warn'} />
                  <Stat label="eta" value="24h" />
                </div>
                <div className="flex flex-col gap-2">
                  {scopeUrls.map((u, i) => (
                    <div key={u} className="flex flex-wrap items-center gap-3 rounded-md border border-border px-4 py-2.5 font-mono text-xs">
                      <span className="break-all">{u}</span>
                      <StateBadge state={i === 0 ? 'ok' : 'neutral'}>{hostOf(u) === originHost ? 'origin' : 'subdomain'}</StateBadge>
                    </div>
                  ))}
                  <div className="flex flex-wrap items-center gap-3 rounded-md border border-border px-4 py-2.5 font-mono text-xs">
                    <span className="text-muted-foreground">ownership</span>
                    <StateBadge state={target.site?.ownership_proven ? 'ok' : 'neutral'}>{target.site?.ownership_proven ? (target.site.ownership_method === 'dns_txt' ? 'dns' : 'well-known') : 'not proven'}</StateBadge>
                  </div>
                </div>
              </div>
            )}

            {stepKey === 'review' && target && pin && !isSite && (
              <div className="flex flex-col gap-6">
                <div className="rounded-md border border-border px-4 py-3">
                  <Pin pin={pin} adapter={adapterKey} />
                </div>
                <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                  <Stat label={words.programs} value={programs.length} hint={programs.map((x) => short(x.id, 4, 4)).join(' ')} />
                  <Stat label="files" value={selected.size} hint={`${selectedLines.toLocaleString('en-US')} code lines`} />
                  <Stat label="cost" value={costKind === 'slot' ? 'slot' : '1'} hint={costKind === 'slot' ? 'today' : 'credit'} tone={canRun ? undefined : 'warn'} />
                  <Stat label="eta" value="24h" />
                </div>
                <div className="flex flex-col gap-2">
                  {programs.map((x) => (
                    <div key={x.id} className="flex flex-wrap items-center gap-3 rounded-md border border-border px-4 py-2.5 font-mono text-xs">
                      <span>{short(x.id, 6, 6)}</span>
                      {x.last_deploy_slot != null && <span className="text-faint">slot {x.last_deploy_slot.toLocaleString('en-US')}</span>}
                      {x.bytecode_sha256 && (
                        <span className="text-muted-foreground" title={x.bytecode_sha256}>
                          {x.bytecode_sha256.slice(0, 12)}
                        </span>
                      )}
                      {adapterKey !== 'ethereum' && <StateBadge state={x.ownership_proven ? 'ok' : 'neutral'}>{x.ownership_proven ? 'owner' : 'not proven'}</StateBadge>}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
          <div className="flex items-center justify-between border-t border-border px-6 py-4">
            <Button variant="ghost" disabled={step === 0} onClick={() => setStep(step - 1)}>
              <ArrowLeft className="size-4" /> Back
            </Button>
            {stepKey === 'target' && (
              <Button onClick={() => setStep(step + 1)}>
                Continue <ArrowRight className="size-4" />
              </Button>
            )}
            {(stepKey === 'source' || stepKey === 'site') && (
              <Button
                disabled={resolving || looking || !(resolved || sourceValid)}
                onClick={async () => {
                  if (resolved || (await resolveSource())) setStep(step + 1)
                }}
              >
                {resolving ? <Mark size={16} className="animate-[spin_6s_linear_infinite]" /> : null} Continue <ArrowRight className="size-4" />
              </Button>
            )}
            {stepKey === 'programs' && (
              <Button
                disabled={!target || fetching || pending.length > 0}
                onClick={async () => {
                  setTree(null)
                  if (await loadTree(false)) setStep(step + 1)
                }}
              >
                Prepare scope <ArrowRight className="size-4" />
              </Button>
            )}
            {stepKey === 'scope' && !last && !isSite && (
              <Button disabled={!tree || selected.size === 0 || overCap} onClick={() => setStep(step + 1)}>
                Review <ArrowRight className="size-4" />
              </Button>
            )}
            {stepKey === 'scope' && !last && isSite && (
              <Button
                disabled={!target || savingScope}
                onClick={async () => {
                  if (await saveScope()) setStep(step + 1)
                }}
              >
                {savingScope ? <Mark size={16} className="animate-[spin_6s_linear_infinite]" /> : null} Review <ArrowRight className="size-4" />
              </Button>
            )}
            {stepKey === 'review' &&
              (canRun ? (
                <Button disabled={queueing || (!isSite && (overCap || !tree))} onClick={() => void queue()}>
                  {queueing ? <Mark size={16} className="animate-[spin_6s_linear_infinite]" /> : null} Queue audit <ArrowRight className="size-4" />
                </Button>
              ) : (
                <Button render={<Link to={returnTo} />}>
                  Buy credits <ArrowRight className="size-4" />
                </Button>
              ))}
          </div>
        </div>

        <aside className="flex flex-col gap-3 lg:sticky lg:top-8 lg:self-start">
          <div className="flex flex-col gap-4 rounded-md border border-border bg-card p-5">
            <span className="label-caps">order</span>
            <span className="num text-2xl">1 audit</span>
            <dl className="flex flex-col gap-2 border-t border-border pt-4 text-xs">
              <Row k="target" v={card?.label ?? '-'} />
              {hasCoverage && <Row k="coverage" v={coverage} />}
              {isSite ? (
                <>
                  <Row k="site" v={originHost || '-'} />
                  <Row k="snapshot" v={target?.revision.commit ? `${target.repo} ${short(target.revision.commit, 7, 0)}` : 'none · live only'} />
                  <Row k={words.programs} v={originHost ? String(scopeHosts.length) : '-'} />
                  <Row k={words.instructions} v={paths.length ? String(paths.length) : 'all'} />
                  <Row k="ownership" v={target?.site?.ownership_proven ? 'proven' : 'not proven'} />
                </>
              ) : (
                <>
                  <Row k="source" v={target ? target.repo : sourceKind === 'installation' ? 'GitHub App' : 'GitHub'} />
                  <Row k="commit" v={target ? short(target.revision.commit, 7, 0) : '-'} />
                  {coverage === 'diff' && <Row k="base" v={base.trim() ? short(base.trim(), 7, 0) : '-'} />}
                  <Row k={words.programs} v={programs.length ? String(programs.length) : 'none · source only'} />
                  <Row k="scope" v={tree ? `${selected.size} files` : '-'} />
                </>
              )}
              <Row k="eta" v="24h" />
              <Row k="pays with" v={canRun ? (costKind === 'slot' ? 'daily slot' : '1 credit') : 'no credits'} tone={canRun ? undefined : 'warn'} />
            </dl>
          </div>
        </aside>
      </div>
    </Page>
  )
}

// One row per program the source declares or the user fetched. The checkbox
// is membership in this audit, up to the tier cap; the highlighted row is the
// one previewed on the right. Two states, two controls.
function ProgramRail({
  programs,
  declared,
  scanned,
  selected,
  busy,
  max,
  onSelect,
  onAdd,
  onRemove,
}: {
  programs: Program[]
  declared: Target['declared']
  scanned: boolean
  selected: string
  busy: boolean
  max: number
  onSelect: (id: string) => void
  onAdd: (id: string) => void
  onRemove: (id: string) => void
}) {
  const rest = (declared ?? []).filter((d) => !programs.some((x) => x.id === d.id))
  const full = programs.length >= max
  const matchWord: Record<string, string> = { verified: 'verified', self_built: 'self-built', mismatch: 'mismatch', unverified: 'unverified' }
  const matchState: Record<string, 'ok' | 'neutral' | 'bad'> = { verified: 'ok', self_built: 'neutral', mismatch: 'bad', unverified: 'neutral' }
  const row = 'grid grid-cols-[16px_minmax(0,1fr)] items-center gap-2.5 rounded pl-2'
  const body = 'grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 py-1.5 pr-2 text-left font-mono text-[12.5px]'
  return (
    <div className="flex flex-col gap-2 lg:self-start">
      <Meter value={programs.length} max={max} label="in audit" className="w-full" />
      <ul className="flex flex-col gap-0.5 rounded-md border border-border bg-card p-1.5">
        {programs.map((x) => {
          const on = x.id === selected
          return (
            <li key={x.id} className={cn(row, on ? 'bg-accent' : 'hover:bg-accent/40')}>
              <Checkbox checked disabled={busy} onCheckedChange={() => onRemove(x.id)} aria-label={`Remove ${x.id} from the audit`} />
              <button type="button" onClick={() => onSelect(x.id)} className={cn(body, 'text-foreground')}>
                <span className="truncate">{short(x.id, 4, 4)}</span>
                <span className="flex items-center gap-1.5">
                  {x.loader?.startsWith('evm') ? null : <span className={cn('size-2 rounded-full', x.ownership_proven ? 'bg-ok' : 'border border-border')} title={x.ownership_proven ? 'ownership proven' : 'ownership not proven'} />}
                  <StateBadge state={matchState[x.verified_build] ?? 'neutral'}>{matchWord[x.verified_build] ?? x.verified_build}</StateBadge>
                </span>
              </button>
            </li>
          )
        })}
        {!scanned && rest.length === 0 && (
          <li className="px-2 py-1.5">
            <StateBadge state="pending">scanning source</StateBadge>
          </li>
        )}
        {rest.map((d) => {
          const dead = d.on_chain === 'absent' || d.on_chain === 'account'
          const blocked = dead || busy || full
          return (
            <li key={d.id} className={cn(row, !blocked && 'hover:bg-accent/40')} title={d.file}>
              <Checkbox checked={false} disabled={blocked} onCheckedChange={() => onAdd(d.id)} aria-label={`Add ${d.id} to the audit`} />
              <button type="button" disabled={blocked} onClick={() => onAdd(d.id)} className={cn(body, 'text-muted-foreground disabled:cursor-not-allowed')}>
                <span className="truncate">{short(d.id, 4, 4)}</span>
                <StateBadge state={d.on_chain === 'absent' ? 'bad' : d.on_chain === 'live' ? (full ? 'warn' : 'ok') : 'neutral'}>
                  {d.on_chain === 'live' ? (full ? `cap ${max}` : 'live') : d.on_chain === 'account' ? 'not a program' : d.on_chain === 'absent' ? 'absent' : 'declared'}
                </StateBadge>
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

// A list the customer writes: a fixed first entry when there is one, the
// entries added so far with a remove mark, and the input that adds one.
// Membership is the whole control; what an entry is reads from its badge.
function ListField({
  label,
  fixed,
  fixedWord,
  itemWord,
  emptyWord,
  items,
  placeholder,
  valid,
  normalize,
  onChange,
}: {
  label: string
  fixed?: string
  fixedWord?: string
  itemWord: string
  emptyWord?: string
  items: string[]
  placeholder: string
  valid: (v: string) => boolean
  normalize: (v: string) => string
  onChange: (items: string[]) => void
}) {
  const [draft, setDraft] = useState('')
  const value = normalize(draft)
  const ok = value !== '' && valid(value) && !items.includes(value)
  function add() {
    if (!ok) return
    onChange([...items, value])
    setDraft('')
  }
  return (
    <Section label={label}>
      <ul className="flex flex-col gap-0.5 rounded-md border border-border bg-card p-1.5">
        {fixed && (
          <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded px-2 py-1.5 font-mono text-[12.5px]">
            <span className="truncate">{fixed}</span>
            <StateBadge state="ok">{fixedWord}</StateBadge>
          </li>
        )}
        {items.map((x) => (
          <li key={x} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 rounded px-2 py-1 font-mono text-[12.5px] hover:bg-accent/40">
            <span className="truncate">{x}</span>
            <StateBadge state="neutral">{itemWord}</StateBadge>
            <Button size="icon-xs" variant="ghost" onClick={() => onChange(items.filter((y) => y !== x))} aria-label={`Remove ${x}`}>
              <X className="size-3.5" />
            </Button>
          </li>
        ))}
        {!fixed && items.length === 0 && emptyWord && (
          <li className="px-2 py-1.5">
            <StateBadge state="neutral">{emptyWord}</StateBadge>
          </li>
        )}
      </ul>
      <div className="flex gap-2">
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={placeholder}
          spellCheck={false}
          aria-label={label}
          className={cn('h-10 font-mono', draft && !ok && 'border-bad')}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return
            e.preventDefault()
            add()
          }}
        />
        <Button className="h-10" disabled={!ok} onClick={add}>
          <Plus className="size-4" /> Add
        </Button>
      </div>
    </Section>
  )
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <span className="label-caps">{label}</span>
      {children}
    </div>
  )
}

function Row({ k, v, tone }: { k: string; v: string; tone?: 'warn' }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-muted-foreground">{k}</dt>
      <dd className={cn('num truncate text-right', tone === 'warn' && 'text-warn')}>{v}</dd>
    </div>
  )
}

function InstallationPicker({ installs, value, onChange, repo, onRepo }: { installs: Installation[]; value: string; onChange: (v: string) => void; repo: string; onRepo: (v: string) => void }) {
  const installUrl = $api.useQuery('get', '/v1/me/github/install-url')
  if (installs.length === 0) {
    return (
      <div className="flex flex-wrap items-center gap-3 rounded-md border border-dashed border-border px-5 py-5">
        <StateBadge state="neutral">0 installations</StateBadge>
        <Button size="sm" variant="outline" render={<a href={installUrl.data?.url ?? '#'} />} disabled={!installUrl.data?.url}>
          Install the GitHub App
        </Button>
      </div>
    )
  }
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-[220px_1fr]">
      <Section label="installation">
        <Select value={value} onValueChange={(v) => v && onChange(v)}>
          <SelectTrigger className="h-10" aria-label="Installation">
            <SelectValue placeholder="account" />
          </SelectTrigger>
          <SelectContent>
            {installs.map((i) => (
              <SelectItem key={i.id} value={i.id}>
                {i.account_login} {i.status !== 'active' && <span className="ml-2 text-warn">{i.status}</span>}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Section>
      <Section label="repository">
        <Input value={repo} onChange={(e) => onRepo(e.target.value)} placeholder="owner/name" spellCheck={false} aria-label="Repository" className="h-10 font-mono" />
      </Section>
    </div>
  )
}
