import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'

type Review = {
  purchase_id: string
  transaction_id: string
  player_id: string
  device_id: string | null
  amount_minor: string
  event_time: string
  queued_at: string
  rule_score: number
  model_score: number | null
  model_version: string | null
  fired_rules: { name: string; points: number; message: string }[]
}

type ReviewCase = Review & {
  device_id: string | null
  ruleset_version: number
}

type QueuePage = {
  items: Review[]
  next_cursor: string | null
}

type Decision = 'approve' | 'reject'
type ReviewAction = { item: Review; decision: Decision }

const timestamp = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'medium',
  timeZone: 'UTC',
})
const amount = new Intl.NumberFormat()

function ReviewTable({ items, pending, onOpen, onDecision }: {
  items: Review[]
  pending: ReadonlySet<string>
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
            <th scope="col">Amount (minor units)</th>
            <th scope="col">Purchase time (UTC)</th>
            <th scope="col">Queued time (UTC)</th>
            <th scope="col">Rule score</th>
            <th scope="col">Model score</th>
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
              <td>{amount.format(BigInt(item.amount_minor))}</td>
              <td><time dateTime={item.event_time}>{timestamp.format(new Date(item.event_time))}</time></td>
              <td><time dateTime={item.queued_at}>{timestamp.format(new Date(item.queued_at))}</time></td>
              <td>{item.rule_score}</td>
              <td>{item.model_score ?? 'Unavailable'}</td>
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
  | { status: 'failed' }
  | { status: 'loaded'; review: ReviewCase }

function CaseView({ apiKey, purchaseId, onBack, onKeyRejected }: {
  apiKey: string
  purchaseId: string
  onBack: () => void
  onKeyRejected: () => void
}) {
  const [state, setState] = useState<CaseState>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    const deadline = window.setTimeout(() => controller.abort(), 10_000)
    let stopped = false

    async function load() {
      try {
        const response = await fetch(`/reviews/${encodeURIComponent(purchaseId)}`, {
          headers: { 'X-API-Key': apiKey },
          signal: controller.signal,
        })
        if (stopped) return
        if (response.status === 401) {
          onKeyRejected()
          return
        }
        if (response.status === 404) {
          setState({ status: 'not-found' })
          return
        }
        if (!response.ok) throw new Error('Could not load the review case')
        const review: ReviewCase = await response.json()
        if (!stopped) setState({ status: 'loaded', review })
      } catch {
        if (!stopped) setState({ status: 'failed' })
      } finally {
        window.clearTimeout(deadline)
      }
    }

    void load()
    return () => {
      stopped = true
      window.clearTimeout(deadline)
      controller.abort()
    }
  }, [apiKey, purchaseId, attempt, onKeyRejected])

  return (
    <article className="review-case" aria-labelledby="case-heading">
      <button type="button" className="review-back" onClick={onBack}>Back to the queue</button>
      <h3 id="case-heading">Review case</h3>
      {state.status === 'loading' && <p>Loading review case…</p>}
      {state.status === 'not-found' && (
        <p role="status">This purchase is not under review. It may have been decided, or the ID is wrong.</p>
      )}
      {state.status === 'failed' && (
        <p role="alert">
          Could not load this review case.{' '}
          <button type="button" onClick={() => {
            setState({ status: 'loading' })
            setAttempt(attempt + 1)
          }}>
            Try again
          </button>
        </p>
      )}
      {state.status === 'loaded' && <CaseDetails review={state.review} />}
    </article>
  )
}

