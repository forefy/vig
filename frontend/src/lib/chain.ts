// The chain's own words for the things vig draws, chosen by the target's
// adapter. Solana is adapter one and its words are the defaults.
export type Adapter = 'solana' | 'ethereum' | (string & {}) | undefined

export type ChainWords = {
  evm: boolean
  web: boolean
  program: string
  programs: string
  instruction: string
  instructions: string
  height: string
  signer: string
  signers: string
  calls: string
  hash: string
  idl: string
  stageVerified: string
  recorded: string
  drift: string
  exploitable: Record<string, string>
}

export function chainWords(adapter: Adapter): ChainWords {
  const evm = adapter === 'ethereum'
  // A web target has no chain: its places are hosts and routes, and the
  // facets of a report fold its findings by those.
  const web = adapter === 'webapp'
  // A plain repository has neither: its places are packages and functions.
  const repo = adapter === 'repo'
  return {
    evm,
    web,
    program: web ? 'host' : evm ? 'contract' : repo ? 'package' : 'program',
    programs: web ? 'hosts' : evm ? 'contracts' : repo ? 'packages' : 'programs',
    instruction: web ? 'route' : evm || repo ? 'function' : 'instruction',
    instructions: web ? 'routes' : evm || repo ? 'functions' : 'instructions',
    height: evm ? 'block' : 'slot',
    signer: evm ? 'sender' : 'signer',
    signers: evm ? 'senders' : 'signers',
    calls: evm ? 'calls out' : 'cpi',
    hash: evm ? 'keccak' : 'sha256',
    idl: evm ? 'ABI' : 'IDL',
    stageVerified: evm ? 'Contracts verified' : 'Programs verified',
    recorded: evm ? 'contracts recorded' : 'programs recorded',
    drift: evm ? 'contract changed' : 'program changed',
    // Who can reach a finding, least to most privileged, in the words of
    // the target: a site has sessions and keys where a chain has signers.
    exploitable: {
      anyone: web ? 'anyone, no account' : 'anyone',
      any_user: web ? 'any signed-in user' : 'any user',
      internal: 'insider (repo or telemetry)',
      any_signer: web ? 'any session' : evm ? 'any account' : 'any signer',
      account_owner: web ? 'account member' : evm ? 'position owner' : 'account owner',
      api_key: 'API key holder',
      internal_network: 'internal network',
      admin: web ? 'account admin' : 'admin',
      upgrade_authority: web ? 'platform operator' : evm ? 'upgrade admin' : 'upgrade authority',
      other_program: web ? 'another service' : evm ? 'other contract' : 'other program',
      none: 'no one',
    },
  }
}

// What a source scan counted, in the chain's words: the keys of the
// summary that mean something on that chain, in display order.
export function sourceSummaryWords(adapter: Adapter): { key: string; word: string; tone?: 'warn' | 'bad' }[] {
  if (adapter === 'ethereum') {
    return [
      { key: 'programs', word: 'contracts' },
      { key: 'instructions', word: 'functions' },
      { key: 'raw', word: 'unchecked', tone: 'warn' },
      { key: 'cpi', word: 'calls out' },
      { key: 'signed_cpi', word: 'delegatecalls', tone: 'warn' },
      { key: 'token', word: 'transfers' },
      { key: 'unsafe', word: 'assembly', tone: 'bad' },
    ]
  }
  return [
    { key: 'programs', word: 'programs' },
    { key: 'instructions', word: 'instructions' },
    { key: 'accounts', word: 'accounts' },
    { key: 'raw', word: 'raw', tone: 'warn' },
    { key: 'cpi', word: 'cpi' },
    { key: 'signed_cpi', word: 'signed', tone: 'warn' },
    { key: 'pda', word: 'pda' },
    { key: 'token', word: 'token ops' },
    { key: 'unsafe', word: 'unsafe', tone: 'bad' },
  ]
}
