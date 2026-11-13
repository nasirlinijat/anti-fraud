import type { Review } from '../api.ts'

// The purchase IDs chosen for a bulk decision.
export type Selection = ReadonlySet<string>

export type SelectAllState = 'none' | 'some' | 'all'

// A session can decide only the purchases it has claimed, so only those can be selected.
export function isSelectable(item: Review, username: string): boolean {
  return item.claimed_by === username
}

// Keeps only the IDs still listed and still claimed by the account. The same
// set comes back when nothing was dropped, so an unchanged list does not re-render.
export function pruneSelection(selection: Selection, items: Review[], username: string): Selection {
  const selectable = new Set(items.filter(item => isSelectable(item, username)).map(item => item.purchase_id))
  const kept = [...selection].filter(id => selectable.has(id))
  return kept.length === selection.size ? selection : new Set(kept)
}

export function toggleSelected(selection: Selection, purchaseId: string): Selection {
  const next = new Set(selection)
  if (next.has(purchaseId)) next.delete(purchaseId)
  else next.add(purchaseId)
  return next
}

export function selectAllMine(items: Review[], username: string): Selection {
  return new Set(items.filter(item => isSelectable(item, username)).map(item => item.purchase_id))
}

export function selectAllState(selection: Selection, items: Review[], username: string): SelectAllState {
  const mine = items.filter(item => isSelectable(item, username))
  const chosen = mine.filter(item => selection.has(item.purchase_id)).length
  if (chosen === 0) return 'none'
  return chosen === mine.length ? 'all' : 'some'
}

// The selected purchases a bulk decision would send, in list order. A purchase
// with a request in flight is left out, so a batch never repeats one.
export function decidableSelection(selection: Selection, items: Review[], username: string,
  busy: ReadonlySet<string>): Review[] {
  return items.filter(item => selection.has(item.purchase_id) && isSelectable(item, username) && !busy.has(item.purchase_id))
}

export function claimableItems(items: Review[], busy: ReadonlySet<string>): Review[] {
  return items.filter(item => item.claimed_by === null && !busy.has(item.purchase_id))
}

const number = new Intl.NumberFormat('en-US')

export function listedReviewsNote(listed: number, total: number): string | null {
  if (total <= listed) return null
  return `Showing the ${number.format(listed)} highest rule scores of ${number.format(total)}.`
}