function CaseDetails({ review }: { review: ReviewCase }) {
  return (
    <>
      <dl className="review-facts">
        <dt>Transaction</dt>
        <dd>{review.transaction_id}</dd>
        <dt>Player</dt>
        <dd>{review.player_id}</dd>
        <dt>Device</dt>
        <dd>{review.device_id ?? 'None'}</dd>
        <dt>Amount (minor units)</dt>
        <dd>{amount.format(BigInt(review.amount_minor))}</dd>
        <dt>Purchase time (UTC)</dt>
        <dd><time dateTime={review.event_time}>{timestamp.format(new Date(review.event_time))}</time></dd>
        <dt>Queued time (UTC)</dt>
        <dd><time dateTime={review.queued_at}>{timestamp.format(new Date(review.queued_at))}</time></dd>
        <dt>Ruleset version</dt>
        <dd>{review.ruleset_version}</dd>
        <dt>Rule score</dt>
        <dd>{review.rule_score}</dd>
        <dt>Model score</dt>
        <dd>{review.model_score ?? 'Unavailable'}</dd>
        <dt>Model version</dt>
        <dd>{review.model_version ?? 'None'}</dd>
      </dl>
      <h4>Fired rules</h4>
      {review.fired_rules.length === 0 ? <p>No rules fired.</p> : (
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
    </>
  )
}

function ReviewQueue() {
  const [apiKey, setApiKey] = useState<string | null>(null)
  const [keyRejected, setKeyRejected] = useState(false)
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [page, setPage] = useState<QueuePage | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [openCase, setOpenCase] = useState<string | null>(null)
  const [accessUnavailable, setAccessUnavailable] = useState(false)
  const [confirmation, setConfirmation] = useState<ReviewAction | null>(null)
  const [feedback, setFeedback] = useState('')
  const [blockDevice, setBlockDevice] = useState(false)
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set())
  const [refreshVersion, setRefreshVersion] = useState(0)
  const pollVersion = useRef(0)
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

  const rejectKey = useCallback(() => {
    pollVersion.current++
    cancelDecisions()
    setPending(new Set())
    setApiKey(null)
    setKeyRejected(true)
    setPage(null)
    setCursors([null])
    setOpenCase(null)
    setConfirmation(null)
    setFeedback('')
  }, [cancelDecisions])

  useEffect(() => {
    if (apiKey === null) return

    const headers = { 'X-API-Key': apiKey }
    const url = cursor === null ? '/reviews' : '/reviews?' + new URLSearchParams({ cursor })
    let stopped = false
    const version = pollVersion.current
    const isCurrent = () => !stopped && pollVersion.current === version
    let retryTimer: number | undefined
    let request: AbortController | undefined

    async function refresh() {
      const controller = new AbortController()
      request = controller
      const deadline = window.setTimeout(() => controller.abort(), 10_000)

      try {
        const response = await fetch(url, {
          headers,
          signal: controller.signal,
        })
        if (!isCurrent()) return
        if (response.status === 401) {
          rejectKey()
          return
        }
        if (response.status === 503) {
          setAccessUnavailable(true)
          throw new Error('Review access unavailable')
        }
        if (!response.ok) throw new Error('Could not load the review queue')

        const result: QueuePage = await response.json()
        if (!isCurrent()) return
        // An unconfirmed save is cleared only when this page still lists the
        // purchase, so it is known to be pending. A poll of another page says
        // nothing about it, and the save may still commit.
        let reconciled = false
        for (const [id, inFlight] of requests.current) {
          if (inFlight !== null || !result.items.some(row => row.purchase_id === id)) continue
          requests.current.delete(id)
          reconciled = true
        }
        if (reconciled) setPending(new Set(requests.current.keys()))
        setPage(result)
        setLoadFailed(false)
        setAccessUnavailable(false)
      } catch {
        if (isCurrent()) setLoadFailed(true)
      } finally {
        window.clearTimeout(deadline)
        if (isCurrent()) retryTimer = window.setTimeout(refresh, 5_000)
      }
    }

    void refresh()
    return () => {
      stopped = true
      window.clearTimeout(retryTimer)
      request?.abort()
    }
  }, [apiKey, cursor, refreshVersion, rejectKey])

  function openQueue(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const key = new FormData(event.currentTarget).get('apiKey')
    if (typeof key !== 'string') return
    setKeyRejected(false)
    setLoadFailed(false)
    setAccessUnavailable(false)
    setApiKey(key.trim())
  }

  function goTo(next: (string | null)[]) {
    pollVersion.current++
    setPage(null)
    setLoadFailed(false)
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
    setFeedback('')
    const deadline = window.setTimeout(() => controller.abort(), 10_000)
    const isCurrent = () => requests.current.get(id) === controller
    let needsReconciliation = false
    try {
      const response = await fetch(`/reviews/${encodeURIComponent(id)}/decision`, {
        method: 'POST',
        headers: { 'X-API-Key': apiKey, 'Content-Type': 'application/json' },
        body: JSON.stringify(decision === 'reject' && blockDevice ? { decision, block_device: true } : { decision }),
        signal: controller.signal,
      })
      if (!isCurrent()) return
      if (response.status === 401) {
        rejectKey()
        return
      }
      if (response.status === 503) {
        setFeedback('Review access is unavailable. Check the backend configuration.')
        return
      }
      if (response.status === 404) {
        setFeedback(`${item.transaction_id} was not found. The queue is refreshing.`)
        return
      }
      if (response.status !== 201 && response.status !== 409) {
        if (response.status >= 500) throw new Error('Review save could not be confirmed')
        setFeedback(`Could not save a decision for ${item.transaction_id}.`)
        return
      }

      if (document.activeElement?.closest('tr')?.dataset.purchaseId === id) heading.current?.focus()
      setPage(current => current && {
        ...current,
        items: current.items.filter(row => row.purchase_id !== id),
      })
      let deviceBlocked = false
      if (response.status === 201) {
        deviceBlocked = ((await response.json().catch(() => null)) as { device_blocked?: boolean } | null)?.device_blocked === true
      }
      setFeedback(response.status === 201
        ? `${decision === 'approve' ? 'Approved' : 'Rejected'} ${item.transaction_id}${deviceBlocked ? ' and blocked its device' : ''}.`
        : `${item.transaction_id} is no longer pending review.`)
    } catch {
      if (!isCurrent()) return
      needsReconciliation = true
      setFeedback(`Could not confirm the decision for ${item.transaction_id}. You can retry if it is still listed after the queue refreshes.`)
    } finally {
      window.clearTimeout(deadline)
      if (isCurrent()) {
        if (needsReconciliation) requests.current.set(id, null)
        else requests.current.delete(id)
        setPending(new Set(requests.current.keys()))
        pollVersion.current++
        setRefreshVersion(pollVersion.current)
      }
    }
  }

  const nextCursor = page?.next_cursor ?? null
  const hasPrevious = cursors.length > 1

  return (
    <section aria-labelledby="reviews-heading">
      <h2 id="reviews-heading" ref={heading} tabIndex={-1}>Review queue</h2>
      {apiKey === null ? (
        <form className="review-key-form" method="post" onSubmit={openQueue}>
          <label htmlFor="review-api-key">Analyst key</label>
          <p id="review-key-hint">Enter your individual review key.</p>
          {keyRejected && <p id="review-key-error" role="alert">The analyst key was rejected. Enter a valid key.</p>}
          <input
            id="review-api-key"
            name="apiKey"
            type="password"
            autoComplete="off"
            autoFocus={keyRejected}
            aria-describedby={keyRejected ? 'review-key-hint review-key-error' : 'review-key-hint'}
            aria-invalid={keyRejected || undefined}
            required
          />
          <button type="submit">Open queue</button>
        </form>
      ) : openCase !== null ? (
        <CaseView key={openCase} apiKey={apiKey} purchaseId={openCase} onBack={() => setOpenCase(null)} onKeyRejected={rejectKey} />
      ) : (
        <>
          <p role="status">{feedback}</p>
          {loadFailed && (
            <p role="status">
              {accessUnavailable ? 'Review access is unavailable. Check the backend configuration.' :
                page ? 'Refresh failed. The queue shown may be out of date.' : 'Could not load this page.'}
              {' Retrying automatically.'}
            </p>
          )}
          {page === null && !loadFailed && <p>Loading review queue…</p>}
          {page && (page.items.length > 0 ? (
            <ReviewTable items={page.items} pending={pending} onOpen={setOpenCase} onDecision={chooseDecision} />
          ) : (
            <p>{cursor === null ? 'No purchases are waiting for review.' : 'No purchases on this page.'}</p>
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
