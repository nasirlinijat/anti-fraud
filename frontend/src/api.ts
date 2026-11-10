// Types and calls for the APIs described in openapi.yaml.

import { ApiError, request, toErrorResponse, type ErrorResponse } from './request.ts'

export type { ErrorResponse } from './request.ts'

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

export const periods = ['15m', '1h', '24h', '7d', 'all'] as const
export type Period = (typeof periods)[number]

export interface DecisionSeries {
  bucket_seconds: number
  buckets: { start: string; allow: number; review: number; block: number }[]
}

export interface Stats {
  period: Period
  since: string
  generated_at: string
  outcomes: { allow: number; review: number; block: number; total: number }
  top_rules: { name: string; fired: number; points: number }[]
  review_queue: number
  model: { scored: number; flagged: number; cleared: number }
  p95_scoring_latency_us: number | null
  blocked_value_minor: string
  decision_series: DecisionSeries
}

// getStats reads the dashboard statistics for a period. The proxy adds the API key.
export function getStats(period: Period, signal?: AbortSignal): Promise<Stats> {
  return request(`/stats?period=${period}`, { signal })
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

async function save<T>(operation: () => Promise<T>): Promise<Saved<T>> {
  try {
    return { ok: true, value: await operation() }
  } catch (error) {
    return { ok: false, error: toErrorResponse(error) }
  }
}

export interface RuleSettings {
  enabled: boolean
  weight: number
  limit?: bigint
  window_days?: number
  min_amount_minor?: bigint
  amount_multiplier?: number
}

export interface RulesetConfig {
  review_threshold: number
  block_threshold: number
  rules: Record<string, RuleSettings>
}

export interface Ruleset extends RulesetConfig {
  version: number
}

// The API sends 64-bit thresholds as JSON integers. Keep their exact value in the editor.
const rulesetJSON = JSON as JSON & {
  parse(text: string, reviver: (key: string, value: unknown, context?: { source: string }) => unknown): unknown
  rawJSON?(text: string): unknown
}

async function readRuleset(res: Response): Promise<Ruleset> {
  return rulesetJSON.parse(await res.text(), (key: string, value: unknown, context?: { source: string }) => {
    if ((key !== 'limit' && key !== 'min_amount_minor') || typeof value !== 'number') return value
    if (Number.isSafeInteger(value)) return BigInt(value)
    if (context) return BigInt(context.source)
    throw new ApiError('response', 'This browser cannot read the rule limit exactly.', res.status)
  }) as Ruleset
}

export function getActiveRuleset(signal?: AbortSignal): Promise<Ruleset> {
  return request('/rulesets/active', { read: readRuleset, signal })
}

export function publishRuleset(config: RulesetConfig): Promise<Saved<Ruleset>> {
  return save(() => request('/rulesets', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config, (_key, value: unknown) => {
      if (typeof value !== 'bigint') return value
      const number = Number(value)
      if (Number.isSafeInteger(number)) return number
      if (rulesetJSON.rawJSON) return rulesetJSON.rawJSON(value.toString())
      throw new Error('This browser cannot save the rule limit exactly.')
    }),
    read: readRuleset,
  }))
}

// The proxy adds the API key to the blocklist requests.
export async function listBlocklist(signal?: AbortSignal): Promise<BlocklistEntry[]> {
  const result = await request<{ entries: BlocklistEntry[] }>('/blocklist', { signal })
  return result.entries
}

export function addBlocklistEntry(entry: { type: string; value: string; reason: string }): Promise<Saved<BlocklistEntry>> {
  return save(() => request<BlocklistEntry>('/blocklist', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(entry),
  }))
}

export function removeBlocklistEntry(id: number): Promise<Saved<null>> {
  return save(() => request(`/blocklist/${id}`, { method: 'DELETE', read: async () => null }))
}

export type ScoreResult =
  | { ok: true; response: ScoreResponse }
  | { ok: false; status: number; error: ErrorResponse }

// score sends one score request. The proxy adds the API key.
export async function score(purchase: Record<string, unknown>): Promise<ScoreResult> {
  try {
    const response = await request<ScoreResponse>('/score', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(purchase),
    })
    return { ok: true, response }
  } catch (error) {
    return { ok: false, status: error instanceof ApiError ? error.status ?? 0 : 0, error: toErrorResponse(error) }
  }
}

export function checkReadiness(signal?: AbortSignal): Promise<void> {
  return request('/ready', { auth: 'none', signal, read: async () => {} })
}

export interface Review {
  purchase_id: string
  transaction_id: string
  player_id: string
  device_id: string | null
  amount_minor: string
  event_time: string
  queued_at: string
  rule_score: number
  model_score: number | null
  model_version: string | null
  fired_rules: FiredRule[]
}

export interface ReviewCase extends Review {
  ruleset_version: number
}

export interface QueuePage {
  items: Review[]
  next_cursor: string | null
}

export type Decision = 'approve' | 'reject'

export function listReviews(analystKey: string, cursor: string | null, signal: AbortSignal): Promise<QueuePage> {
  const path = cursor === null ? '/reviews' : `/reviews?${new URLSearchParams({ cursor })}`
  return request(path, { auth: 'analyst', headers: { 'X-API-Key': analystKey }, signal })
}

export function getReviewCase(analystKey: string, purchaseId: string, signal: AbortSignal): Promise<ReviewCase> {
  return request(`/reviews/${encodeURIComponent(purchaseId)}`, {
    auth: 'analyst', headers: { 'X-API-Key': analystKey }, signal,
  })
}

export async function submitReviewDecision(analystKey: string, purchaseId: string,
  decision: Decision, blockDevice: boolean, signal: AbortSignal): Promise<boolean> {
  try {
    const result = await request<{ device_blocked?: boolean } | null>(`/reviews/${encodeURIComponent(purchaseId)}/decision`, {
      auth: 'analyst',
      method: 'POST',
      headers: { 'X-API-Key': analystKey, 'Content-Type': 'application/json' },
      body: JSON.stringify(decision === 'reject' && blockDevice ? { decision, block_device: true } : { decision }),
      expectedStatus: 201,
      signal,
    })
    return result?.device_blocked === true
  } catch (error) {
    // A 201 confirms the save even if the optional feedback could not be read.
    if (error instanceof ApiError && error.status === 201) return false
    throw error
  }
}
