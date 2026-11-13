import { useCallback, useEffect, useState } from 'react'
import { getLinkedReviews, type LinkedReviews as LinkedList } from '../api'
import { ApiError } from '../request'
import { usePolling } from '../usePolling'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { pruneSelection, type Selection } from './linkedSelection'
import { LinkedReviewTable } from './LinkedReviewTable'
import type { ReviewHandlers } from './ReviewActions'
import type { LinkedReviewActions } from './useReviewWork'

type ListState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; list: LinkedList }

const REFRESH_MS = 3_000

interface LinkedReviewsProps {
  purchaseId: string
  handlers: ReviewHandlers
  actions: LinkedReviewActions
  onOpen: (purchaseId: string) => void
}

// The other purchases awaiting review that share this case's device, card or
// player, so a fraud ring can be claimed and decided together.
export function LinkedReviews({ purchaseId, handlers, actions, onOpen }: LinkedReviewsProps) {
  const { state, selection, setSelection, retry, pending } = useLinkedReviews(purchaseId, handlers.username, actions)
  return (
    <section className="panel linked-reviews" aria-labelledby="case-linked-reviews">
      <h3 id="case-linked-reviews">Linked purchases awaiting review</h3>
      {state.status === 'loading' && <LoadingSkeleton shape="table" label="Loading linked purchases awaiting review…" />}
      {state.status === 'not-found' && (
        <EmptyState announce>This purchase is no longer awaiting review, so its linked purchases are not shown.</EmptyState>
      )}
      {state.status === 'failed' && <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>}
      {state.status === 'loaded' && state.list.items.length === 0 && (
        <EmptyState>
          No other purchases awaiting review share this purchase&apos;s device, card or player within {state.list.window_days} days.
        </EmptyState>
      )}
      {state.status === 'loaded' && state.list.items.length > 0 && (
        <LinkedReviewTable list={state.list} selection={selection} handlers={handlers} actions={actions}
          onSelectionChange={setSelection} onOpen={onOpen} />
      )}
    </section>
  )
}

// The list is read again every few seconds, because other reviewers claim and
// decide these purchases. A failed refresh keeps the last good list, as the case does.
function useLinkedReviews(purchaseId: string, username: string, actions: LinkedReviewActions) {
  const [state, setState] = useState<ListState>({ status: 'loading' })
  const [selection, setSelection] = useState<Selection>(() => new Set<string>())
  const load = useCallback((signal: AbortSignal) => getLinkedReviews(purchaseId, signal), [purchaseId])
  const { refresh, retry, pending } = usePolling(load, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: (list) => {
      actions.onListed(list.items)
      setState({ status: 'loaded', list })
      setSelection(current => pruneSelection(current, list.items, username))
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 404) setState({ status: 'not-found' })
      else if (state.status !== 'loaded') {
        setState({ status: 'failed', message: error instanceof Error ? error.message : 'Could not load the linked purchases awaiting review.' })
      }
    },
  })

  const { onAttach } = actions
  useEffect(() => onAttach({
    refresh,
    drop: (id) => setState(current => dropPurchase(current, id)),
  }), [onAttach, refresh])

  return { state, selection, setSelection, retry, pending }
}

// A purchase decided here leaves the list at once instead of at the next read.
function dropPurchase(state: ListState, purchaseId: string): ListState {
  if (state.status !== 'loaded' || !state.list.items.some(item => item.purchase_id === purchaseId)) return state
  const { list } = state
  return { status: 'loaded', list: { ...list, total: list.total - 1, items: list.items.filter(item => item.purchase_id !== purchaseId) } }
}
