// A DuckDB-WASM session over the owner's index parts. The engine and its
// worker come from node_modules through Vite asset URLs, nothing from a
// CDN. Every Parquet part is registered by its API URL (same origin, so
// the session cookie rides along; the URL carries the part's digest, so
// the browser keeps what it fetched) under a hive path whose `target` is
// the build's audit id, and each table becomes a view over every build's
// parts. A scope narrows the
// views to one build; the statement stays the same, and DuckDB reads only
// that build's parts.
import type { AsyncDuckDB, AsyncDuckDBConnection } from '@duckdb/duckdb-wasm'
import ehWasm from '@duckdb/duckdb-wasm/dist/duckdb-eh.wasm?url'
import ehWorker from '@duckdb/duckdb-wasm/dist/duckdb-browser-eh.worker.js?url'
import type { components } from '@/api/client'
import { apiOriginAbs } from '@/api/client'

export type IndexBuild = components['schemas']['IndexBuild']

export type Result = { columns: string[]; types: string[]; rows: Record<string, unknown>[]; ms: number }

export type Column = { name: string; type: string }

export const lit = (v: string) => `'${v.replace(/'/g, "''")}'`

export class Session {
  private queue: Promise<unknown> = Promise.resolve()
  private readonly db: AsyncDuckDB
  private readonly conn: AsyncDuckDBConnection
  private readonly worker: Worker
  private readonly files: Map<string, string[]>
  readonly version: string
  readonly tables: string[]
  // The build the views are narrowed to; null is every build.
  target: string | null = null

  private constructor(db: AsyncDuckDB, conn: AsyncDuckDBConnection, worker: Worker, version: string, files: Map<string, string[]>) {
    this.db = db
    this.conn = conn
    this.worker = worker
    this.version = version
    this.files = files
    this.tables = [...files.keys()].sort()
  }

  static async open(builds: IndexBuild[]): Promise<Session> {
    const duckdb = await import('@duckdb/duckdb-wasm')
    const worker = new Worker(ehWorker)
    const db = new duckdb.AsyncDuckDB(new duckdb.VoidLogger(), worker)
    await db.instantiate(ehWasm)
    await db.open({
      path: ':memory:',
      query: { castBigIntToDouble: false, castTimestampToDate: true },
      // A part is read whole, once, and kept in the engine's memory: every
      // statement after that is local. Reading by Range instead (full reads
      // off) costs more, not less, because the engine keeps nothing between
      // statements and fetches the same row groups again for every one; the
      // open alone moved more bytes than the whole index that way. The
      // server still answers HEAD and Range for an engine that reads so,
      // and the part URLs carry their digest, so a browser keeps the parts
      // across visits and a second open fetches nothing.
      filesystem: { reliableHeadRequests: true, allowFullHTTPReads: true },
    })
    const version = await db.getVersion()
    const conn = await db.connect()
    const files = new Map<string, string[]>()
    const origin = apiOriginAbs()
    for (const b of builds) {
      for (const t of b.tables ?? []) {
        const list = files.get(t.name) ?? []
        for (const p of t.parts ?? []) {
          const name = `${t.name}/target=${b.audit_id}/${p.file}`
          // Direct: every read is an HTTP Range over the part, never a copy of it.
          await db.registerFileURL(name, new URL(p.url, origin).toString(), duckdb.DuckDBDataProtocol.HTTP, true)
          list.push(name)
        }
        files.set(t.name, list)
      }
    }
    const s = new Session(db, conn, worker, version, files)
    await s.views(null)
    return s
  }

  // across is a table over every one of its parts whatever the scope, for
  // a count per build on the rail.
  across(table: string): string {
    const parts = this.files.get(table) ?? []
    return `read_parquet([${parts.map(lit).join(', ')}], hive_partitioning=true, union_by_name=true)`
  }

  // views makes every table a view over its parts, narrowed to one build
  // when a target is given: the filter sits in the view, so DuckDB prunes
  // the other builds' parts by their hive path.
  private async views(target: string | null): Promise<void> {
    for (const table of this.tables) {
      const parts = this.files.get(table) ?? []
      const where = target === null ? '' : ` WHERE target = ${lit(target)}`
      await this.conn.query(`CREATE OR REPLACE VIEW "${table}" AS SELECT * FROM read_parquet([${parts.map(lit).join(', ')}], hive_partitioning=true, union_by_name=true)${where}`)
    }
    this.target = target
  }

