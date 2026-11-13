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

export interface ModelStatus {
  loaded: boolean
  version: string | null
  mode: 'off' | 'shadow' | 'assist'
  clear_below: number | null
  flag_at: number | null
  evaluation: { dataset: string; roc_auc: number; pr_auc: number } | null
}

export function getModelStatus(signal?: AbortSignal): Promise<ModelStatus> {
  return request('/model', { signal })
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
  blocklist_blocks: number
  top_rules: { name: string; fired: number; points: number }[]
  review_queue: number
  model: { scored: number; flagged: number; cleared: number }
  p95_scoring_latency_us: number | null
  blocked_value_minor: string
  decision_series: DecisionSeries
}

export function getStats(period: Period, signal?: AbortSignal): Promise<Stats> {
  return request(`/stats?period=${period}`, { signal })
}

export interface RecentPipeline {
  after: number | null
  next_after: number | null
  outcomes: Stats['outcomes']
  blocklist_blocks: number
  model: Pick<Stats['model'], 'flagged' | 'cleared'>
}

// Without a cursor the counts are zero and next_after is the newest purchase,
// so the console counts only the decisions made after its first read.
export function getRecentPipeline(after: number | null, signal?: AbortSignal): Promise<RecentPipeline> {
  const query = after === null ? '' : `?after=${after}`
  return request(`/stats/pipeline/recent${query}`, { signal })
}

export const entryTypes =['card', 'device', 'player', 'ip'] as const
export type EntryType = (typeof entryTypes)[number]

export interface BlocklistEntry {
  id: number
  type: EntryType
  value: string
  reason: string
  created_at: string
}

export type Saved<T> = { ok: true; value: T } | { ok: false; error: ErrorResponse }

export async function save<T>(operation: () => Promise<T>): Promise<Saved<T>> {
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
  note?: string
  rules: Record<string, RuleSettings>
}

export interface Ruleset extends RulesetConfig {
  version: number
  note: string
  created_at: string
}

