import type { Decision, Review } from '../api'
import { Button } from '../ui/Button'

export interface ReviewHandlers {
  username: string
  // Purchases with a claim or a decision being saved.
  busy: ReadonlySet<string>
  onClaim: (item: Review) => void
  onDecision: (item: Review, decision: Decision) => void
}

// A purchase is decided only by the account that claimed it, so the actions
// follow the claim: claim it, decide it, or see who holds it.
export function ReviewActions({ item, handlers }: { item: Review; handlers: ReviewHandlers }) {
  const { username, busy, onClaim, onDecision } = handlers
  const waiting = busy.has(item.purchase_id)
  if (item.claimed_by === null) {
    return (
      <div className="review-actions">
        <Button size="sm" aria-disabled={waiting} aria-label={`Claim ${item.transaction_id}`}
          onClick={() => onClaim(item)}>Claim</Button>
      </div>
    )
  }
  if (item.claimed_by !== username) {
    return <span className="visually-hidden">No actions. Claimed by {item.claimed_by}.</span>
  }
  return (
    <div className="review-actions">
      <Button size="sm" aria-disabled={waiting} aria-label={`Approve ${item.transaction_id}`}
        onClick={() => onDecision(item, 'approve')}>Approve</Button>
      <Button size="sm" variant="danger" aria-disabled={waiting} aria-label={`Reject ${item.transaction_id}`}
        onClick={() => onDecision(item, 'reject')}>Reject</Button>
    </div>
  )
}

export function ClaimBadge({ item, username }: { item: Review; username: string }) {
  if (item.claimed_by === null) return <span className="badge">Unclaimed</span>
  if (item.claimed_by === username) return <span className="badge badge-accent">Claimed by you</span>
  return <span className="badge badge-muted">Claimed by {item.claimed_by}</span>
}
