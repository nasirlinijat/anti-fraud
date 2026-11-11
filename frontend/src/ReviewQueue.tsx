import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type RefObject } from 'react'
import { getReviewCase, listReviews, submitReviewDecision, type Decision, type QueuePage, type Review, type ReviewCase, type ReviewSort } from './api'
import { ModelRiskBadge, OutcomeBadge, RuleScoreBadge, TimeDisplayToggle, Timestamp } from './Display'
import { formatUsdMinor } from './displayFormat'
import { ApiError } from './request'
import { usePolling } from './usePolling'
import { useTimeDisplay, type TimeDisplay } from './useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from './ViewState'

type ReviewAction = { item: Review; decision: Decision }
type Feedback = { text: string; failed?: boolean }
const REFRESH_MS = 3_000

function ReviewTable({ items, pending, time, sort, modelEnabled, onOpen, onDecision }: {
  items: Review[]
  pending: ReadonlySet<string>
  time: TimeDisplay
  sort: ReviewSort
  modelEnabled: boolean
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
        <caption id="review-caption">Purchases awaiting review</caption>
        <thead>
          <tr>
            <th scope="col">Transaction</th>
            <th scope="col">Player</th>
            <th scope="col">Amount (USD)</th>
            <th scope="col">Purchase time</th>
            <th scope="col">Queued time</th>
            <th scope="col" aria-sort={sort === 'rule_score' ? 'descending' : undefined}>Rule score</th>
            {modelEnabled && <th scope="col" aria-sort={sort === 'model_score' ? 'descending' : undefined}>Model risk</th>}
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
              {modelEnabled && <td><ModelRiskBadge value={item.model_score} /></td>}
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
  const time = useTimeDisplay()
  const heading = useRef<HTMLHeadingElement>(null)

  function openQueue(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const key = new FormData(event.currentTarget).get('apiKey')
    if (typeof key !== 'string') return
    setKeyError(null)
    setApiKey(key.trim())
  }

  // Closing the session unmounts QueueSession, which discards its queue state.
  function rejectKey(message: string) {
    setApiKey(null)
    setKeyError(message)
  }

  return (
    <section aria-labelledby="reviews-heading">
      <h2 id="reviews-heading" ref={heading} tabIndex={-1}>Review queue</h2>
      {apiKey === null ? (
        <AnalystKeyForm keyError={keyError} onSubmit={openQueue} />
      ) : (
        <QueueSession apiKey={apiKey} time={time} heading={heading} onKeyRejected={rejectKey} />
      )}
    </section>
  )
}

function AnalystKeyForm({ keyError, onSubmit }: {
  keyError: string | null
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  return (
    <form className="review-key-form" method="post" onSubmit={onSubmit}>
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
  )
}

// QueueSession is the queue while an analyst key is in use. It is mounted
// once per accepted key, so a rejected key starts the next session clean.
function QueueSession({ apiKey, time, heading, onKeyRejected }: {
  apiKey: string
  time: TimeDisplay
  heading: RefObject<HTMLHeadingElement | null>
  onKeyRejected: (message: string) => void
}) {
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [sort, setSort] = useState<ReviewSort>('rule_score')
  const [modelEnabled, setModelEnabled] = useState(false)
  const [page, setPage] = useState<QueuePage | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [openCase, setOpenCase] = useState<string | null>(null)
  const cursor = cursors[cursors.length - 1]

  const loadQueue = useCallback(
    (signal: AbortSignal) => listReviews(apiKey, sort, cursor, signal),
    [apiKey, sort, cursor],
  )
  const { stop, refresh, retry, pending: queuePending } = usePolling(loadQueue, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: (result) => {
      setModelEnabled(result.model_enabled)
      if (!result.model_enabled && sort === 'model_score') {
        changeSort('rule_score')
        return
      }
      decisions.requests.reconcile(result.items)
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

  const decisions = useDecisions({ apiKey, refresh, onKeyRejected: rejectKey, onDecided: removeDecidedPurchase })

  function rejectKey(message: string) {
    stop()
    decisions.requests.cancelAll()
    onKeyRejected(message)
  }

  function goTo(next: (string | null)[]) {
    stop()
    setPage(null)
    setLoadError(null)
    setCursors(next)
  }

  function changeSort(next: ReviewSort) {
    setSort(next)
    goTo([null])
  }

  function removeDecidedPurchase(id: string) {
    if (document.activeElement?.closest('tr')?.dataset.purchaseId === id) heading.current?.focus()
    setPage(current => current && {
      ...current,
      items: current.items.filter(row => row.purchase_id !== id),
    })
  }

  const nextCursor = page?.next_cursor ?? null
  const { feedback } = decisions

  return (
    <>
      {(openCase !== null || (page && page.items.length > 0)) && <TimeDisplayToggle display={time} />}
      {openCase !== null ? (
        <CaseView key={openCase} apiKey={apiKey} purchaseId={openCase} time={time} onBack={() => setOpenCase(null)} onKeyRejected={rejectKey} />
      ) : (
        <>
          {modelEnabled && <SortSelect sort={sort} onChange={changeSort} />}
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
          {page && page.items.length > 0 && (
            <ReviewTable items={page.items} pending={decisions.requests.pending} time={time} sort={sort} modelEnabled={modelEnabled} onOpen={setOpenCase} onDecision={decisions.choose} />
          )}
          {page && page.items.length === 0 && <EmptyQueue firstPage={cursor === null} />}
          <QueuePages
            number={cursors.length}
            onPrevious={cursors.length > 1 ? () => goTo(cursors.slice(0, -1)) : undefined}
            onNext={nextCursor !== null ? () => goTo([...cursors, nextCursor]) : undefined}
          />
          <ConfirmationDialog dialogRef={decisions.dialog} confirmation={decisions.confirmation}
            blockDevice={decisions.blockDevice} onBlockDevice={decisions.setBlockDevice}
            onDismiss={decisions.dismiss} onConfirm={(action) => void decisions.save(action)} />
        </>
      )}
    </>
  )
}

// Takes a decision from the analyst's confirmation to its saved result, and
// reports that result as feedback.
function useDecisions({ apiKey, refresh, onKeyRejected, onDecided }: {
  apiKey: string
  refresh: () => void
  onKeyRejected: (message: string) => void
  onDecided: (purchaseId: string) => void
}) {
  const [confirmation, setConfirmation] = useState<ReviewAction | null>(null)
  const [feedback, setFeedback] = useState<Feedback | null>(null)
  const [blockDevice, setBlockDevice] = useState(false)
  const requests = useDecisionRequests()
  const dialog = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    if (confirmation && !dialog.current?.open) dialog.current?.showModal()
  }, [confirmation])

  function choose(item: Review, decision: Decision) {
    if (requests.has(item.purchase_id)) return
    setBlockDevice(false)
    setConfirmation({ item, decision })
  }

  async function save({ item, decision }: ReviewAction) {
    const id = item.purchase_id
    if (requests.has(id)) return

    const controller = requests.begin(id)
    setFeedback(null)
    const isCurrent = () => requests.isCurrent(id, controller)
    let unconfirmed = false
    try {
      const deviceBlocked = await submitReviewDecision(apiKey, id, decision, blockDevice, controller.signal)
      if (!isCurrent()) return
      onDecided(id)
      setFeedback({ text: `${decision === 'approve' ? 'Approved' : 'Rejected'} ${item.transaction_id}${deviceBlocked ? ' and blocked its device' : ''}.` })
    } catch (error) {
      if (!isCurrent()) return
      if (error instanceof ApiError && error.status === 401) {
        onKeyRejected(error.message)
        return
      }
      const failure = describeFailure(error, item.transaction_id)
      if (failure.decided) onDecided(id)
      unconfirmed = failure.unconfirmed === true
      setFeedback(failure.feedback)
    } finally {
      if (isCurrent()) {
        if (unconfirmed) requests.holdForReconciliation(id)
        else requests.finish(id)
        refresh()
      }
    }
  }

  const dismiss = () => setConfirmation(null)
  return { requests, confirmation, feedback, blockDevice, dialog, setBlockDevice, choose, save, dismiss }
}

// Tracks the decisions being saved, by purchase ID, so each purchase can have
// only one save in flight and its buttons stay disabled until it settles.
function useDecisionRequests() {
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
    cancelAll,
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

type DecisionFailure = { feedback: Feedback; decided?: boolean; unconfirmed?: boolean }

// A 409 means the purchase was already decided. A failure that is not a clear
// client error leaves the outcome unknown until the queue is read again.
function describeFailure(error: unknown, transactionId: string): DecisionFailure {
  const status = error instanceof ApiError ? error.status : undefined
  const message = error instanceof Error ? error.message : 'Could not save the decision.'
  if (status === 404) {
    return { feedback: { text: `Could not find ${transactionId}. ${message}`, failed: true } }
  }
  if (status === 409) {
    return { decided: true, feedback: { text: `${transactionId} is no longer pending review. ${message}` } }
  }
  if (status !== undefined && status < 500 && error instanceof ApiError && error.kind === 'http') {
    return { feedback: { text: `Could not save a decision for ${transactionId}. ${message}`, failed: true } }
  }
  return {
    unconfirmed: true,
    feedback: { text: `Could not confirm the decision for ${transactionId}. ${message} Check whether it is still pending after the queue refreshes.`, failed: true },
  }
}

function SortSelect({ sort, onChange }: { sort: ReviewSort; onChange: (sort: ReviewSort) => void }) {
  return (
    <label>
      Sort by{' '}
      <select value={sort} onChange={event => onChange(event.target.value === 'model_score' ? 'model_score' : 'rule_score')}>
        <option value="rule_score">Rule score</option>
        <option value="model_score">Model risk</option>
      </select>
    </label>
  )
}

function EmptyQueue({ firstPage }: { firstPage: boolean }) {
  if (!firstPage) {
    return <EmptyState>No purchases on this page. Choose Previous to see the earlier page.</EmptyState>
  }
  return (
    <EmptyState>
      No purchases are waiting for review. Score a purchase in <a href="#/tester">Tester</a> or run <code>make simulate</code> to generate traffic.
    </EmptyState>
  )
}

// A missing handler means there is no page in that direction.
function QueuePages({ number, onPrevious, onNext }: {
  number: number
  onPrevious?: () => void
  onNext?: () => void
}) {
  return (
    <nav className="review-pages" aria-label="Review queue pages">
      <button type="button" aria-disabled={!onPrevious} onClick={() => onPrevious?.()}>
        Previous
      </button>
      <span aria-live="polite">Page {number}</span>
      <button type="button" aria-disabled={!onNext} onClick={() => onNext?.()}>
        Next
      </button>
    </nav>
  )
}

function ConfirmationDialog({ dialogRef, confirmation, blockDevice, onBlockDevice, onDismiss, onConfirm }: {
  dialogRef: RefObject<HTMLDialogElement | null>
  confirmation: ReviewAction | null
  blockDevice: boolean
  onBlockDevice: (block: boolean) => void
  onDismiss: () => void
  onConfirm: (action: ReviewAction) => void
}) {
  return (
    <dialog className="review-confirmation" ref={dialogRef} aria-labelledby="review-confirmation-title"
      onClose={event => {
        const confirmed = event.currentTarget.returnValue === 'confirm'
        event.currentTarget.returnValue = ''
        onDismiss()
        if (confirmed && confirmation) onConfirm(confirmation)
      }}>
      <h3 id="review-confirmation-title">
        {confirmation?.decision === 'approve' ? 'Approve' : 'Reject'} this purchase?
      </h3>
      <p>Transaction {confirmation?.item.transaction_id}. This decision is final.</p>
      {confirmation?.decision === 'reject' && confirmation.item.device_id !== null && (
        <label className="review-block-device">
          <input type="checkbox" checked={blockDevice} onChange={event => onBlockDevice(event.target.checked)} />
          {' '}Also block this purchase&apos;s device ({confirmation.item.device_id})
        </label>
      )}
      <form method="dialog" className="review-actions">
        <button value="cancel" className="secondary" autoFocus>Cancel</button>
        <button value="confirm">{confirmation?.decision === 'approve' ? 'Approve' : 'Reject'}</button>
      </form>
    </dialog>
  )
}

export default ReviewQueue
