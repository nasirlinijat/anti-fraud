// Types and calls for the score API described in openapi.yaml.

export type Outcome = 'allow' | 'review' | 'block'

export interface FiredRule {
  name: string
  points: number
}

export interface ScoreResponse {
  transaction_id: string
  outcome: Outcome
  score: number
  fired_rules: FiredRule[]
  ruleset_version: number
  model_score: number | null
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

// getStats reads the dashboard statistics for a period. The development server
// adds the API key.
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

// The development server adds the API key to the blocklist requests.
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

// score sends one score request. The development server adds the API key.
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
    error.error += '. Set API_KEY in the .env file and restart npm run dev.'
  }
  return { ok: false, status: res.status, error }
}