  // scope narrows the views to one build, or widens them to every build
  // with null, after whatever is running. It says whether anything changed.
  scope(target: string | null): Promise<boolean> {
    const p = this.queue.then(async () => {
      if (target === this.target) return false
      await this.views(target)
      return true
    })
    this.queue = p.catch(() => undefined)
    return p
  }

  // One connection is not reentrant: runs are serialised. A box holding
  // several statements runs them in order and shows the last; an error in
  // any stops there.
  run(sql: string): Promise<Result> {
    const p = this.queue.then(async () => {
      const parts = splitStatements(sql)
      let out: Result | null = null
      let ms = 0
      for (const part of parts.length ? parts : [sql]) {
        out = await this.runNow(part)
        ms += out.ms
      }
      return { ...out!, ms }
    })
    this.queue = p.catch(() => undefined)
    return p
  }

  // Rows are read column by column, so two columns of one name both
  // survive (the second as `name_1`), and a DECIMAL arrives as its value
  // rather than its unscaled digits.
  private async runNow(sql: string): Promise<Result> {
    const started = performance.now()
    const t = await this.conn.query(sql)
    const columns = uniqueNames(t.schema.fields.map((f) => f.name))
    const types = t.schema.fields.map((f) => typeName(f.type))
    const rows: Record<string, unknown>[] = Array.from({ length: t.numRows }, () => ({}))
    t.schema.fields.forEach((f, i) => {
      const v = t.getChildAt(i)
      if (!v) return
      const scale = decimalScale(f.type)
      const name = columns[i]!
      for (let j = 0; j < t.numRows; j++) {
        const x = v.get(j)
        rows[j]![name] = scale === null ? x : decimalOf(x, scale)
      }
    })
    return { columns, types, rows, ms: performance.now() - started }
  }

  // The columns of a table, as the parts declare them.
  async columns(table: string): Promise<Column[]> {
    const r = await this.run(`DESCRIBE SELECT * FROM "${table}"`)
    return r.rows.map((row) => ({ name: String(row.column_name), type: String(row.column_type) }))
  }

  // How many rows a table holds in the scope.
  async count(table: string): Promise<number> {
    const r = await this.run(`SELECT count(*) AS n FROM "${table}"`)
    return Number(r.rows[0]?.n ?? 0)
  }

  async close(): Promise<void> {
    await this.conn.close()
    await this.db.terminate()
    this.worker.terminate()
  }
}

// uniqueNames keeps every column of a result: a repeated name takes a
// numbered suffix.
export function uniqueNames(names: string[]): string[] {
  const seen = new Map<string, number>()
  return names.map((n) => {
    const k = seen.get(n) ?? 0
    seen.set(n, k + 1)
    return k === 0 ? n : `${n}_${k}`
  })
}

// typeName is a column's type as SQL would say it: a DECIMAL by its
// precision and scale, anything else as Arrow names it.
export function typeName(type: unknown): string {
  const t = type as { precision?: unknown; scale?: unknown }
  if (typeof t.scale === 'number' && typeof t.precision === 'number' && /decimal/i.test(String(type))) return `DECIMAL(${t.precision},${t.scale})`
  return String(type)
}

// decimalScale is the scale of a DECIMAL column's Arrow type, or null for
// any other type.
function decimalScale(type: unknown): number | null {
  const t = type as { scale?: unknown }
  return typeof t.scale === 'number' && /decimal/i.test(String(type)) ? t.scale : null
}

