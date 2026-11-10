// Types and calls for the APIs described in openapi.yaml.

export type Outcome = 'allow' | 'review' | 'block'

export interface FiredRule {
  name: string
  points: number
  message: string
}

export interface ScoreResponse {
  transaction_id: string
  outcome: Outcome
  score: number
  fired_rules: FiredRule[]
  ruleset_version: number
  model_score: number | null
  model_version: string | null
}

export interface ErrorResponse {
  error: string
  fields?: Record<string, string>
}

export const periods = ['15m', '1h', '24h', '7d', 'all'] as const
export type Period = (typeof periods)[number]

export interface Stats {
  period: Period
  since: string
  generated_at: string
  outcomes: { allow: number; review: number; block: number; total: number }
  top_rules: { name: string; fired: number; points: number }[]
  review_queue: number
  model: { scored: number; flagged: number; cleared: number }
}

// getStats reads the dashboard statistics for a period. The proxy adds the API key.
export async function getStats(period: Period): Promise<Stats> {
  const res = await fetch(`/stats?period=${period}`)
  if (!res.ok) {
    throw new Error(`The backend answered with status ${res.status}.`)
  }
  return (await res.json()) as Stats
}

export const entryTypes = ['card', 'device', 'player', 'ip'] as const
export type EntryType = (typeof entryTypes)[number]

export interface BlocklistEntry {
  id: number
  type: EntryType
  value: string
  reason: string
  created_at: string
}

export type Saved<T> = { ok: true; value: T } | { ok: false; error: ErrorResponse }

async function errorFrom(res: Response): Promise<ErrorResponse> {
  const body: unknown = await res.json().catch(() => null)
  return body && typeof body === 'object' && 'error' in body
    ? (body as ErrorResponse)
    : { error: `The backend answered with status ${res.status}.` }
}

export interface RuleSettings {
  enabled: boolean
  weight: number
  limit?: bigint
}

export interface RulesetConfig {
  review_threshold: number
  block_threshold: number
  rules: Record<string, RuleSettings>
}

export interface Ruleset extends RulesetConfig {
  version: number
}

// The API sends 64-bit limits as JSON integers. Keep their exact value in the editor.
const rulesetJSON = JSON as JSON & {
  parse(text: string, reviver: (key: string, value: unknown, context?: { source: string }) => unknown): unknown
  rawJSON?(text: string): unknown
}

async function readRuleset(res: Response): Promise<Ruleset> {
  return rulesetJSON.parse(await res.text(), (key: string, value: unknown, context?: { source: string }) => {
    if (key !== 'limit' || typeof value !== 'number') return value
    if (Number.isSafeInteger(value)) return BigInt(value)
    if (context) return BigInt(context.source)
    throw new Error('This browser cannot read the rule limit exactly.')
  }) as Ruleset
}

export async function getActiveRuleset(): Promise<Ruleset> {
  const res = await fetch('/rulesets/active')
  if (!res.ok) throw new Error((await errorFrom(res)).error)
  return readRuleset(res)
}

export async function publishRuleset(config: RulesetConfig): Promise<Saved<Ruleset>> {
  try {
    const res = await fetch('/rulesets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config, (_key, value: unknown) => {
        if (typeof value !== 'bigint') return value
        const number = Number(value)
        if (Number.isSafeInteger(number)) return number
        if (rulesetJSON.rawJSON) return rulesetJSON.rawJSON(value.toString())
        throw new Error('This browser cannot save the rule limit exactly.')
      }),
    })
    if (!res.ok) return { ok: false, error: await errorFrom(res) }
    return { ok: true, value: await readRuleset(res) }
  } catch (error) {
    return {
      ok: false,
      error: { error: error instanceof TypeError ? 'The backend cannot be reached. Is it running?' : String(error) },
    }
  }
}

// The proxy adds the API key to the blocklist requests.
export async function listBlocklist(): Promise<BlocklistEntry[]> {
  const res = await fetch('/blocklist')
  if (!res.ok) {
    throw new Error((await errorFrom(res)).error)
  }
  return ((await res.json()) as { entries: BlocklistEntry[] }).entries
}

export async function addBlocklistEntry(entry: { type: string; value: string; reason: string }): Promise<Saved<BlocklistEntry>> {
  try {
    const res = await fetch('/blocklist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(entry),
    })
    if (!res.ok) {
      return { ok: false, error: await errorFrom(res) }
    }
    return { ok: true, value: (await res.json()) as BlocklistEntry }
  } catch {
    return { ok: false, error: { error: 'The backend cannot be reached. Is it running?' } }
  }
}

export async function removeBlocklistEntry(id: number): Promise<Saved<null>> {
  try {
    const res = await fetch(`/blocklist/${id}`, { method: 'DELETE' })
    if (!res.ok) {
      return { ok: false, error: await errorFrom(res) }
    }
    return { ok: true, value: null }
  } catch {
    return { ok: false, error: { error: 'The backend cannot be reached. Is it running?' } }
  }
}

export type ScoreResult =
  | { ok: true; response: ScoreResponse }
  | { ok: false; status: number; error: ErrorResponse }

// score sends one score request. The proxy adds the API key.
export async function score(request: Record<string, unknown>): Promise<ScoreResult> {
  let res: Response
  try {
    res = await fetch('/score', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(request),
    })
  } catch {
    return { ok: false, status: 0, error: { error: 'The backend cannot be reached. Is it running?' } }
  }

  const body: unknown = await res.json().catch(() => null)
  if (res.ok) {
    return { ok: true, response: body as ScoreResponse }
  }
  const error =
    body && typeof body === 'object' && 'error' in body
      ? (body as ErrorResponse)
      : { error: `The backend answered with status ${res.status}.` }
  if (res.status === 401) {
    error.error += '. Check API_KEY in .env and restart the console and backend.'
  }
  return { ok: false, status: res.status, error }
}
