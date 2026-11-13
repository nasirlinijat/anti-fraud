import type { LinkedEntity, LinkedPurchase, NetworkAnchor, NetworkEntityType, NetworkStatus } from '../api.ts'

// The same player can be linked through both the device and the card, so a
// selection is identified by the anchor it hangs off as well as by its value.
export interface NetworkSelection {
  anchor: NetworkEntityType
  entity: LinkedEntity
}

export type StatusFilter = ReadonlySet<NetworkStatus>

// What the diagram and the table share: the current choices and the ways to
// change them. The origin is the control that made the selection, so focus can
// return to it when the details close.
export interface NetworkInteraction {
  selection: NetworkSelection | null
  statuses: StatusFilter
  onSelect: (selection: NetworkSelection, origin: HTMLElement | SVGElement) => void
  onShowMore: (anchor: NetworkEntityType) => void
}

export function selectionKey({ anchor, entity }: NetworkSelection): string {
  return `${anchor}:${entity.type}:${entity.value}`
}

export function sameSelection(a: NetworkSelection | null, b: NetworkSelection | null): boolean {
  if (a === null || b === null) return a === b
  return selectionKey(a) === selectionKey(b)
}

// Choosing the selected value again clears the selection.
export function toggleSelection(current: NetworkSelection | null, next: NetworkSelection): NetworkSelection | null {
  return sameSelection(current, next) ? null : next
}

// An empty filter shows every status.
export function isStatusShown(status: NetworkStatus, filter: StatusFilter): boolean {
  return filter.size === 0 || filter.has(status)
}

export function toggleStatus(filter: StatusFilter, status: NetworkStatus): StatusFilter {
  const next = new Set(filter)
  if (next.has(status)) next.delete(status)
  else next.add(status)
  return next
}

export function visibleLinked(anchor: NetworkAnchor, filter: StatusFilter): LinkedEntity[] {
  return anchor.linked.filter(entity => isStatusShown(entity.status, filter))
}

export function reviewText(purchase: LinkedPurchase): string {
  if (purchase.review_decision === 'approve') return 'Approved'
  if (purchase.review_decision === 'reject') return 'Rejected'
  if (!purchase.awaiting_review) return '—'
  return purchase.claimed_by === null ? 'Awaiting review' : `Awaiting review, claimed by ${purchase.claimed_by}`
}

const number = new Intl.NumberFormat('en-US')

export function listedPurchasesNote(listed: number, total: number): string | null {
  if (total <= listed) return null
  return `Showing the newest ${number.format(listed)} of ${number.format(total)} purchases.`
}
