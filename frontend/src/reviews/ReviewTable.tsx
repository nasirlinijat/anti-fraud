import type { KeyboardEvent } from 'react'
import type { Review, ReviewSort } from '../api'
import { ModelRiskBadge, RuleScoreBadge, Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import type { TimeDisplay } from '../useTimeDisplay'
import { ClaimBadge, ReviewActions, type ReviewHandlers } from './ReviewActions'

interface ReviewTableProps {
  items: Review[]
  time: TimeDisplay
  sort: ReviewSort
  modelEnabled: boolean
  handlers: ReviewHandlers
  onOpen: (purchaseId: string) => void
}

// The arrow keys scroll the table sideways when the region itself has focus.
function scrollTable(event: KeyboardEvent<HTMLDivElement>) {
  const region = event.currentTarget
  if (event.target !== region || event.altKey || event.metaKey || event.ctrlKey || event.shiftKey) return
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  if (region.scrollWidth <= region.clientWidth) return

  event.preventDefault()
  region.scrollBy({ left: event.key === 'ArrowLeft' ? -80 : 80 })
}

// On narrow screens each row is shown as a card. The explicit roles keep the
// table structure for assistive technology when CSS changes the display.
export function ReviewTable({ items, time, sort, modelEnabled, handlers, onOpen }: ReviewTableProps) {
  return (
    <div className="table-scroll review-table-scroll" role="region" aria-labelledby="review-caption" tabIndex={0}
      onKeyDown={scrollTable}>
      <table className="review-table stack-table" role="table">
        <caption id="review-caption" className="visually-hidden">Purchases awaiting review</caption>
        <thead role="rowgroup">
          <tr role="row">
            <th role="columnheader" scope="col">Transaction</th>
            <th role="columnheader" scope="col" className="num">Amount (USD)</th>
            <th role="columnheader" scope="col">Queued</th>
            <th role="columnheader" scope="col" aria-sort={sort === 'rule_score' ? 'descending' : undefined}>Rule score</th>
            {modelEnabled && (
              <th role="columnheader" scope="col" aria-sort={sort === 'model_score' ? 'descending' : undefined}>Model risk</th>
            )}
            <th role="columnheader" scope="col">Fired rules</th>
            <th role="columnheader" scope="col">Claim</th>
            <th role="columnheader" scope="col">Actions</th>
          </tr>
        </thead>
        <tbody role="rowgroup">
          {items.map(item => (
            <ReviewRow key={item.purchase_id} item={item} time={time} modelEnabled={modelEnabled}
              handlers={handlers} onOpen={onOpen} />
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ReviewRow({ item, time, modelEnabled, handlers, onOpen }: Omit<ReviewTableProps, 'items' | 'sort'> & { item: Review }) {
  return (
    <tr role="row" data-purchase-id={item.purchase_id}>
      <th role="rowheader" scope="row">
        <button type="button" className="btn-link review-open" onClick={() => onOpen(item.purchase_id)}>
          {item.transaction_id}
        </button>
        <span className="cell-note">Player {item.player_id}</span>
      </th>
      <td role="cell" data-label="Amount (USD)" className="num">{formatUsdMinor(item.amount_minor)}</td>
      <td role="cell" data-label="Queued"><Timestamp value={item.queued_at} display={time} /></td>
      <td role="cell" data-label="Rule score"><RuleScoreBadge value={item.rule_score} /></td>
      {modelEnabled && <td role="cell" data-label="Model risk"><ModelRiskBadge value={item.model_score} /></td>}
      <td role="cell" data-label="Fired rules">
        {item.fired_rules.length === 0 ? 'None' : (
          <ul className="rule-list">
            {item.fired_rules.map(rule => <li key={rule.name}>{rule.message} ({rule.points} points)</li>)}
          </ul>
        )}
      </td>
      <td role="cell" data-label="Claim"><ClaimBadge item={item} username={handlers.username} /></td>
      <td role="cell" data-label="Actions"><ReviewActions item={item} handlers={handlers} /></td>
    </tr>
  )
}
