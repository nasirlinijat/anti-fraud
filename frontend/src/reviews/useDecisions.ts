import { useCallback, useEffect, useRef, useState } from 'react'
import { claimReview, noReviewBlocks, submitReviewDecision, type Decision, type Review, type ReviewBlocks } from '../api'
import { ApiError } from '../request'
import { decisionFeedback } from './decisionFeedback'

export type ReviewAction = { item: Review; decision: Decision }
export type Feedback = { text: string; failed?: boolean }

interface DecisionOptions {
  refresh: () => void
  onFeedback: (feedback: Feedback | null) => void
  onDecided: (purchaseId: string) => void
}

// Takes a decision from the reviewer's confirmation to its saved result, and
// reports that result as feedback.
export function useDecisions({ refresh, onFeedback, onDecided }: DecisionOptions) {
  const [confirmation, setConfirmation] = useState<ReviewAction | null>(null)
  const [blocks, setBlocks] = useState<ReviewBlocks>(noReviewBlocks)
  const requests = useReviewRequests()

  function choose(item: Review, decision: Decision) {
    if (requests.has(item.purchase_id)) return
    setBlocks(noReviewBlocks)
    setConfirmation({ item, decision })
  }

  async function save({ item, decision }: ReviewAction) {
    const id = item.purchase_id
    if (requests.has(id)) return

    const controller = requests.begin(id)
    onFeedback(null)
    const isCurrent = () => requests.isCurrent(id, controller)
    let unconfirmed = false
    try {
      const blocked = await submitReviewDecision(id, decision, blocks, controller.signal)
      if (!isCurrent()) return
      onDecided(id)
      onFeedback({ text: decisionFeedback(item.transaction_id, decision, blocked) })
    } catch (error) {
      if (!isCurrent()) return
      const failure = describeDecisionFailure(error, item.transaction_id)
      unconfirmed = failure.unconfirmed === true
      onFeedback(failure.feedback)
    } finally {
      if (isCurrent()) {
        if (unconfirmed) requests.holdForReconciliation(id)
        else requests.finish(id)
        refresh()
      }
    }
  }

  const dismiss = () => setConfirmation(null)
  return { requests, confirmation, blocks, setBlocks, choose, save, dismiss }
}

// Claims a purchase for the signed-in account. The queue read that follows
// shows the claim, or who holds it when another account was first.
export function useClaims({ refresh, onFeedback }: Pick<DecisionOptions, 'refresh' | 'onFeedback'>) {
  const requests = useReviewRequests()

  async function claim(item: Review) {
    const id = item.purchase_id
    if (requests.has(id)) return
    const controller = requests.begin(id)
    onFeedback(null)
    try {
      await claimReview(id, controller.signal)
      if (!requests.isCurrent(id, controller)) return
      onFeedback({ text: `You claimed ${item.transaction_id}. Only you can decide it now.` })
    } catch (error) {
      if (!requests.isCurrent(id, controller)) return
      const message = error instanceof Error ? error.message : 'The claim could not be saved.'
      onFeedback({ text: `Could not claim ${item.transaction_id}. ${message}`, failed: true })
    } finally {
      if (requests.isCurrent(id, controller)) {
        requests.finish(id)
        refresh()
      }
    }
  }

  return { pending: requests.pending, claim }
}

// Tracks the writes in flight, by purchase ID, so each purchase can have only
// one at a time and its buttons stay disabled until it settles.
function useReviewRequests() {
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set())
  // A null controller keeps the action disabled until a successful poll verifies queue state.
  const requests = useRef(new Map<string, AbortController | null>())

  const cancelAll = useCallback(() => {
    for (const request of requests.current.values()) request?.abort()
    requests.current.clear()
  }, [])

  useEffect(() => cancelAll, [cancelAll])

  const publish = () => setPending(new Set(requests.current.keys()))

  return {
    pending,
    has: (id: string) => requests.current.has(id),
    isCurrent: (id: string, controller: AbortController) => requests.current.get(id) === controller,
    begin(id: string) {
      const controller = new AbortController()
      requests.current.set(id, controller)
      publish()
      return controller
    },
    finish(id: string) {
      requests.current.delete(id)
      publish()
    },
    holdForReconciliation(id: string) {
      requests.current.set(id, null)
      publish()
    },
    // Only a successful read listing the purchase confirms it is still pending.
    reconcile(items: Review[]) {
      let reconciled = false
      for (const [id, inFlight] of requests.current) {
        if (inFlight !== null || !items.some(row => row.purchase_id === id)) continue
        requests.current.delete(id)
        reconciled = true
      }
      if (reconciled) publish()
    },
  }
}

type DecisionFailure = { feedback: Feedback; unconfirmed?: boolean }

// A 409 means the purchase was already decided or the claim is no longer
// held; the queue read that follows shows which. A failure that is not a clear
// client error leaves the outcome unknown until the queue is read again.
function describeDecisionFailure(error: unknown, transactionId: string): DecisionFailure {
  const status = error instanceof ApiError ? error.status : undefined
  const message = error instanceof Error ? error.message : 'Could not save the decision.'
  if (status === 404) {
    return { feedback: { text: `Could not find ${transactionId}. ${message}`, failed: true } }
  }
  if (status === 409) {
    return { feedback: { text: `${transactionId} was not decided. ${message}`, failed: true } }
  }
  if (status !== undefined && status < 500 && error instanceof ApiError && error.kind === 'http') {
    return { feedback: { text: `Could not save a decision for ${transactionId}. ${message}`, failed: true } }
  }
  return {
    unconfirmed: true,
    feedback: { text: `Could not confirm the decision for ${transactionId}. ${message} Check whether it is still pending after the queue refreshes.`, failed: true },
  }
}