// decimalOf is a DECIMAL as a number: Arrow hands the unscaled integer as
// a bigint, a number, a string, or little-endian 32-bit words in two's
// complement.
export function decimalOf(v: unknown, scale: number): number | null {
  if (v === null || v === undefined) return null
  let n: bigint
  if (typeof v === 'bigint') n = v
  else if (typeof v === 'number') n = BigInt(Math.trunc(v))
  else if (typeof v === 'string') n = BigInt(v)
  else if (v instanceof Uint32Array || v instanceof Int32Array || Array.isArray(v)) {
    const words = Array.from(v as ArrayLike<number>)
    n = 0n
    for (let i = words.length - 1; i >= 0; i--) n = (n << 32n) | BigInt(words[i]! >>> 0)
    if (words.length && (words[words.length - 1]! & 0x80000000) !== 0) n -= 1n << BigInt(words.length * 32)
  } else return null
  return Number(n) / 10 ** scale
}

// splitStatements cuts a box into its statements at the semicolons that
// sit outside quotes and comments, dropping empty ones.
export function splitStatements(sql: string): string[] {
  const out: string[] = []
  let cur = ''
  let i = 0
  while (i < sql.length) {
    const c = sql[i]!
    const next = sql[i + 1]
    if (c === "'" || c === '"') {
      let j = i + 1
      while (j < sql.length && sql[j] !== c) j++
      cur += sql.slice(i, j + 1)
      i = j + 1
    } else if (c === '-' && next === '-') {
      const j = sql.indexOf('\n', i)
      cur += j < 0 ? sql.slice(i) : sql.slice(i, j)
      i = j < 0 ? sql.length : j
    } else if (c === '/' && next === '*') {
      const j = sql.indexOf('*/', i + 2)
      cur += j < 0 ? sql.slice(i) : sql.slice(i, j + 2)
      i = j < 0 ? sql.length : j + 2
    } else if (c === ';') {
      if (cur.trim()) out.push(cur.trim())
      cur = ''
      i++
    } else {
      cur += c
      i++
    }
  }
  if (cur.trim()) out.push(cur.trim())
  return out
}

// errorText is DuckDB's message without its wrapper lines: the message
// and the lines it points at, not a block the engine repeats (a syntax
// error names its line twice) and not its advice paragraphs (an out of
// memory error explains the settings to try).
export function errorText(e: unknown): string {
  const m = e instanceof Error ? e.message : String(e)
  // Out of memory says what it failed to allocate on its first line; the
  // rest is the engine's advice on settings the page does not offer.
  if (/^Out of Memory Error: /.test(m)) return m.replace(/^Out of Memory Error: /, '').split('\n')[0]!.trim()
  const text = m.replace(/^(Catalog |Binder |Parser |Conversion |Invalid Input |IO )?Error: /, '')
  const blocks = text.split(/\n\s*\n/)
  const kept = blocks.filter((b, i) => (i === 0 || /^LINE \d+:/.test(b.trim())) && (i === 0 || b.trim() !== blocks[i - 1]!.trim()))
  return kept.join('\n\n')
}

// cellText is one value as the result table shows it: text as is, a
// number as digits, a date in UTC, a list or a map as JSON. A timestamp
// or date column's number is the epoch in milliseconds, shown as a date.
export function cellText(v: unknown, type?: string): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  if (type && /timestamp|date/i.test(type) && (typeof v === 'number' || typeof v === 'bigint')) return new Date(Number(v)).toISOString()
  if (typeof v === 'bigint' || typeof v === 'number' || typeof v === 'boolean') return String(v)
  if (v instanceof Date) return v.toISOString()
  if (typeof v === 'object' && v !== null && 'toJSON' in v && typeof (v as { toJSON: unknown }).toJSON === 'function') return cellText((v as { toJSON: () => unknown }).toJSON())
  try {
    return JSON.stringify(v, (_k, x) => (typeof x === 'bigint' ? Number(x) : x))
  } catch {
    return String(v)
  }
}

// csv is a result as a file: the columns, then every row, quoted where a
// value holds a comma, a quote or a line break.
export function csv(result: Result): string {
  const q = (v: unknown, type?: string) => {
    const t = cellText(v, type)
    return /[",\n\r]/.test(t) ? `"${t.replace(/"/g, '""')}"` : t
  }
  return [result.columns.map((c) => q(c)).join(','), ...result.rows.map((r) => result.columns.map((c, i) => q(r[c], result.types[i])).join(','))].join('\n')
}
