import { useState } from 'react'
import { claimReviews, noReviewBlocks, submitReviewDecisions, type Decision, type Review, type ReviewBlocks } from '../api'
import { claimBatchFeedback, decisionBatchFeedback, purchaseCount } from './batchFeedback'
import { isClientError, type Feedback, type ReviewRequests } from './useDecisions'

export type BatchAction = { items: Review[]; decision: Decision }

interface BatchOptions {
  // The trackers of single decisions and claims, so a purchase never has two writes at once.
  decisions: ReviewRequests
  claims: ReviewRequests
  refresh: () => void
  onFeedback: (feedback: Feedback | null) => void
  onDecided: (purchaseId: string) => void
}

// Claims or decides many purchases in one request. Each purchase gets its own
// result, and the results are reported together as one message.
export function useBatchReviews({ decisions, claims, refresh, onFeedback, onDecided }: BatchOptions) {
  const [confirmation, setConfirmation] = useState<BatchAction | null>(null)
  const [blocks, setBlocks] = useState<ReviewBlocks>(noReviewBlocks)
  const startable = (items: Review[]) =>
    items.length > 0 && !items.some(({ purchase_id: id }) => decisions.has(id) || claims.has(id))

  async function claimAll(items: Review[]) {
    if (!startable(items)) return
    const ids = items.map(item => item.purchase_id)
    const controller = claims.begin(...ids)
    const isCurrent = () => ids.every(id => claims.isCurrent(id, controller))
    onFeedback(null)
    try {
      const results = await claimReviews(ids, controller.signal)
      if (!isCurrent()) return
      onFeedback(claimBatchFeedback(items, results))
    } catch (error) {
      if (!isCurrent()) return
      const message = error instanceof Error ? error.message : 'The claims could not be saved.'
      onFeedback({ text: `Could not claim ${purchaseCount(ids.length)}. ${message}`, failed: true })
    } finally {
      if (isCurrent()) {
        claims.finish(...ids)
        refresh()
      }
    }
  }

  function choose(items: Review[], decision: Decision) {
    if (!startable(items)) return
    setBlocks(noReviewBlocks)
    setConfirmation({ items, decision })
  }

  async function save({ items, decision }: BatchAction) {
    if (!startable(items)) return
    const ids = items.map(item => item.purchase_id)
    const controller = decisions.begin(...ids)
    const isCurrent = () => ids.every(id => decisions.isCurrent(id, controller))
    onFeedback(null)
    let unconfirmed = false
    try {
      const results = await submitReviewDecisions(ids, decision, blocks, controller.signal)
      if (!isCurrent()) return
      for (const result of results) if (result.status === 'saved') onDecided(result.purchase_id)
      onFeedback(decisionBatchFeedback(decision, items, results))
    } catch (error) {
      if (!isCurrent()) return
      unconfirmed = !isClientError(error)
      onFeedback(batchDecisionFailure(error, ids.length))
    } finally {
      if (isCurrent()) {
        if (unconfirmed) decisions.holdForReconciliation(...ids)
        else decisions.finish(...ids)
        refresh()
      }
    }
  }

  const dismiss = () => setConfirmation(null)
  return { confirmation, blocks, setBlocks, claimAll, choose, save, dismiss }
}

// As with a single decision, a failure that is not a clear client error
// leaves the outcome unknown until the purchases are read again.
function batchDecisionFailure(error: unknown, count: number): Feedback {
  const message = error instanceof Error ? error.message : 'Could not save the decisions.'
  if (isClientError(error)) {
    return { text: `Could not save the decisions for ${purchaseCount(count)}. ${message}`, failed: true }
  }
  return {
    text: `Could not confirm the decisions for ${purchaseCount(count)}. ${message} Check whether they are still pending after the list refreshes.`,
    failed: true,
  }
}
