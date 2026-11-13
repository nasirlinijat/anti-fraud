import type { ClaimOutcome, Decision, DecisionOutcome, Review, ReviewBlocks } from '../api.ts'
import { joinNames, type Feedback } from './decisionFeedback.ts'

type Reason = { singular: string; plural: string }

const reasons: Record<'already_decided' | 'not_found' | 'not_review' | 'error' | 'not_claimed', Reason> = {
  already_decided: { singular: 'was already decided', plural: 'were already decided' },
  not_found: { singular: 'was not found', plural: 'were not found' },
  not_review: { singular: 'is no longer under review', plural: 'are no longer under review' },
  error: { singular: 'could not be saved', plural: 'could not be saved' },
  not_claimed: { singular: 'is not claimed by you', plural: 'are not claimed by you' },
}

function heldBy(name: string | null): Reason {
  const holder = name ?? 'another account'
  return { singular: `is claimed by ${holder}`, plural: `are claimed by ${holder}` }
}

// Summarizes a batch claim, such as "Claimed 3 of 4 purchases. tx-9 is claimed by bob."
export function claimBatchFeedback(items: Review[], results: ClaimOutcome[]): Feedback {
  const claimed = results.filter(result => result.status === 'claimed' || result.status === 'already_yours').length
  const missed = results.flatMap(result => {
    if (result.status === 'claimed' || result.status === 'already_yours') return []
    const reason = result.status === 'held_by_other' ? heldBy(result.claimed_by) : reasons[result.status]
    return [{ purchaseId: result.purchase_id, reason }]
  })
  const head = headline({ done: claimed, total: results.length, verb: 'Claimed', infinitive: 'claim' })
  return summary(`${head}.`, claimed, explainMisses(items, missed))
}

// Summarizes a batch decision, such as "Rejected 3 purchases and blocked 2 devices and 3 cards."
export function decisionBatchFeedback(decision: Decision, items: Review[], results: DecisionOutcome[]): Feedback {
  const saved = results.filter(result => result.status === 'saved')
  const missed = results.flatMap(result => result.status === 'saved'
    ? []
    : [{ purchaseId: result.purchase_id, reason: reasons[result.status] }])
  const verb = decision === 'approve' ? 'Approved' : 'Rejected'
  const head = headline({ done: saved.length, total: results.length, verb, infinitive: decision })
  const blocked = blockedSummary(saved.map(result => result.blocked))
  return summary(blocked ? `${head} and blocked ${blocked}.` : `${head}.`, saved.length, explainMisses(items, missed))
}

function summary(head: string, done: number, explanation: string): Feedback {
  const text = explanation ? `${head} ${explanation}` : head
  return done === 0 ? { text, failed: true } : { text }
}

function headline({ done, total, verb, infinitive }: { done: number; total: number; verb: string; infinitive: string }): string {
  if (done === 0) return `Could not ${infinitive} ${purchaseCount(total)}`
  if (done === total) return `${verb} ${purchaseCount(total)}`
  return `${verb} ${done} of ${purchaseCount(total)}`
}

export function purchaseCount(count: number): string {
  return count === 1 ? '1 purchase' : `${count} purchases`
}

const blockKinds = ['device', 'card', 'player'] as const

function blockedSummary(blocked: ReviewBlocks[]): string {
  const counts = blockKinds.flatMap(kind => {
    const count = blocked.filter(values => values[kind]).length
    if (count === 0) return []
    return [count === 1 ? `1 ${kind}` : `${count} ${kind}s`]
  })
  return counts.length === 0 ? '' : joinNames(counts)
}

type Miss = { purchaseId: string; reason: Reason }

// Purchases that share a reason are named together, in the order they were sent.
function explainMisses(items: Review[], missed: Miss[]): string {
  const transactions = new Map(items.map(item => [item.purchase_id, item.transaction_id]))
  const groups = new Map<string, { reason: Reason; names: string[] }>()
  for (const { purchaseId, reason } of missed) {
    const group = groups.get(reason.singular) ?? { reason, names: [] }
    group.names.push(transactions.get(purchaseId) ?? purchaseId)
    groups.set(reason.singular, group)
  }
  return [...groups.values()]
    .map(({ reason, names }) => `${joinNames(names)} ${names.length === 1 ? reason.singular : reason.plural}.`)
    .join(' ')
}
