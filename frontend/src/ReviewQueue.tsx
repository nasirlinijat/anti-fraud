import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'

type Review = {
  purchase_id: string
  transaction_id: string
  player_id: string
  amount_minor: string
  event_time: string
  queued_at: string
  rule_score: number
  model_score: number | null
  model_version: string | null
  fired_rules: { name: string; points: number }[]
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

function ReviewTable({ items, pending, onDecision }: {
  items: Review[]
  pending: ReadonlySet<string>
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
              <th scope="row">{item.transaction_id}</th>
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
                      <li key={rule.name}>{rule.name} ({rule.points} points)</li>
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

function ReviewQueue() {
  const [apiKey, setApiKey] = useState<string | null>(null)
  const [keyRejected, setKeyRejected] = useState(false)
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [page, setPage] = useState<QueuePage | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [accessUnavailable, setAccessUnavailable] = useState(false)
  const [confirmation, setConfirmation] = useState<ReviewAction | null>(null)
  const [feedback, setFeedback] = useState('')
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
        let reconciled = false
        for (const [id, request] of requests.current) {
          if (request !== null) continue
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
    if (!requests.current.has(item.purchase_id)) setConfirmation({ item, decision })
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
        body: JSON.stringify({ decision }),
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
      if (response.status !== 201 && response.status !== 409 && response.status !== 404) {
        if (response.status >= 500) throw new Error('Review save could not be confirmed')
        setFeedback(`Could not save a decision for ${item.transaction_id}.`)
        return
      }

      if (document.activeElement?.closest('tr')?.dataset.purchaseId === id) heading.current?.focus()
      setPage(current => current && {
        ...current,
        items: current.items.filter(row => row.purchase_id !== id),
      })
      setFeedback(response.status === 201
        ? `${decision === 'approve' ? 'Approved' : 'Rejected'} ${item.transaction_id}.`
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
            <ReviewTable items={page.items} pending={pending} onDecision={chooseDecision} />
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
