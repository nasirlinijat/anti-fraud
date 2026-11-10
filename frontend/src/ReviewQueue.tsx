import { useEffect, useState, type FormEvent, type KeyboardEvent } from 'react'

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

const timestamp = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'medium',
  timeZone: 'UTC',
})
const amount = new Intl.NumberFormat()

function ReviewTable({ items }: { items: Review[] }) {
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
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr key={item.purchase_id}>
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
  const cursor = cursors[cursors.length - 1]

  useEffect(() => {
    if (apiKey === null) return

    const headers = { 'X-API-Key': apiKey }
    const url = cursor === null ? '/reviews' : '/reviews?' + new URLSearchParams({ cursor })
    let stopped = false
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
        if (stopped) return
        if (response.status === 401) {
          stopped = true
          setApiKey(null)
          setKeyRejected(true)
          setPage(null)
          setCursors([null])
          return
        }
        if (!response.ok) throw new Error('Could not load the review queue')

        const result: QueuePage = await response.json()
        if (stopped) return
        setPage(result)
        setLoadFailed(false)
      } catch {
        if (!stopped) setLoadFailed(true)
      } finally {
        window.clearTimeout(deadline)
        if (!stopped) retryTimer = window.setTimeout(refresh, 5_000)
      }
    }

    void refresh()
    return () => {
      stopped = true
      window.clearTimeout(retryTimer)
      request?.abort()
    }
  }, [apiKey, cursor])

  function openQueue(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const key = new FormData(event.currentTarget).get('apiKey')
    if (typeof key !== 'string') return
    setKeyRejected(false)
    setLoadFailed(false)
    setApiKey(key.trim())
  }

  function goTo(next: (string | null)[]) {
    setPage(null)
    setLoadFailed(false)
    setCursors(next)
  }

  const nextCursor = page?.next_cursor ?? null
  const hasPrevious = cursors.length > 1

  return (
    <section aria-labelledby="reviews-heading">
      <h2 id="reviews-heading">Review queue</h2>
      {apiKey === null ? (
        <form className="review-key-form" method="post" onSubmit={openQueue}>
          <label htmlFor="review-api-key">Local API key</label>
          <p id="review-key-hint">Use the API_KEY from your local .env file.</p>
          {keyRejected && <p id="review-key-error" role="alert">The API key was rejected. Enter a valid key.</p>}
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
          {loadFailed && (
            <p role="status">
              {page ? 'Refresh failed. The queue shown may be out of date.' : 'Could not load this page.'}
              {' Retrying automatically.'}
            </p>
          )}
          {page === null && !loadFailed && <p>Loading review queue…</p>}
          {page && (page.items.length > 0 ? <ReviewTable items={page.items} /> : (
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
        </>
      )}
    </section>
  )
}

export default ReviewQueue
