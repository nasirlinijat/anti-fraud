import { ChevronLeft } from '@xsolla/xui-icons-base'
import { useCallback, useState, type ReactNode } from 'react'
import { getResolvedReviewCase, getReviewCase, type ResolvedReviewCase, type ReviewCase } from '../api'
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
import { ReviewResolution } from './ReviewResolution'
import { ClaimBadge, ReviewActions, type ReviewHandlers } from './ReviewActions'
import type { LinkedReviewActions } from './useReviewWork'

type CaseState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'open'; review: ReviewCase }
  | { status: 'resolved'; review: ResolvedReviewCase }

const REFRESH_MS = 3_000

function caseError(error: unknown): CaseState {
  return { status: 'failed', message: error instanceof Error ? error.message : 'Could not load this review case.' }
}

interface CaseViewProps {
  purchaseId: string
  time: TimeDisplay
  handlers: ReviewHandlers
  linked: LinkedReviewActions
  onBack: () => void
  // Opens another purchase awaiting review in place of this one.
  onOpen: (purchaseId: string) => void
}

// An open case can become resolved between any two reads.
async function loadCase(purchaseId: string, signal: AbortSignal): Promise<CaseState> {
  try {
    return { status: 'open', review: await getReviewCase(purchaseId, signal) }
  } catch (error) {
    if (!(error instanceof ApiError)) throw error
    if (error.status === 400) return { status: 'not-found' }
    if (error.status !== 404) throw error
  }
  try {
    return { status: 'resolved', review: await getResolvedReviewCase(purchaseId, signal) }
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return { status: 'not-found' }
    return caseError(error)
  }
}

// The case is read again every few seconds, so a claim taken or released by
// someone else changes the available actions without a reload.
export function CaseView({ purchaseId, time, handlers, linked, onBack, onOpen }: CaseViewProps) {
  const [state, setState] = useState<CaseState>({ status: 'loading' })
  const load = useCallback((signal: AbortSignal) => loadCase(purchaseId, signal), [purchaseId])
  const { retry, pending } = usePolling(state.status === 'resolved' || state.status === 'not-found' ? null : load, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: setState,
    onError: error => setState(current => current.status === 'open' ? current : caseError(error)),
  })

  return (
    <article className="review-case" aria-labelledby={state.status === 'open' || state.status === 'resolved' ? 'case-heading' : undefined}>
      <div className="toolbar">
        <Button variant="ghost" onClick={onBack}><ChevronLeft size={18} aria-hidden />Back to the queue</Button>
        <TimeDisplayToggle display={time} />
      </div>
      {state.status === 'loading' && <LoadingSkeleton shape="case" label="Loading review case…" />}
      {state.status === 'not-found' && (
        <EmptyState announce>This review case is unavailable. Go back to the queue to pick another.</EmptyState>
      )}
      {state.status === 'failed' && <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>}
      {state.status === 'open' && (
        <CaseDetails review={state.review} time={time} handlers={handlers} linked={linked} onOpen={onOpen} />
      )}
      {state.status === 'resolved' && (
        <>
          <CaseSummary review={state.review} />
          <ReviewResolution resolution={state.review.resolution} time={time} />
          <CaseEvidence review={state.review} time={time} />
        </>
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
      <CaseSummary review={review}>
        <div className="case-decision">
          <ClaimBadge item={review} username={handlers.username} />
          <ReviewActions item={review} handlers={handlers} />
        </div>
      </CaseSummary>
      <CaseEvidence review={review} time={time}>
        <CaseNetwork purchaseId={review.purchase_id} time={time} onOpen={onOpen} />
        <LinkedReviews purchaseId={review.purchase_id} handlers={handlers} actions={linked} onOpen={onOpen} />
      </CaseEvidence>
    </>
  )
}

function CaseSummary({ review, children }: { review: ReviewCase; children?: ReactNode }) {
  return (
    <header className="panel case-summary">
      <div>
        <p className="eyebrow">Review case</p>
        <h2 id="case-heading">{review.transaction_id}</h2>
        <p className="case-amount">{formatUsdMinor(review.amount_minor)}</p>
      </div>
      {children}
    </header>
  )
}
