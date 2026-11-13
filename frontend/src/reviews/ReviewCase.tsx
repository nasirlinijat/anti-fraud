import { ChevronLeft } from '@xsolla/xui-icons-base'
import { useCallback, useState } from 'react'
import { getReviewCase, type ReviewCase } from '../api'
import { TimeDisplayToggle } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { ApiError } from '../request'
import { Button } from '../ui/Button'
import { usePolling } from '../usePolling'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { CaseEvidence } from './CaseEvidence'
import { CaseNetwork } from './CaseNetwork'
import { LinkedReviews } from './LinkedReviews'
import { ClaimBadge, ReviewActions, type ReviewHandlers } from './ReviewActions'
import type { LinkedReviewActions } from './useReviewWork'

type CaseState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; review: ReviewCase }

const REFRESH_MS = 3_000

interface CaseViewProps {
  purchaseId: string
  time: TimeDisplay
  handlers: ReviewHandlers
  linked: LinkedReviewActions
  onBack: () => void
  // Opens another purchase awaiting review in place of this one.
  onOpen: (purchaseId: string) => void
}

// The case is read again every few seconds, so a claim taken or released by
// someone else changes the available actions without a reload.
export function CaseView({ purchaseId, time, handlers, linked, onBack, onOpen }: CaseViewProps) {
  const [state, setState] = useState<CaseState>({ status: 'loading' })
  const load = useCallback((signal: AbortSignal) => getReviewCase(purchaseId, signal), [purchaseId])
  const { retry, pending } = usePolling(load, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: (review) => setState({ status: 'loaded', review }),
    onError: (error) => {
      if (error instanceof ApiError && error.status === 404) setState({ status: 'not-found' })
      else if (state.status !== 'loaded') {
        setState({ status: 'failed', message: error instanceof Error ? error.message : 'Could not load this review case.' })
      }
    },
  })

  return (
    <article className="review-case" aria-labelledby="case-heading">
      <div className="toolbar">
        <Button variant="ghost" onClick={onBack}><ChevronLeft size={18} aria-hidden />Back to the queue</Button>
        <TimeDisplayToggle display={time} />
      </div>
      {state.status === 'loading' && <LoadingSkeleton shape="case" label="Loading review case…" />}
      {state.status === 'not-found' && (
        <EmptyState announce>This purchase is no longer available for review. Go back to the queue to pick another.</EmptyState>
      )}
      {state.status === 'failed' && <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>}
      {state.status === 'loaded' && (
        <CaseDetails review={state.review} time={time} handlers={handlers} linked={linked} onOpen={onOpen} />
      )}
    </article>
  )
}

interface CaseDetailsProps {
  review: ReviewCase
  time: TimeDisplay
  handlers: ReviewHandlers
  linked: LinkedReviewActions
  onOpen: (purchaseId: string) => void
}

function CaseDetails({ review, time, handlers, linked, onOpen }: CaseDetailsProps) {
  return (
    <>
      <header className="panel case-summary">
        <div>
          <p className="eyebrow">Review case</p>
          <h2 id="case-heading">{review.transaction_id}</h2>
          <p className="case-amount">{formatUsdMinor(review.amount_minor)}</p>
        </div>
        <div className="case-decision">
          <ClaimBadge item={review} username={handlers.username} />
          <ReviewActions item={review} handlers={handlers} />
        </div>
      </header>
      <CaseEvidence review={review} time={time} />
      <CaseNetwork purchaseId={review.purchase_id} time={time} onOpen={onOpen} />
      <LinkedReviews purchaseId={review.purchase_id} handlers={handlers} actions={linked} onOpen={onOpen} />
    </>
  )
}
