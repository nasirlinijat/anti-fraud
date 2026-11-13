import { useState } from 'react'
import type { LinkedPurchase } from '../api'
import { OutcomeBadge, Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { Button } from '../ui/Button'
import type { TimeDisplay } from '../useTimeDisplay'
import { reviewText } from './networkSelection'
import { ResolvedCaseDialog } from './ResolvedCaseDialog'

interface LinkedPurchaseTableProps {
  purchases: LinkedPurchase[]
  // The selected player or card, such as "Player p-12".
  name: string
  time: TimeDisplay
  onOpen: (purchaseId: string) => void
}

// On narrow screens each row is shown as a card. The explicit roles keep the
// table structure for assistive technology when CSS changes the display.
export function LinkedPurchaseTable({ purchases, name, time, onOpen }: LinkedPurchaseTableProps) {
  const [resolvedId, setResolvedId] = useState<string | null>(null)
  return (
    <>
      <div className="table-scroll" role="region" tabIndex={0} aria-label={`Purchases for ${name}`}>
        <table className="stack-table" role="table">
          <caption className="visually-hidden">Purchases for {name}, newest first</caption>
          <thead role="rowgroup">
            <tr role="row">
              <th role="columnheader" scope="col">Transaction</th>
              <th role="columnheader" scope="col">Purchase time</th>
              <th role="columnheader" scope="col" className="num">Amount (USD)</th>
              <th role="columnheader" scope="col">Outcome</th>
              <th role="columnheader" scope="col">Review</th>
              <th role="columnheader" scope="col"><span className="visually-hidden">Actions</span></th>
            </tr>
          </thead>
          <tbody role="rowgroup">
            {purchases.map(purchase => (
              <tr role="row" key={purchase.purchase_id}>
                <th role="rowheader" scope="row">{purchase.transaction_id}</th>
                <td role="cell" data-label="Purchase time"><Timestamp value={purchase.event_time} display={time} /></td>
                <td role="cell" data-label="Amount (USD)" className="num">{formatUsdMinor(purchase.amount_minor)}</td>
                <td role="cell" data-label="Outcome"><OutcomeBadge outcome={purchase.outcome} /></td>
                <td role="cell" data-label="Review">{reviewText(purchase)}</td>
                <td role="cell" className="cell-action">
                  {purchase.awaiting_review && (
                    <Button variant="secondary" size="sm" aria-label={`Open case ${purchase.transaction_id}`}
                      onClick={() => onOpen(purchase.purchase_id)}>Open case</Button>
                  )}
                  {purchase.review_decision && (
                    <Button variant="secondary" size="sm" aria-label={`View resolved case ${purchase.transaction_id}`}
                      onClick={() => setResolvedId(purchase.purchase_id)}>View case</Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {resolvedId && <ResolvedCaseDialog purchaseId={resolvedId} time={time} onClose={() => setResolvedId(null)} />}
    </>
  )
}
