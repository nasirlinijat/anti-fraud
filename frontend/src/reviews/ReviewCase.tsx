import { ChevronLeft } from '@xsolla/xui-icons-base'
import { useCallback, useState } from 'react'
import { getReviewCase, type ReviewCase } from '../api'
import { ModelRiskBadge, OutcomeBadge, RuleScoreBadge, TimeDisplayToggle, Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { ApiError } from '../request'
import { Button } from '../ui/Button'
import { usePolling } from '../usePolling'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { CaseNetwork } from './CaseNetwork'
import { ClaimBadge, ReviewActions, type ReviewHandlers } from './ReviewActions'

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
  onBack: () => void
}

// The case is read again every few seconds, so a claim taken or released by
// someone else changes the available actions without a reload.
export function CaseView({ purchaseId, time, handlers, onBack }: CaseViewProps) {
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
      {state.status === 'loaded' && <CaseDetails review={state.review} time={time} handlers={handlers} />}
    </article>
  )
}

function CaseDetails({ review, time, handlers }: { review: ReviewCase; time: TimeDisplay; handlers: ReviewHandlers }) {
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
      <div className="case-grid">
        <section className="panel" aria-labelledby="case-purchase">
          <h3 id="case-purchase">Purchase</h3>
          <PurchaseFacts review={review} time={time} />
        </section>
        <section className="panel" aria-labelledby="case-scoring">
          <h3 id="case-scoring">Scoring</h3>
          <ScoringFacts review={review} />
        </section>
      </div>
      <section className="panel" aria-labelledby="case-rules">
        <h3 id="case-rules">Fired rules</h3>
        <FiredRules review={review} />
      </section>
      <section className="panel" aria-labelledby="case-history">
        <h3 id="case-history">Recent player purchases</h3>
        <PlayerPurchases review={review} time={time} />
      </section>
      <section className="panel" aria-labelledby="case-device">
        <h3 id="case-device">Recent device activity</h3>
        <DeviceActivity review={review} time={time} />
      </section>
      <CaseNetwork purchaseId={review.purchase_id} />
    </>
  )
}

function PurchaseFacts({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  return (
    <dl className="facts">
      <dt>Transaction</dt>
      <dd>{review.transaction_id}</dd>
      <dt>Player</dt>
      <dd>{review.player_id}</dd>
      <dt>Device</dt>
      <dd>{review.device_id ?? 'None'}</dd>
      <dt>Amount (USD)</dt>
      <dd>{formatUsdMinor(review.amount_minor)}</dd>
      <dt>Purchase time</dt>
      <dd><Timestamp value={review.event_time} display={time} /></dd>
      <dt>Queued time</dt>
      <dd><Timestamp value={review.queued_at} display={time} /></dd>
    </dl>
  )
}

function ScoringFacts({ review }: { review: ReviewCase }) {
  return (
    <dl className="facts">
      <dt>Rule score</dt>
      <dd><RuleScoreBadge value={review.rule_score} /></dd>
      <dt>Ruleset version</dt>
      <dd>{review.ruleset_version}</dd>
      <dt>Model risk</dt>
      <dd><ModelRiskBadge value={review.model_score} /></dd>
      <dt>Model version</dt>
      <dd>{review.model_version ?? 'None'}</dd>
    </dl>
  )
}

function FiredRules({ review }: { review: ReviewCase }) {
  if (review.fired_rules.length === 0) return <EmptyState>No rules fired.</EmptyState>
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Fired rule evidence">
      <table>
        <thead>
          <tr>
            <th scope="col">Rule</th>
            <th scope="col">Why it fired</th>
            <th scope="col" className="num">Points</th>
          </tr>
        </thead>
        <tbody>
          {review.fired_rules.map(rule => (
            <tr key={rule.name}>
              <th scope="row"><code>{rule.name}</code></th>
              <td>{rule.message}</td>
              <td className="num">{rule.points}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function PlayerPurchases({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  if (review.player_purchases.length === 0) return <EmptyState>No earlier purchases for this player.</EmptyState>
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Recent player purchases">
      <table>
        <thead>
          <tr>
            <th scope="col">Transaction</th>
            <th scope="col">Purchase time</th>
            <th scope="col" className="num">Amount (USD)</th>
            <th scope="col">Outcome</th>
          </tr>
        </thead>
        <tbody>
          {review.player_purchases.map(purchase => (
            <tr key={purchase.transaction_id}>
              <th scope="row">{purchase.transaction_id}</th>
              <td><Timestamp value={purchase.event_time} display={time} /></td>
              <td className="num">{formatUsdMinor(purchase.amount_minor)}</td>
              <td><OutcomeBadge outcome={purchase.outcome} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DeviceActivity({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  const activity = review.device_activity
  if (activity === null) return <EmptyState>This purchase did not include a device ID.</EmptyState>
  return (
    <dl className="facts">
      <dt>Purchases on this device</dt>
      <dd>{activity.purchases}</dd>
      <dt>Distinct cards</dt>
      <dd>{activity.distinct_cards}</dd>
      <dt>Last seen</dt>
      <dd><Timestamp value={activity.last_seen_at} display={time} /></dd>
    </dl>
  )
}
