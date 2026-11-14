import { useEffect, useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import { searchPurchases, type PurchaseMatch } from './api'
import { OutcomeBadge } from './Display'
import { formatUsdMinor } from './displayFormat'
import { purchaseAddress } from './purchaseAddress'
import { caseAddress } from './reviews/caseAddress'

type SearchState =
  | { status: 'idle' }
  | { status: 'searching' }
  | { status: 'results'; query: string; items: PurchaseMatch[] }
  | { status: 'failed'; message: string }

const matchedBy: Record<PurchaseMatch['matched_by'], string> = {
  transaction: 'transaction ID',
  player: 'player ID',
  card: 'card token',
}

const reviewText: Record<NonNullable<PurchaseMatch['review']>, string> = {
  open: 'review open',
  approved: 'approved',
  rejected: 'rejected',
}

function matchAddress(match: PurchaseMatch): string {
  return match.review === 'open' ? caseAddress(match.purchase_id) : purchaseAddress(match.purchase_id)
}

export function PurchaseSearch() {
  const id = useId()
  const [query, setQuery] = useState('')
  const [state, setState] = useState<SearchState>({ status: 'idle' })

  useEffect(() => {
    const close = () => setState({ status: 'idle' })
    window.addEventListener('hashchange', close)
    return () => window.removeEventListener('hashchange', close)
  }, [])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const value = query.trim()
    if (!value) return
    setState({ status: 'searching' })
    try {
      const items = await searchPurchases(value)
      if (items.length === 1) {
        setState({ status: 'idle' })
        window.location.hash = matchAddress(items[0])
        return
      }
      setState({ status: 'results', query: value, items })
    } catch (error) {
      setState({ status: 'failed', message: error instanceof Error ? error.message : 'The search failed.' })
    }
  }

  const closeOnEscape = (event: KeyboardEvent) => {
    if (event.key === 'Escape' && state.status !== 'idle') {
      event.preventDefault()
      setState({ status: 'idle' })
    }
  }

  return (
    <form className="purchase-search" role="search" onSubmit={event => void submit(event)} onKeyDown={closeOnEscape}>
      <label className="visually-hidden" htmlFor={`${id}-query`}>Search purchases</label>
      <input id={`${id}-query`} type="search" value={query} maxLength={200} autoComplete="off"
        placeholder="Transaction, player or card" onChange={event => setQuery(event.target.value)} />
      <p className="visually-hidden" role="status">
        {state.status === 'searching' ? 'Searching…' : state.status === 'results' ? `${state.items.length} purchases found.` : ''}
      </p>
      {state.status === 'failed' && (
        <div className="purchase-search-results"><p className="error-text">{state.message}</p></div>
      )}
      {state.status === 'results' && (
        <div className="purchase-search-results">
          {state.items.length === 0
            ? <p className="muted">No purchase has the transaction ID, player ID or card token “{state.query}”.</p>
            : (
              <ul aria-label={`Purchases matching ${state.query}`}>
                {state.items.map(item => (
                  <li key={item.purchase_id}>
                    <a href={matchAddress(item)}>
                      <span className="purchase-search-title">{item.transaction_id}</span>
                      <span className="muted small">
                        {formatUsdMinor(item.amount_minor)} · matched by {matchedBy[item.matched_by]}
                        {item.review && ` · ${reviewText[item.review]}`}
                      </span>
                    </a>
                    <OutcomeBadge outcome={item.outcome} />
                  </li>
                ))}
              </ul>
            )}
        </div>
      )}
    </form>
  )
}
