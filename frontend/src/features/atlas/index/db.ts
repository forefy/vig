// A DuckDB-WASM session over the owner's index. The engine and its worker
// come from node_modules through Vite asset URLs, nothing from a CDN, and
// the engine module itself is imported only when a session opens, so the
// map view never pays for it. Every Parquet part is registered by its API
// URL (same origin, so the session cookies ride along and DuckDB takes
// footers and row groups by Range) under a hive path that carries the
// build's audit id as `target`: a question's {{target}} is the audit id,
// and that path value overrides whatever slug zz wrote into the file.
import type { AsyncDuckDB, AsyncDuckDBConnection } from '@duckdb/duckdb-wasm'
import type { Table as ArrowTable } from 'apache-arrow'
import ehWasm from '@duckdb/duckdb-wasm/dist/duckdb-eh.wasm?url'
import ehWorker from '@duckdb/duckdb-wasm/dist/duckdb-browser-eh.worker.js?url'
import type { components } from '@/api/client'
import { apiOriginAbs } from '@/api/client'
import { TABLES, emptyViewSql } from './schema'

export type IndexBuild = components['schemas']['IndexBuild']

export type Result = { columns: string[]; types: string[]; rows: Record<string, unknown>[]; ms: number }

export class Session {
  private queue: Promise<unknown> = Promise.resolve()
  private readonly db: AsyncDuckDB
  private readonly conn: AsyncDuckDBConnection
  private readonly worker: Worker
  readonly version: string
  readonly tables: string[]

  private constructor(db: AsyncDuckDB, conn: AsyncDuckDBConnection, worker: Worker, version: string, tables: string[]) {
    this.db = db
    this.conn = conn
    this.worker = worker
    this.version = version
    this.tables = tables
  }

  static async open(builds: IndexBuild[]): Promise<Session> {
    const duckdb = await import('@duckdb/duckdb-wasm')
    const worker = new Worker(ehWorker)
    const db = new duckdb.AsyncDuckDB(new duckdb.VoidLogger(), worker)
    await db.instantiate(ehWasm)
    await db.open({
      path: ':memory:',
      query: { castBigIntToDouble: false, castTimestampToDate: false },
      filesystem: { reliableHeadRequests: true, allowFullHTTPReads: true },
    })
    const version = await db.getVersion()
    const conn = await db.connect()

    const files = new Map<string, string[]>()
    for (const t of TABLES) files.set(t, [])
    const origin = apiOriginAbs()
    for (const b of builds) {
      for (const t of b.tables ?? []) {
        const list = files.get(t.name) ?? []
        for (const p of t.parts ?? []) {
          const name = `${t.name}/target=${b.audit_id}/${p.file}`
          await db.registerFileURL(name, new URL(p.url, origin).toString(), duckdb.DuckDBDataProtocol.HTTP, false)
          list.push(name)
        }
        files.set(t.name, list)
      }
    }
    for (const [table, parts] of files) {
      const sql = parts.length
        ? `CREATE OR REPLACE VIEW "${table}" AS SELECT * FROM read_parquet([${parts.map(lit).join(', ')}], hive_partitioning=true, union_by_name=true)`
        : `CREATE OR REPLACE VIEW "${table}" AS ${emptyViewSql(table)}`
      await conn.query(sql)
    }
    return new Session(db, conn, worker, version, [...files.keys()])
  }

  // One connection is not reentrant: runs are serialised.
  run(sql: string): Promise<Result> {
    const p = this.queue.then(() => this.runNow(sql))
    this.queue = p.catch(() => undefined)
    return p
  }

  private async runNow(sql: string): Promise<Result> {
    const t0 = performance.now()
    // DuckDB-WASM returns the last result set only; leading statements run on their own.
    let table: ArrowTable | null = null
    for (const s of splitStatements(sql)) table = await this.conn.query(s)
    const ms = performance.now() - t0
    if (!table) return { columns: [], types: [], rows: [], ms }
    return { columns: table.schema.fields.map((f) => f.name), types: table.schema.fields.map((f) => String(f.type)), rows: rowsOf(table), ms }
  }

  async close(): Promise<void> {
    try {
      await this.conn.close()
      await this.db.terminate()
    } finally {
      this.worker.terminate()
    }
  }
}

export const lit = (v: string) => `'${v.replace(/'/g, "''")}'`

export function errorText(e: unknown): string {
  const m = e instanceof Error ? e.message : String(e)
  return m.replace(/^(Binder|Catalog|Parser|Conversion|Invalid Input|IO) Error:\s*/, '').trim()
}

// Split on ';' outside quotes and line comments.
export function splitStatements(sql: string): string[] {
  const out: string[] = []
  let cur = ''
  let q: string | null = null
  for (let i = 0; i < sql.length; i++) {
    const ch = sql[i]!
    if (q) {
      cur += ch
      if (ch === q) q = null
      continue
    }
    if (ch === "'" || ch === '"') {
      q = ch
      cur += ch
      continue
    }
    if (ch === '-' && sql[i + 1] === '-') {
      const end = sql.indexOf('\n', i)
      const stop = end === -1 ? sql.length : end
      cur += sql.slice(i, stop)
      i = stop - 1
      continue
    }
    if (ch === ';') {
      if (cur.trim()) out.push(cur.trim())
      cur = ''
      continue
    }
    cur += ch
  }
  if (cur.trim()) out.push(cur.trim())
  return out.length ? out : [sql]
}

// Arrow table to plain rows: BigInt, Date, vectors, maps and structs as JSON-safe values.
function rowsOf(table: ArrowTable): Record<string, unknown>[] {
  const fields = table.schema.fields.map((f) => f.name)
  const out: Record<string, unknown>[] = []
  for (const batch of table.batches) {
    const cols = fields.map((_, c) => batch.getChildAt(c))
    for (let r = 0; r < batch.numRows; r++) {
      const row: Record<string, unknown> = {}
      for (let c = 0; c < fields.length; c++) row[fields[c]!] = plain(cols[c]?.get(r))
      out.push(row)
    }
  }
  return out
}

export function plain(v: unknown): unknown {
  if (v === null || v === undefined) return null
  const t = typeof v
  if (t === 'bigint') {
    const n = Number(v)
    return Number.isSafeInteger(n) ? n : (v as bigint).toString()
  }
  if (t !== 'object') return v
  if (v instanceof Date) return v.toISOString()
  if (v instanceof Uint8Array) return `${v.length} bytes`
  if (Array.isArray(v)) return v.map(plain)
  if (v instanceof Map) {
    const o: Record<string, unknown> = {}
    for (const [k, val] of v) o[String(k)] = plain(val)
    return o
  }
  const any = v as { toArray?: () => unknown[]; toJSON?: () => unknown }
  if (typeof any.toArray === 'function') return any.toArray().map(plain)
  if (typeof any.toJSON === 'function') return plain(any.toJSON())
  const o: Record<string, unknown> = {}
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) o[k] = plain(val)
  return o
}
