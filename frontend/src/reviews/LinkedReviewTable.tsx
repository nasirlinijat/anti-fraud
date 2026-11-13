import type { Decision, LinkedReviews, Review } from '../api'
import { RuleScoreBadge } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { Button } from '../ui/Button'
import {
  claimableItems, decidableSelection, isSelectable, listedReviewsNote, selectAllMine, selectAllState, toggleSelected,
  type Selection,
} from './linkedSelection'
import { ClaimBadge, ReviewActions, type ReviewHandlers } from './ReviewActions'
import type { LinkedReviewActions } from './useReviewWork'

interface LinkedReviewTableProps {
  list: LinkedReviews
  selection: Selection
  handlers: ReviewHandlers
  actions: LinkedReviewActions
  onSelectionChange: (selection: Selection) => void
  onOpen: (purchaseId: string) => void
}

export function LinkedReviewTable({ list, selection, handlers, actions, onSelectionChange, onOpen }: LinkedReviewTableProps) {
  const note = listedReviewsNote(list.items.length, list.total)
  return (
    <>
      <BulkControls list={list} selection={selection} handlers={handlers} actions={actions} onSelectionChange={onSelectionChange} />
      {note && <p className="muted small">{note}</p>}
      <div className="table-scroll" role="region" tabIndex={0} aria-labelledby="linked-reviews-caption">
        <table className="stack-table linked-review-table" role="table">
          <caption id="linked-reviews-caption" className="visually-hidden">Linked purchases awaiting review, highest rule score first</caption>
          <thead role="rowgroup">
            <tr role="row">
              <th role="columnheader" scope="col"><span className="visually-hidden">Select</span></th>
              <th role="columnheader" scope="col">Transaction</th>
              <th role="columnheader" scope="col" className="num">Amount (USD)</th>
              <th role="columnheader" scope="col">Rule score</th>
              <th role="columnheader" scope="col">Claim</th>
              <th role="columnheader" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody role="rowgroup">
            {list.items.map(item => (
              <LinkedReviewRow key={item.purchase_id} item={item} handlers={handlers} selected={selection.has(item.purchase_id)}
                onToggle={() => onSelectionChange(toggleSelected(selection, item.purchase_id))} onOpen={onOpen} />
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

function LinkedReviewRow({ item, handlers, selected, onToggle, onOpen }: {
  item: Review
  handlers: ReviewHandlers
  selected: boolean
  onToggle: () => void
  onOpen: (purchaseId: string) => void
}) {
  const busy = handlers.busy.has(item.purchase_id)
  return (
    <tr role="row" data-purchase-id={item.purchase_id} className={selected ? 'is-selected' : undefined}>
      <td role="cell" className="cell-select">
        {isSelectable(item, handlers.username) && (
          <input type="checkbox" checked={selected} disabled={busy} aria-label={`Select ${item.transaction_id}`} onChange={onToggle} />
        )}
      </td>
      <th role="rowheader" scope="row">
        <button type="button" className="btn-link review-open" onClick={() => onOpen(item.purchase_id)}>{item.transaction_id}</button>
        <span className="cell-note">Player {item.player_id}</span>
      </th>
      <td role="cell" data-label="Amount (USD)" className="num">{formatUsdMinor(item.amount_minor)}</td>
      <td role="cell" data-label="Rule score"><RuleScoreBadge value={item.rule_score} /></td>
      <td role="cell" data-label="Claim"><ClaimBadge item={item} username={handlers.username} /></td>
      <td role="cell" data-label="Actions"><ReviewActions item={item} handlers={handlers} /></td>
    </tr>
  )
}

// The select-all box sits with the bulk buttons rather than in the table
// header, because narrow screens hide the header when rows become cards.
function BulkControls({ list, selection, handlers, actions, onSelectionChange }: Omit<LinkedReviewTableProps, 'onOpen'>) {
  const { items } = list
  const { username, busy } = handlers
  const claimable = claimableItems(items, busy)
  const decidable = decidableSelection(selection, items, username, busy)
  const allState = selectAllState(selection, items, username)
  const decide = (decision: Decision) => { if (decidable.length > 0) actions.onDecideAll(decidable, decision) }
  return (
    <div className="linked-review-bulk" role="group" aria-label="Bulk actions">
      {items.some(item => isSelectable(item, username)) && (
        <label className="check">
          <input type="checkbox" checked={allState === 'all'} ref={box => { if (box) box.indeterminate = allState === 'some' }}
            onChange={() => onSelectionChange(allState === 'all' ? new Set() : selectAllMine(items, username))} />
          <span>Select all claimed by you</span>
        </label>
      )}
      {claimable.length > 0 && (
        <Button variant="secondary" size="sm" onClick={() => actions.onClaimAll(claimable)}>
          Claim all unclaimed ({claimable.length})
        </Button>
      )}
      <Button size="sm" aria-disabled={decidable.length === 0} onClick={() => decide('approve')}>
        Approve selected ({decidable.length})
      </Button>
      <Button size="sm" variant="danger" aria-disabled={decidable.length === 0} onClick={() => decide('reject')}>
        Reject selected ({decidable.length})
      </Button>
    </div>
  )
}