export interface RulesetSummary {
  version: number
  note: string
  created_at: string
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

export function getRuleset(version: number, signal?: AbortSignal): Promise<Ruleset> {
  return request(`/rulesets/${version}`, { read: readRuleset, signal })
}

export async function listRulesets(signal?: AbortSignal): Promise<RulesetSummary[]> {
  const result = await request<{ versions: RulesetSummary[] }>('/rulesets', { signal })
  return result.versions
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

// score sends one score request. POST /score accepts only the service key,
// which the console proxy adds after checking the session.
export async function score(purchase: Record<string, unknown>): Promise<ScoreResult> {
  try {
    const response = await request<ScoreResponse>('/score', {
      method: 'POST',
      auth: 'session',
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
  claimed_by: string | null
  claimed_at: string | null
}

export interface PlayerPurchase {
  transaction_id: string
  event_time: string
  amount_minor: string
  outcome: Outcome
}

export interface DeviceActivity {
  purchases: number
  distinct_cards: number
  last_seen_at: string
}

export type CvvResult = 'match' | 'mismatch' | 'not_checked'
export type AvsResult = 'match' | 'partial' | 'mismatch' | 'not_checked'
export type ThreeDsResult = 'authenticated' | 'attempted' | 'failed' | 'not_enrolled'

export type AccountEventType = 'login' | 'password_reset'

export interface AccountEvent {
  event_id: string
  event_type: AccountEventType
  event_time: string
  device_id: string | null
  ip_country: string | null
  received_at: string
  // True when the event was recorded after the purchase was decided, so the
  // rules could not have seen it.
  recorded_after_decision: boolean
}

export interface ReviewCase extends Review {
  ruleset_version: number
  player_purchases: PlayerPurchase[]
  device_activity: DeviceActivity | null
  cvv_result: CvvResult | null
  avs_result: AvsResult | null
  three_ds_result: ThreeDsResult | null
  account_events: AccountEvent[]
  ip_intel: IpIntel | null
}

export interface IpIntel {
  provider: string
  available: boolean
  hosting: boolean
  vpn: boolean
  proxy: boolean
  tor: boolean
}

export interface QueuePage {
  items: Review[]
  next_cursor: string | null
  model_enabled: boolean
}

export type ReviewSort = 'rule_score' | 'model_score'
export type Decision = 'approve' | 'reject'

export type ClaimFilter = 'all' | 'mine'

export interface QueueQuery {
  sort: ReviewSort
  cursor: string | null
  claimed: ClaimFilter
}

export function listReviews({ sort, cursor, claimed }: QueueQuery, signal: AbortSignal): Promise<QueuePage> {
  const params = new URLSearchParams({ sort })
  if (cursor !== null) params.set('cursor', cursor)
  if (claimed === 'mine') params.set('claimed', 'mine')
  return request(`/reviews?${params}`, { signal })
}

export function getReviewCase(purchaseId: string, signal: AbortSignal): Promise<ReviewCase> {
  return request(`/reviews/${encodeURIComponent(purchaseId)}`, { signal })
}

export type NetworkStatus = 'blocklisted' | 'rejected' | 'blocked' | 'in_review' | 'clear'
export type NetworkEntityType = 'device' | 'card' | 'player'

export interface LinkedEntity {
  type: 'player' | 'card'
  value: string
  purchases: number
  status: NetworkStatus
}

export interface NetworkAnchor {
  type: NetworkEntityType
  value: string
  blocklisted: boolean
  linked_total: number
  linked: LinkedEntity[]
}

export interface CaseNetwork {
  window_days: number
  anchors: NetworkAnchor[]
}

export function getReviewNetwork(purchaseId: string, signal: AbortSignal): Promise<CaseNetwork> {
  return request(`/reviews/${encodeURIComponent(purchaseId)}/network`, { signal })
}

export interface LinkedPurchase {
  purchase_id: string
  transaction_id: string
  event_time: string
  amount_minor: string
  outcome: Outcome
  review_decision: Decision | null
  claimed_by: string | null
  awaiting_review: boolean
}

export interface LinkedPurchases {
  anchor: NetworkEntityType
  value: string
  window_days: number
  // All matching purchases; at most 50 of the newest are listed.
  total: number
  purchases: LinkedPurchase[]
}

export function getLinkedPurchases(purchaseId: string, anchor: NetworkEntityType, value: string,
  signal: AbortSignal): Promise<LinkedPurchases> {
  const params = new URLSearchParams({ anchor, value })
  return request(`/reviews/${encodeURIComponent(purchaseId)}/network/purchases?${params}`, { signal })
}

export interface LinkedReviews {
  window_days: number
  // All linked purchases awaiting review; at most 50 with the highest rule scores are listed.
  total: number
  items: Review[]
}

export function getLinkedReviews(purchaseId: string, signal: AbortSignal): Promise<LinkedReviews> {
  return request(`/reviews/${encodeURIComponent(purchaseId)}/network/reviews`, { signal })
}

export interface Claim {
  purchase_id: string
  claimed_by: string
  claimed_at: string
}

export function claimReview(purchaseId: string, signal?: AbortSignal): Promise<Claim> {
  return request(`/reviews/${encodeURIComponent(purchaseId)}/claim`, { method: 'POST', signal })
}

export type ClaimStatus = 'claimed' | 'already_yours' | 'held_by_other' | 'already_decided' | 'not_found' | 'not_review' | 'error'

export interface ClaimOutcome {
  purchase_id: string
  status: ClaimStatus
  claimed_by: string | null
  claimed_at: string | null
}

// Returns one result per purchase, in request order: the batch is not atomic.
export async function claimReviews(purchaseIds: string[], signal: AbortSignal): Promise<ClaimOutcome[]> {
  const result = await request<{ results: ClaimOutcome[] }>('/reviews/claims', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ purchase_ids: purchaseIds }),
    signal,
  })
  return result.results
}

export function releaseClaim(purchaseId: string): Promise<Saved<null>> {
  return save(() => request(`/reviews/${encodeURIComponent(purchaseId)}/claim`, { method: 'DELETE', read: async () => null }))
}

// The purchase values a rejection adds to the blocklist.
export interface ReviewBlocks {
  device: boolean
  card: boolean
  player: boolean
}

export const noReviewBlocks: ReviewBlocks = { device: false, card: false, player: false }

interface DecisionSaved {
  device_blocked?: boolean
  card_blocked?: boolean
  player_blocked?: boolean
}

// Returns the blocklist entries the rejection added, which leaves out values
// that were already blocked.
export async function submitReviewDecision(purchaseId: string, decision: Decision,
  blocks: ReviewBlocks, signal: AbortSignal): Promise<ReviewBlocks> {
  try {
    const result = await request<DecisionSaved | null>(`/reviews/${encodeURIComponent(purchaseId)}/decision`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(decisionBody(decision, blocks)),
      expectedStatus: 201,
      signal,
    })
    return blockedValues(result)
  } catch (error) {
    // A 201 confirms the save even if the optional feedback could not be read.
    if (error instanceof ApiError && error.status === 201) return noReviewBlocks
    throw error
  }
}

function blockedValues(saved: DecisionSaved | null): ReviewBlocks {
  return { device: saved?.device_blocked === true, card: saved?.card_blocked === true, player: saved?.player_blocked === true }
}

export type DecisionStatus = 'saved' | 'already_decided' | 'not_claimed' | 'not_found' | 'not_review' | 'error'

export interface DecisionOutcome {
  purchase_id: string
  status: DecisionStatus
  // The blocklist entries this purchase's rejection added.
  blocked: ReviewBlocks
}

interface DecisionResult extends DecisionSaved {
  purchase_id: string
  status: DecisionStatus
}

// Returns one result per purchase, in request order: the batch is not atomic.
// The block choices apply to each purchase's own device, card and player.
export async function submitReviewDecisions(purchaseIds: string[], decision: Decision,
  blocks: ReviewBlocks, signal: AbortSignal): Promise<DecisionOutcome[]> {
  const result = await request<{ results: DecisionResult[] }>('/reviews/decisions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...decisionBody(decision, blocks), purchase_ids: purchaseIds }),
    signal,
  })
  return result.results.map(saved => ({ purchase_id: saved.purchase_id, status: saved.status, blocked: blockedValues(saved) }))
}

