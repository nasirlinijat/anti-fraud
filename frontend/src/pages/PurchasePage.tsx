import { useCallback, useEffect, useState } from 'react'
import { getPurchase, type PurchaseDetails } from '../api'
import { ApiError } from '../request'
import { OutcomeBadge, TimeDisplayToggle, Timestamp } from '../Display'
import { purchaseInAddress } from '../purchaseAddress'
import { caseAddress } from '../reviews/caseAddress'
import { CaseEvidence } from '../reviews/CaseEvidence'
import { PageHeader } from '../ui/PageHeader'
import { usePolling } from '../usePolling'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

type PurchaseState =
  | { status: 'loading' }
  | { status: 'loaded'; purchase: PurchaseDetails }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }

function usePurchaseAddress(): string | null {
  const [purchaseId, setPurchaseId] = useState(() => purchaseInAddress(window.location.hash))
  useEffect(() => {
    const follow = () => setPurchaseId(purchaseInAddress(window.location.hash))
    window.addEventListener('hashchange', follow)
    return () => window.removeEventListener('hashchange', follow)
  }, [])
  return purchaseId
}

function PurchasePage() {
  const purchaseId = usePurchaseAddress()
  const time = useTimeDisplay()
  if (purchaseId === null) {
    return (
      <PageHeader eyebrow="Operations" title="Purchase"
        lead="Search for a transaction ID, player ID or card token in the search box at the top." />
    )
  }
  return <Purchase key={purchaseId} purchaseId={purchaseId} time={time} />
}

function Purchase({ purchaseId, time }: { purchaseId: string; time: TimeDisplay }) {
  const [state, setState] = useState<PurchaseState>({ status: 'loading' })
  const load = useCallback((signal: AbortSignal) => getPurchase(purchaseId, signal), [purchaseId])
  const { retry, pending } = usePolling(load, {
    onSuccess: purchase => setState({ status: 'loaded', purchase }),
    onError: error => {
      if (error instanceof ApiError && (error.status === 404 || error.status === 400)) setState({ status: 'not-found' })
      else setState({ status: 'failed', message: error instanceof Error ? error.message : 'Could not load this purchase.' })
    },
  })

  return (
    <section aria-labelledby="purchase-heading">
      <PageHeader eyebrow="Purchase" headingId="purchase-heading"
        title={state.status === 'loaded' ? state.purchase.transaction_id : 'Purchase'}>
        <TimeDisplayToggle display={time} />
      </PageHeader>
      {state.status === 'loading' && <LoadingSkeleton shape="case" label="Loading purchase…" />}
      {state.status === 'not-found' && <EmptyState announce>No purchase has this ID. Search again from the box at the top.</EmptyState>}
      {state.status === 'failed' && <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>}
      {state.status === 'loaded' && (
        <>
          <section className="panel" aria-labelledby="purchase-decision">
            <h3 id="purchase-decision">Decision and review</h3>
            <dl className="facts">
              <dt>Outcome</dt>
              <dd><OutcomeBadge outcome={state.purchase.outcome} /></dd>
              <dt>Review</dt>
              <dd><ReviewResult purchase={state.purchase} time={time} /></dd>
            </dl>
          </section>
          <CaseEvidence review={state.purchase} time={time} />
        </>
      )}
    </section>
  )
}

function ReviewResult({ purchase, time }: { purchase: PurchaseDetails; time: TimeDisplay }) {
  if (purchase.resolution) {
    return (
      <>
        {purchase.resolution.decision === 'approve' ? 'Approved' : 'Rejected'} by {purchase.resolution.analyst_id},{' '}
        <Timestamp value={purchase.resolution.resolved_at} display={time} />
      </>
    )
  }
  if (purchase.outcome === 'review') return <a href={caseAddress(purchase.purchase_id)}>Waiting for an analyst: open the review case</a>
  return <>Not reviewed</>
}

export default PurchasePage
