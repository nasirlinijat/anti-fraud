import { useRef } from 'react'
import type { NetworkStatus } from '../api'
import { statusLabels, statusOrder } from './networkLayout'
import type { StatusFilter } from './networkSelection'
import { StatusIcon } from './NetworkStatusMarks'

interface StatusFilterProps {
  statuses: StatusFilter
  onToggle: (status: NetworkStatus) => void
  onShowAll: () => void
}

// The status key doubles as the filter, so the symbol a reader learns is the
// control that narrows the view to it.
export function NetworkStatusFilter({ statuses, onToggle, onShowAll }: StatusFilterProps) {
  const group = useRef<HTMLDivElement>(null)
  // Show all removes itself, so focus moves to the first status instead of being lost.
  function showAll() {
    onShowAll()
    group.current?.querySelector('button')?.focus()
  }
  return (
    <div className="network-legend" role="group" aria-label="Status key. Choose statuses to show only those" ref={group}>
      {statusOrder.map((status) => (
        <button key={status} type="button" className="network-legend-item" aria-pressed={statuses.has(status)}
          onClick={() => onToggle(status)}>
          <StatusIcon status={status} />
          {statusLabels[status]}
        </button>
      ))}
      {statuses.size > 0 && <button type="button" className="btn-link network-show-all" onClick={showAll}>Show all</button>}
    </div>
  )
}