// The API accepts block fields only with a rejection, so only the chosen ones are sent.
function decisionBody(decision: Decision, blocks: ReviewBlocks) {
  if (decision === 'approve') return { decision }
  return {
    decision,
    ...(blocks.device && { block_device: true }),
    ...(blocks.card && { block_card: true }),
    ...(blocks.player && { block_player: true }),
  }
}

export interface SimulationSettings {
  request_id: string
  mode: 'live' | 'backfill'
  scenarios: string[]
  seed: number
  rate: number
  count: number
}
export type SimulationStatus = 'starting' | 'running' | 'stopping' | 'completed' | 'stopped' | 'failed' | 'interrupted'
export interface SimulationRun {
  id: string
  settings: SimulationSettings
  status: SimulationStatus
  created_at: string
  finished_at: string | null
  sent: number
  failed: number
  allowed: number
  reviewed: number
  blocked: number
  pending: boolean
  error?: string
}
export interface SimulationHistory {
  items: SimulationRun[]
  next_cursor?: string
  available_scenarios: string[]
}
export function listSimulations(signal?: AbortSignal): Promise<SimulationHistory> {
  return request('/simulations?limit=10', { signal })
}
export function getSimulation(id: string, signal?: AbortSignal): Promise<SimulationRun> {
  return request(`/simulations/${encodeURIComponent(id)}`, { signal })
}
export function startSimulation(settings: SimulationSettings, signal?: AbortSignal): Promise<SimulationRun> {
  return request('/simulations', { method: 'POST', signal,
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(settings) })
}
export function stopSimulation(id: string, signal?: AbortSignal): Promise<SimulationRun> {
  return request(`/simulations/${encodeURIComponent(id)}/stop`, { method: 'POST', signal })
}

export type QualitySource = 'simulator' | 'ieee-cis'

export interface QualityReport {
  source: QualitySource
  run_id: string
  generated_at: string
  labelled_decisions: number
  true_positives: number
  false_positives: number
  true_negatives: number
  false_negatives: number
  precision: number | null
  recall: number | null
  false_positive_rate: number | null
  fraud_value_flagged_minor: string
  fraud_value_allowed_minor: string
  coverage: {
    pending: number
    quarantined: number
    scored_decisions: number | null
    run_status: SimulationStatus | null
  }
}

export interface QualityRuns {
  source: QualitySource
  run_ids: string[]
  next_cursor?: string
}

export function getQuality(source: QualitySource, runId: string, signal?: AbortSignal): Promise<QualityReport> {
  return request(`/stats/quality?${new URLSearchParams({ source, run_id: runId })}`, { signal })
}

export function listQualityRuns(source: QualitySource, after: string, signal?: AbortSignal): Promise<QualityRuns> {
  const params = new URLSearchParams({ source, limit: '20' })
  if (after) params.set('after', after)
  return request(`/stats/quality/runs?${params}`, { signal })
}
