import { useCallback, useRef } from 'react'
import type { Decision, Review } from '../api'
import type { ReviewHandlers } from './ReviewActions'
import { useBatchReviews } from './useBatchReviews'
import { useClaims, useDecisions, type Feedback } from './useDecisions'

// What the queue can ask of the linked reviews list on an open case.
export interface LinkedList {
  refresh: () => void
  drop: (purchaseId: string) => void
}

export interface LinkedReviewActions {
  onClaimAll: (items: Review[]) => void
  onDecideAll: (items: Review[], decision: Decision) => void
  // Called with every successful read of the list, so a purchase held after an
  // unconfirmed decision is released once the list shows it still pending.
  onListed: (items: Review[]) => void
  // Connects the mounted list, so a settled write refreshes it at once. Returns the disconnect.
  onAttach: (list: LinkedList) => () => void
}

interface WorkOptions {
  username: string
  refreshQueue: () => void
  onFeedback: (feedback: Feedback | null) => void
  onDecided: (purchaseId: string) => void
}

// Ties single and batch writes together: they share one busy set, and every
// settled write refreshes the queue and the linked reviews list of an open case.
export function useReviewWork({ username, refreshQueue, onFeedback, onDecided }: WorkOptions) {
  const linkedList = useRef<LinkedList | null>(null)
  const refresh = () => {
    refreshQueue()
    linkedList.current?.refresh()
  }
  const decided = (id: string) => {
    linkedList.current?.drop(id)
    onDecided(id)
  }
  const decisions = useDecisions({ refresh, onFeedback, onDecided: decided })
  const claims = useClaims({ refresh, onFeedback })
  const batch = useBatchReviews({ decisions: decisions.requests, claims: claims.requests, refresh, onFeedback, onDecided: decided })
  const onAttach = useCallback((list: LinkedList) => {
    linkedList.current = list
    return () => { if (linkedList.current === list) linkedList.current = null }
  }, [])

  const handlers: ReviewHandlers = {
    username,
    busy: new Set([...decisions.requests.pending, ...claims.requests.pending]),
    onClaim: item => void claims.claim(item),
    onDecision: decisions.choose,
  }
  const linked: LinkedReviewActions = {
    onClaimAll: items => void batch.claimAll(items),
    onDecideAll: batch.choose,
    onListed: decisions.requests.reconcile,
    onAttach,
  }
  return { handlers, linked, decisions, batch, reconcile: decisions.requests.reconcile }
}
