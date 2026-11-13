import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  claimableItems, decidableSelection, listedReviewsNote, pruneSelection, selectAllMine, selectAllState, toggleSelected,
} from '../src/reviews/linkedSelection.ts'

function review(id, claimedBy = null) {
  return { purchase_id: id, transaction_id: `tx-${id}`, claimed_by: claimedBy }
}

const items = [review('1'), review('2', 'alice'), review('3', 'alice'), review('4', 'bob')]
const none = new Set()

test('pruning drops IDs that left the list or are no longer claimed by the account', () => {
  const pruned = pruneSelection(new Set(['2', '3', '4', '9']), items, 'alice')
  assert.deepEqual([...pruned], ['2', '3'])
})

test('pruning returns the same selection when nothing is dropped', () => {
  const selection = new Set(['2'])
  assert.equal(pruneSelection(selection, items, 'alice'), selection)
})

test('toggling adds and removes one ID', () => {
  const added = toggleSelected(new Set(['2']), '3')
  assert.deepEqual([...added], ['2', '3'])
  assert.deepEqual([...toggleSelected(added, '2')], ['3'])
})

test('select all chooses only the purchases claimed by the account', () => {
  assert.deepEqual([...selectAllMine(items, 'alice')], ['2', '3'])
})

test('the select-all state counts only selectable purchases', () => {
  assert.equal(selectAllState(none, items, 'alice'), 'none')
  assert.equal(selectAllState(new Set(['2']), items, 'alice'), 'some')
  assert.equal(selectAllState(new Set(['2', '3']), items, 'alice'), 'all')
})

test('a bulk decision sends the selected own purchases in list order and skips busy ones', () => {
  const selection = new Set(['3', '2', '4'])
  assert.deepEqual(decidableSelection(selection, items, 'alice', none).map(item => item.purchase_id), ['2', '3'])
  assert.deepEqual(decidableSelection(selection, items, 'alice', new Set(['3'])).map(item => item.purchase_id), ['2'])
})

test('claim all takes every unclaimed purchase that has no request in flight', () => {
  const list = [...items, review('5')]
  assert.deepEqual(claimableItems(list, none).map(item => item.purchase_id), ['1', '5'])
  assert.deepEqual(claimableItems(list, new Set(['5'])).map(item => item.purchase_id), ['1'])
})

test('the note appears only when more purchases match than are listed', () => {
  assert.equal(listedReviewsNote(3, 3), null)
  assert.equal(listedReviewsNote(50, 1234), 'Showing the 50 highest rule scores of 1,234.')
})
