import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { getResolvedReviewCase, type ResolvedReviewCase } from '../api'
import { Timestamp } from '../Display'
import { ApiError } from '../request'
import { Button } from '../ui/Button'
import { usePolling } from '../usePolling'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { CaseEvidence } from './CaseEvidence'

type ResolvedCaseState =
  | { status: 'loading' }
  | { status: 'unavailable' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; review: ResolvedReviewCase }

export function ResolvedCaseDialog({ purchaseId, time, onClose }: {
  purchaseId: string
  time: TimeDisplay
  onClose: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const heading = useId()
  const [state, setState] = useState<ResolvedCaseState>({ status: 'loading' })
  const load = useCallback((signal: AbortSignal) => getResolvedReviewCase(purchaseId, signal), [purchaseId])
  const { retry, pending } = usePolling(load, {
    onSuccess: review => setState({ status: 'loaded', review }),
    onError: error => {
      if (error instanceof ApiError && error.status === 404) {
        setState({ status: 'unavailable' })
        return
      }
      setState({ status: 'failed', message: error instanceof Error ? error.message : 'Could not load this resolved case.' })
    },
  })

  useEffect(() => {
    if (!dialog.current?.open) dialog.current?.showModal()
  }, [])

  return (
    <dialog className="dialog resolved-case-dialog" ref={dialog} aria-labelledby={heading} onClose={onClose}>
      <div className="panel-head">
        <h2 id={heading}>Resolved case{state.status === 'loaded' && ` · ${state.review.transaction_id}`}</h2>
        <form method="dialog">
          <Button type="submit" variant="secondary" autoFocus>Back to connection</Button>
        </form>
      </div>
      {state.status === 'loading' && <LoadingSkeleton shape="case" label="Loading resolved case…" />}
      {state.status === 'unavailable' && <EmptyState announce>This resolved case is unavailable.</EmptyState>}
      {state.status === 'failed' && <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>}
      {state.status === 'loaded' && <ResolvedEvidence review={state.review} time={time} />}
    </dialog>
  )
}

function ResolvedEvidence({ review, time }: { review: ResolvedReviewCase; time: TimeDisplay }) {
  return (
    <>
      <section className="panel">
        <h3>Analyst decision</h3>
        <dl className="facts">
          <dt>Decision</dt><dd>{review.resolution.decision === 'approve' ? 'Approved' : 'Rejected'}</dd>
          <dt>Analyst</dt><dd>{review.resolution.analyst_id}</dd>
          <dt>Resolved time</dt><dd><Timestamp value={review.resolution.resolved_at} display={time} /></dd>
        </dl>
      </section>
      <CaseEvidence review={review} time={time} />
    </>
  )
}
