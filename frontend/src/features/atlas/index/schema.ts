// The index tables as zz writes them (docs/SCHEMA.md in the zz repository).
// Real tables are read from Parquet with union_by_name, so this never shapes
// data; it only gives a table with no parts yet an empty, typed view, so a
// question that names it still parses and runs.

export const COMMON_COLS: Record<string, string> = { target: 'VARCHAR', ingested_at: 'TIMESTAMP' }

export const TABLE_SCHEMAS: Record<string, Record<string, string>> = {
  repos: {
    repo: 'VARCHAR', commit: 'VARCHAR', default_branch: 'VARCHAR', language: 'VARCHAR', stars: 'INTEGER',
    fork: 'BOOLEAN', size_kb: 'INTEGER', fetched_at: 'TIMESTAMP', prefilter_hit: 'BOOLEAN', source_blob: 'VARCHAR',
  },
  files: { repo: 'VARCHAR', path: 'VARCHAR', lang: 'VARCHAR', bytes: 'INTEGER', sha256: 'VARCHAR', loc: 'INTEGER' },
  functions: {
    repo: 'VARCHAR', path: 'VARCHAR', fn_id: 'VARCHAR', name: 'VARCHAR', qualname: 'VARCHAR', params: 'VARCHAR[]',
    line_start: 'INTEGER', line_end: 'INTEGER', is_exported: 'BOOLEAN', is_handler: 'BOOLEAN',
  },
  calls: {
    repo: 'VARCHAR', path: 'VARCHAR', call_id: 'VARCHAR', caller_fn: 'VARCHAR', callee_name: 'VARCHAR', callee_fn: 'VARCHAR',
    resolution: 'VARCHAR', args: 'VARCHAR[]', arg_kinds: 'VARCHAR[]', line: 'INTEGER', col: 'INTEGER',
  },
  flows: {
    repo: 'VARCHAR', path: 'VARCHAR', fn_id: 'VARCHAR', src: 'VARCHAR', dst: 'VARCHAR', kind: 'VARCHAR',
    arg_index: 'INTEGER', line: 'INTEGER',
  },
  imports: { repo: 'VARCHAR', path: 'VARCHAR', module: 'VARCHAR', name: 'VARCHAR', alias: 'VARCHAR', line: 'INTEGER' },
  literals: { repo: 'VARCHAR', path: 'VARCHAR', value: 'VARCHAR', kind: 'VARCHAR', line: 'INTEGER' },
  findings: {
    repo: 'VARCHAR', path: 'VARCHAR', rule_id: 'VARCHAR', severity: 'VARCHAR', line_start: 'INTEGER', line_end: 'INTEGER',
    message: 'VARCHAR', snippet: 'VARCHAR',
  },
  hosts: { host: 'VARCHAR', first_seen: 'TIMESTAMP', last_seen: 'TIMESTAMP', sources: 'VARCHAR[]' },
  har_entries: {
    session: 'VARCHAR', entry_id: 'VARCHAR', started_at: 'TIMESTAMP', method: 'VARCHAR', scheme: 'VARCHAR', host: 'VARCHAR',
    path: 'VARCHAR', query: 'VARCHAR', status: 'INTEGER', req_content_type: 'VARCHAR', resp_content_type: 'VARCHAR',
    req_headers: 'MAP(VARCHAR, VARCHAR)', resp_headers: 'MAP(VARCHAR, VARCHAR)', cookie_names: 'VARCHAR[]',
    req_body_sha256: 'VARCHAR', resp_body_sha256: 'VARCHAR', resp_body_bytes: 'INTEGER', time_ms: 'DOUBLE', page_ref: 'VARCHAR',
  },
  bodies: {
    sha256: 'VARCHAR', bytes: 'INTEGER', content_type: 'VARCHAR', is_text: 'BOOLEAN', first_seen: 'TIMESTAMP', blob_pack: 'VARCHAR',
  },
  domains: { apex: 'VARCHAR', registrar: 'VARCHAR', ns: 'VARCHAR', created: 'DATE', expires: 'DATE' },
  subdomains: {
    apex: 'VARCHAR', fqdn: 'VARCHAR', a: 'VARCHAR[]', cname: 'VARCHAR', asn: 'INTEGER', cdn: 'VARCHAR', ports: 'INTEGER[]',
    http_status: 'INTEGER', http_title: 'VARCHAR', tech: 'VARCHAR[]', source: 'VARCHAR', first_seen: 'TIMESTAMP', last_seen: 'TIMESTAMP',
  },
  cloud_accounts: { provider: 'VARCHAR', account_id: 'VARCHAR', alias: 'VARCHAR', snapshot: 'TIMESTAMP' },
  cloud_resources: {
    provider: 'VARCHAR', account_id: 'VARCHAR', region: 'VARCHAR', service: 'VARCHAR', rtype: 'VARCHAR', rid: 'VARCHAR',
    name: 'VARCHAR', public: 'BOOLEAN', hostnames: 'VARCHAR[]', tags: 'MAP(VARCHAR, VARCHAR)', raw_blob: 'VARCHAR', snapshot: 'TIMESTAMP',
  },
  edges: {
    src_kind: 'VARCHAR', src_id: 'VARCHAR', dst_kind: 'VARCHAR', dst_id: 'VARCHAR', rel: 'VARCHAR', confidence: 'DOUBLE', evidence: 'VARCHAR',
  },
}

export const TABLES = Object.keys(TABLE_SCHEMAS)

// `SELECT NULL::T AS col, ... WHERE false` for a table with no parts.
export function emptyViewSql(table: string): string {
  const cols = { ...COMMON_COLS, ...(TABLE_SCHEMAS[table] ?? {}) }
  const sel = Object.entries(cols)
    .map(([c, t]) => `NULL::${t} AS "${c}"`)
    .join(', ')
  return `SELECT ${sel} WHERE false`
}
