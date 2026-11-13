import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  isStatusShown, listedPurchasesNote, reviewText, sameSelection, selectionKey, toggleSelection, toggleStatus, visibleLinked,
} from '../src/reviews/networkSelection.ts'

const player = { type: 'player', value: 'player-7', purchases: 2, status: 'rejected' }

function purchase(fields = {}) {
  return { purchase_id: '1', transaction_id: 'tx-1', event_time: '2026-11-01T10:00:00Z', amount_minor: '1999', outcome: 'allow',
    review_decision: null, claimed_by: null, awaiting_review: false, ...fields }
}

test('a selection is identified by its anchor, linked type and value', () => {
  assert.equal(selectionKey({ anchor: 'device', entity: player }), 'device:player:player-7')
  assert.ok(sameSelection({ anchor: 'device', entity: player }, { anchor: 'device', entity: { ...player, purchases: 9 } }))
})

test('the same value under two anchors is two selections', () => {
  assert.equal(sameSelection({ anchor: 'device', entity: player }, { anchor: 'card', entity: player }), false)
})

test('no selection matches only no selection', () => {
  assert.equal(sameSelection(null, null), true)
  assert.equal(sameSelection(null, { anchor: 'card', entity: player }), false)
})

test('choosing the selected value again clears the selection', () => {
  const selection = { anchor: 'card', entity: player }
  assert.equal(toggleSelection(selection, { anchor: 'card', entity: player }), null)
  assert.deepEqual(toggleSelection(null, selection), selection)
  assert.deepEqual(toggleSelection({ anchor: 'device', entity: player }, selection), selection)
})

test('an empty status filter shows every status', () => {
  assert.equal(isStatusShown('clear', new Set()), true)
  assert.equal(isStatusShown('blocklisted', new Set()), true)
})

test('a status filter shows only the chosen statuses', () => {
  const filter = new Set(['rejected', 'blocked'])
  assert.equal(isStatusShown('rejected', filter), true)
  assert.equal(isStatusShown('clear', filter), false)
})

test('toggling a status adds or removes it without changing the original filter', () => {
  const original = new Set(['rejected'])
  assert.deepEqual([...toggleStatus(original, 'clear')], ['rejected', 'clear'])
  assert.deepEqual([...toggleStatus(original, 'rejected')], [])
  assert.deepEqual([...original], ['rejected'])
})

test("an anchor's rows keep only the shown statuses in their order", () => {
  const linked = [player, { ...player, value: 'player-8', status: 'clear' }, { ...player, value: 'player-9', status: 'rejected' }]
  const anchor = { type: 'device', value: 'device-1', blocklisted: false, linked_total: 3, linked }
  assert.deepEqual(visibleLinked(anchor, new Set(['rejected'])).map(entity => entity.value), ['player-7', 'player-9'])
  assert.equal(visibleLinked(anchor, new Set()).length, 3)
  assert.deepEqual(visibleLinked(anchor, new Set(['blocked'])), [])
})

test('the review column names the decision or the open review', () => {
  assert.equal(reviewText(purchase({ outcome: 'review', review_decision: 'approve' })), 'Approved')
  assert.equal(reviewText(purchase({ outcome: 'review', review_decision: 'reject' })), 'Rejected')
  assert.equal(reviewText(purchase({ outcome: 'review', awaiting_review: true })), 'Awaiting review')
  assert.equal(reviewText(purchase({ outcome: 'review', awaiting_review: true, claimed_by: 'alice' })), 'Awaiting review, claimed by alice')
})

test('a purchase never sent to review shows a dash', () => {
  assert.equal(reviewText(purchase({ outcome: 'block' })), '—')
})

test('the note appears only when more purchases exist than are listed', () => {
  assert.equal(listedPurchasesNote(3, 3), null)
  assert.equal(listedPurchasesNote(50, 1234), 'Showing the newest 50 of 1,234 purchases.')
})
