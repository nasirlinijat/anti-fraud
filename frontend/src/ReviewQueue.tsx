import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { getReviewCase, listReviews, submitReviewDecision, type Decision, type QueuePage, type Review, type ReviewCase } from './api'
import { ModelRiskBadge, OutcomeBadge, RuleScoreBadge, TimeDisplayToggle, Timestamp } from './Display'
import { formatUsdMinor } from './displayFormat'
import { ApiError } from './request'
import { usePolling } from './usePolling'
import { useTimeDisplay, type TimeDisplay } from './useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from './ViewState'

type ReviewAction = { item: Review; decision: Decision }
const REFRESH_MS = 5_000

function ReviewTable({ items, pending, time, onOpen, onDecision }: {
  items: Review[]
  pending: ReadonlySet<string>
  time: TimeDisplay
  onOpen: (purchaseId: string) => void
  onDecision: (item: Review, decision: Decision) => void
}) {
  function scrollTable(event: KeyboardEvent<HTMLDivElement>) {
    const region = event.currentTarget
    if (event.target !== region || event.altKey || event.metaKey || event.ctrlKey || event.shiftKey) return
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    if (region.scrollWidth <= region.clientWidth) return

    event.preventDefault()
    region.scrollBy({ left: event.key === 'ArrowLeft' ? -80 : 80 })
  }

  return (
    <div
      className="review-table-scroll"
      role="region"
      aria-labelledby="review-caption"
      tabIndex={0}
      onKeyDown={scrollTable}
    >
      <table className="review-table">
        <caption id="review-caption">Purchases awaiting review, oldest queued first</caption>
        <thead>
          <tr>
            <th scope="col">Transaction</th>
            <th scope="col">Player</th>
            <th scope="col">Amount (USD)</th>
            <th scope="col">Purchase time</th>
            <th scope="col">Queued time</th>
            <th scope="col">Rule score</th>
            <th scope="col">Model risk</th>
            <th scope="col">Model version</th>
            <th scope="col">Fired rules</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr key={item.purchase_id} data-purchase-id={item.purchase_id}>
              <th scope="row">
                <button type="button" className="review-open" onClick={() => onOpen(item.purchase_id)}>
                  {item.transaction_id}
                </button>
              </th>
              <td>{item.player_id}</td>
              <td>{formatUsdMinor(item.amount_minor)}</td>
              <td><Timestamp value={item.event_time} display={time} /></td>
              <td><Timestamp value={item.queued_at} display={time} /></td>
              <td><RuleScoreBadge value={item.rule_score} /></td>
              <td><ModelRiskBadge value={item.model_score} /></td>
              <td>{item.model_version ?? 'None'}</td>
              <td>
                {item.fired_rules.length === 0 ? 'None' : (
                  <ul>
                    {item.fired_rules.map((rule) => (
                      <li key={rule.name}>{rule.message} ({rule.points} points)</li>
                    ))}
                  </ul>
                )}
              </td>
              <td>
                <div className="review-actions">
                  <button type="button" aria-disabled={pending.has(item.purchase_id)}
                    aria-label={`Approve ${item.transaction_id}`}
                    onClick={() => onDecision(item, 'approve')}>Approve</button>
                  <button type="button" className="secondary" aria-disabled={pending.has(item.purchase_id)}
                    aria-label={`Reject ${item.transaction_id}`}
                    onClick={() => onDecision(item, 'reject')}>Reject</button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

type CaseState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; review: ReviewCase }

function CaseView({ apiKey, purchaseId, time, onBack, onKeyRejected }: {
  apiKey: string
  purchaseId: string
  time: TimeDisplay
  onBack: () => void
  onKeyRejected: (message: string) => void
}) {
  const [state, setState] = useState<CaseState>({ status: 'loading' })
  const load = useCallback((signal: AbortSignal) => getReviewCase(apiKey, purchaseId, signal), [apiKey, purchaseId])
  const { retry, pending } = usePolling(load, {
    onSuccess: (review) => setState({ status: 'loaded', review }),
    onError: (error) => {
      const status = error instanceof ApiError ? error.status : undefined
      const message = error instanceof Error ? error.message : 'Could not load this review case.'
      if (status === 401) onKeyRejected(message)
      else if (status === 404) setState({ status: 'not-found' })
      else setState({ status: 'failed', message })
    },
  })

  return (
    <article className="review-case" aria-labelledby="case-heading">
      <button type="button" className="review-back" onClick={onBack}>Back to the queue</button>
      <h3 id="case-heading">Review case</h3>
      {(state.status === 'loading' || pending) && <LoadingSkeleton shape="case" label="Loading review case…" />}
      {state.status === 'not-found' && (
        <EmptyState announce>This purchase is no longer available for review. Go back to the queue to pick another.</EmptyState>
      )}
      {state.status === 'failed' && (
        <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>
      )}
      {state.status === 'loaded' && <CaseDetails review={state.review} time={time} />}
    </article>
  )
}

function CaseDetails({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  return (
    <>
      <dl className="review-facts">
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
        <dt>Ruleset version</dt>
        <dd>{review.ruleset_version}</dd>
        <dt>Rule score</dt>
        <dd><RuleScoreBadge value={review.rule_score} /></dd>
        <dt>Model risk</dt>
        <dd><ModelRiskBadge value={review.model_score} /></dd>
        <dt>Model version</dt>
        <dd>{review.model_version ?? 'None'}</dd>
      </dl>
      <h4>Fired rules</h4>
      {review.fired_rules.length === 0 ? <EmptyState>No rules fired.</EmptyState> : (
        <table className="review-table review-rules">
          <thead>
            <tr>
              <th scope="col">Rule</th>
              <th scope="col">Why it fired</th>
              <th scope="col">Points</th>
            </tr>
          </thead>
          <tbody>
            {review.fired_rules.map((rule) => (
              <tr key={rule.name}>
                <th scope="row">{rule.name}</th>
                <td>{rule.message}</td>
                <td>{rule.points}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <h4>Recent player purchases</h4>
      {review.player_purchases.length === 0 ? (
        <EmptyState>No earlier purchases for this player.</EmptyState>
      ) : (
        <table className="review-table review-rules">
          <thead>
            <tr>
              <th scope="col">Transaction</th>
              <th scope="col">Purchase time</th>
              <th scope="col">Amount (USD)</th>
              <th scope="col">Outcome</th>
            </tr>
          </thead>
          <tbody>
            {review.player_purchases.map((purchase) => (
              <tr key={purchase.transaction_id}>
                <th scope="row">{purchase.transaction_id}</th>
                <td><Timestamp value={purchase.event_time} display={time} /></td>
                <td>{formatUsdMinor(purchase.amount_minor)}</td>
                <td><OutcomeBadge outcome={purchase.outcome} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <h4>Recent device activity</h4>
      {review.device_activity === null ? (
        <EmptyState>This purchase did not include a device ID.</EmptyState>
      ) : (
        <dl className="review-facts">
          <dt>Purchases on this device</dt>
          <dd>{review.device_activity.purchases}</dd>
          <dt>Distinct cards</dt>
          <dd>{review.device_activity.distinct_cards}</dd>
          <dt>Last seen</dt>
          <dd><Timestamp value={review.device_activity.last_seen_at} display={time} /></dd>
        </dl>
      )}
    </>
  )
}

function ReviewQueue() {
  const [apiKey, setApiKey] = useState<string | null>(null)
  const [keyError, setKeyError] = useState<string | null>(null)
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [page, setPage] = useState<QueuePage | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [openCase, setOpenCase] = useState<string | null>(null)
  const [confirmation, setConfirmation] = useState<ReviewAction | null>(null)
  const [feedback, setFeedback] = useState<{ text: string; failed?: boolean } | null>(null)
  const [blockDevice, setBlockDevice] = useState(false)
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set())
  const time = useTimeDisplay()
  // A null controller keeps the action disabled until a successful poll verifies queue state.
  const requests = useRef(new Map<string, AbortController | null>())
  const dialog = useRef<HTMLDialogElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const cursor = cursors[cursors.length - 1]

  const cancelDecisions = useCallback(() => {
    for (const request of requests.current.values()) request?.abort()
    requests.current.clear()
  }, [])

  useEffect(() => cancelDecisions, [cancelDecisions])

  useEffect(() => {
    if (confirmation && !dialog.current?.open) dialog.current?.showModal()
  }, [confirmation])

  const loadQueue = useMemo(() => {
    if (apiKey === null) return null
    return (signal: AbortSignal) => listReviews(apiKey, cursor, signal)
  }, [apiKey, cursor])
  const { stop, refresh, retry, pending: queuePending } = usePolling(loadQueue, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: (result) => {
      // Only a successful read listing the purchase confirms it is still pending.
      let reconciled = false
      for (const [id, inFlight] of requests.current) {
        if (inFlight !== null || !result.items.some(row => row.purchase_id === id)) continue
        requests.current.delete(id)
        reconciled = true
      }
      if (reconciled) setPending(new Set(requests.current.keys()))
      setPage(result)
      setLoadError(null)
    },
    onError: (error) => {
      const message = error instanceof Error ? error.message : 'Could not load the review queue.'
      if (error instanceof ApiError && error.status === 401) {
        rejectKey(message)
        return
      }
      setLoadError(message)
    },
  })

  const rejectKey = useCallback((message: string) => {
    stop()
    cancelDecisions()
    setPending(new Set())
    setApiKey(null)
    setKeyError(message)
    setPage(null)
    setCursors([null])
    setOpenCase(null)
    setConfirmation(null)
    setFeedback(null)
    setLoadError(null)
  }, [cancelDecisions, stop])

  function openQueue(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const key = new FormData(event.currentTarget).get('apiKey')
    if (typeof key !== 'string') return
    setKeyError(null)
    setLoadError(null)
    setApiKey(key.trim())
  }

  function goTo(next: (string | null)[]) {
    stop()
    setPage(null)
    setLoadError(null)
    setCursors(next)
  }

  function chooseDecision(item: Review, decision: Decision) {
    if (requests.current.has(item.purchase_id)) return
    setBlockDevice(false)
    setConfirmation({ item, decision })
  }

  async function saveDecision({ item, decision }: ReviewAction) {
    const id = item.purchase_id
    if (apiKey === null || requests.current.has(id)) return

    const controller = new AbortController()
    requests.current.set(id, controller)
    setPending(new Set(requests.current.keys()))
    setFeedback(null)
    const isCurrent = () => requests.current.get(id) === controller
    let needsReconciliation = false
    try {
      const deviceBlocked = await submitReviewDecision(apiKey, id, decision, blockDevice, controller.signal)
      if (!isCurrent()) return
      removeDecidedPurchase(id)
      setFeedback({ text: `${decision === 'approve' ? 'Approved' : 'Rejected'} ${item.transaction_id}${deviceBlocked ? ' and blocked its device' : ''}.` })
    } catch (error) {
      if (!isCurrent()) return
      const status = error instanceof ApiError ? error.status : undefined
      const message = error instanceof Error ? error.message : 'Could not save the decision.'
      if (status === 401) {
        rejectKey(message)
      } else if (status === 404) {
        setFeedback({ text: `Could not find ${item.transaction_id}. ${message}`, failed: true })
      } else if (status === 409) {
        removeDecidedPurchase(id)
        setFeedback({ text: `${item.transaction_id} is no longer pending review. ${message}` })
      } else if (status !== undefined && status < 500 && error instanceof ApiError && error.kind === 'http') {
        setFeedback({ text: `Could not save a decision for ${item.transaction_id}. ${message}`, failed: true })
      } else {
        needsReconciliation = true
        setFeedback({ text: `Could not confirm the decision for ${item.transaction_id}. ${message} Check whether it is still pending after the queue refreshes.`, failed: true })
      }
    } finally {
      if (isCurrent()) {
        if (needsReconciliation) requests.current.set(id, null)
        else requests.current.delete(id)
        setPending(new Set(requests.current.keys()))
        refresh()
      }
    }
  }

  function removeDecidedPurchase(id: string) {
    if (document.activeElement?.closest('tr')?.dataset.purchaseId === id) heading.current?.focus()
    setPage(current => current && {
      ...current,
      items: current.items.filter(row => row.purchase_id !== id),
    })
  }

  const nextCursor = page?.next_cursor ?? null
  const hasPrevious = cursors.length > 1

  return (
    <section aria-labelledby="reviews-heading">
      <h2 id="reviews-heading" ref={heading} tabIndex={-1}>Review queue</h2>
      {apiKey !== null && (openCase !== null || (page && page.items.length > 0)) && <TimeDisplayToggle display={time} />}
      {apiKey === null ? (
        <form className="review-key-form" method="post" onSubmit={openQueue}>
          <label htmlFor="review-api-key">Analyst key</label>
          <p id="review-key-hint">Enter your individual review key.</p>
          {keyError && <p id="review-key-error" role="alert">The analyst key was rejected. Enter a valid key. {keyError}</p>}
          <input
            id="review-api-key"
            name="apiKey"
            type="password"
            autoComplete="off"
            autoFocus={keyError !== null}
            aria-describedby={keyError ? 'review-key-hint review-key-error' : 'review-key-hint'}
            aria-invalid={keyError !== null || undefined}
            required
          />
          <button type="submit">Open queue</button>
        </form>
      ) : openCase !== null ? (
        <CaseView key={openCase} apiKey={apiKey} purchaseId={openCase} time={time} onBack={() => setOpenCase(null)} onKeyRejected={rejectKey} />
      ) : (
        <>
          <p role="status">{feedback && !feedback.failed ? feedback.text : ''}</p>
          {feedback?.failed && (
            <ErrorState polite onRetry={loadError ? undefined : retry} pending={queuePending}>{feedback.text}</ErrorState>
          )}
          {loadError && (
            <ErrorState polite onRetry={retry} pending={queuePending}>
              {loadError} {page && 'Showing the last successful update for this page. '}Retrying automatically.
            </ErrorState>
          )}
          {page === null && (!loadError || queuePending) && <LoadingSkeleton shape="table" label="Loading review queue…" />}
          {page && (page.items.length > 0 ? (
            <ReviewTable items={page.items} pending={pending} time={time} onOpen={setOpenCase} onDecision={chooseDecision} />
          ) : (
            <EmptyState>
              {cursor === null ? (
                <>No purchases are waiting for review. Score a purchase in <a href="#/tester">Tester</a> or run <code>make simulate</code> to generate traffic.</>
              ) : 'No purchases on this page. Choose Previous to see the earlier page.'}
            </EmptyState>
          ))}
          <nav className="review-pages" aria-label="Review queue pages">
            <button type="button" aria-disabled={!hasPrevious} onClick={() => {
              if (hasPrevious) goTo(cursors.slice(0, -1))
            }}>
              Previous
            </button>
            <span aria-live="polite">Page {cursors.length}</span>
            <button type="button" aria-disabled={nextCursor === null} onClick={() => {
              if (nextCursor !== null) goTo([...cursors, nextCursor])
            }}>
              Next
            </button>
          </nav>
          <dialog className="review-confirmation" ref={dialog} aria-labelledby="review-confirmation-title"
            onClose={event => {
              const confirmed = event.currentTarget.returnValue === 'confirm'
              event.currentTarget.returnValue = ''
              setConfirmation(null)
              if (confirmed && confirmation) void saveDecision(confirmation)
            }}>
            <h3 id="review-confirmation-title">
              {confirmation?.decision === 'approve' ? 'Approve' : 'Reject'} this purchase?
            </h3>
            <p>Transaction {confirmation?.item.transaction_id}. This decision is final.</p>
            {confirmation?.decision === 'reject' && confirmation.item.device_id !== null && (
              <label className="review-block-device">
                <input type="checkbox" checked={blockDevice} onChange={event => setBlockDevice(event.target.checked)} />
                {' '}Also block this purchase&apos;s device ({confirmation.item.device_id})
              </label>
            )}
            <form method="dialog" className="review-actions">
              <button value="cancel" className="secondary" autoFocus>Cancel</button>
              <button value="confirm">{confirmation?.decision === 'approve' ? 'Approve' : 'Reject'}</button>
            </form>
          </dialog>
        </>
      )}
    </section>
  )
}

export default ReviewQueue
