import { useState, type FormEvent } from 'react'
import { entryTypes, type BlocklistFilters as Filters, type ErrorResponse } from '../api'
import { Button } from '../ui/Button'
import { typeNames } from './entryTypes'

export function BlocklistFilters({ error, onApply }: {
  error: ErrorResponse | null
  onApply: (filters: Filters) => void
}) {
  const [draft, setDraft] = useState<Filters>({ type: '', q: '' })
  const [edited, setEdited] = useState(false)
  const queryError = edited ? undefined : error?.fields?.q
  const typeError = edited ? undefined : error?.fields?.type

  function search(event: FormEvent) {
    event.preventDefault()
    setEdited(false)
    onApply(draft)
  }

  function clear() {
    setDraft({ type: '', q: '' })
    setEdited(false)
    onApply({ type: '', q: '' })
  }

  return (
    <search aria-label="Blocklist search">
      <form className="blocklist-filters" aria-label="Filter blocklist entries" onSubmit={search}>
        <div className={typeError ? 'field invalid' : 'field'}>
          <label htmlFor="blocklist-filter-type">Filter by type</label>
          <select id="blocklist-filter-type" name="type" value={draft.type}
            aria-invalid={typeError ? true : undefined} aria-describedby={typeError ? 'blocklist-type-error' : undefined}
            onChange={event => { setDraft({ ...draft, type: event.target.value as Filters['type'] }); setEdited(true) }}>
            <option value="">All types</option>
            {entryTypes.map(type => <option key={type} value={type}>{typeNames[type]}</option>)}
          </select>
          {typeError && <span className="error-text" id="blocklist-type-error">{typeError}</span>}
        </div>
        <div className={queryError ? 'field invalid' : 'field'}>
          <label htmlFor="blocklist-query">Search values and reasons</label>
          <input id="blocklist-query" name="q" type="search" value={draft.q} autoCapitalize="none" autoCorrect="off"
            spellCheck={false} enterKeyHint="search" aria-invalid={queryError ? true : undefined}
            aria-describedby={`blocklist-query-hint${queryError ? ' blocklist-query-error' : ''}`}
            onChange={event => { setDraft({ ...draft, q: event.target.value }); setEdited(true) }} />
          <span className="muted small" id="blocklist-query-hint">Contains text, ignoring case. Up to 200 characters; symbols match literally.</span>
          {queryError && <span className="error-text" id="blocklist-query-error">{queryError}</span>}
        </div>
        <div className="blocklist-filter-actions">
          <Button type="submit">Search</Button>
          <Button variant="secondary" onClick={clear}>Clear filters</Button>
        </div>
      </form>
    </search>
  )
}
