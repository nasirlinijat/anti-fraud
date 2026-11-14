import { useCallback, useState } from 'react'
import { listBlocklist, type BlocklistFilters, type BlocklistPage, type BlocklistQuery, type ErrorResponse } from '../api'
import { toErrorResponse } from '../request'
import { usePolling } from '../usePolling'

const initialQuery: BlocklistQuery = { type: '', q: '', page: 1, page_size: 25 }

export function useBlocklistPage() {
  const [query, setQuery] = useState(initialQuery)
  const [result, setResult] = useState<BlocklistPage | null>(null)
  const [error, setError] = useState<ErrorResponse | null>(null)
  const load = useCallback((signal: AbortSignal) => listBlocklist(query, signal), [query])
  const { stop, retry, pending } = usePolling(load, {
    onSuccess: (page) => {
      setResult(page)
      setError(null)
      if (page.pagination.page !== query.page) {
        setQuery(current => ({ ...current, page: page.pagination.page }))
      }
    },
    onError: (failure) => setError(toErrorResponse(failure)),
  })

  function change(update: (current: BlocklistQuery) => BlocklistQuery) {
    stop()
    setResult(null)
    setError(null)
    setQuery(update)
  }

  function apply(filters: BlocklistFilters) {
    change(current => ({ ...current, ...filters, q: filters.q.trim(), page: 1 }))
  }

  function goTo(page: number) {
    change(current => ({ ...current, page }))
  }

  function afterAdd() {
    change(current => ({ ...current, page: 1 }))
  }

  function reconcile() {
    change(current => ({ ...current }))
  }

  return { query, result, error, retry, pending, apply, goTo, afterAdd, reconcile }
}
