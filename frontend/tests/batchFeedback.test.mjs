import assert from 'node:assert/strict'
import { test } from 'node:test'
import { claimBatchFeedback, decisionBatchFeedback } from '../src/reviews/batchFeedback.ts'

const items = ['1', '2', '3', '4'].map(id => ({ purchase_id: id, transaction_id: `tx-${id}` }))
const none = { device: false, card: false, player: false }

function claim(id, status, claimedBy = null) {
  return { purchase_id: id, status, claimed_by: claimedBy, claimed_at: null }
}

function decided(id, status, blocked = none) {
  return { purchase_id: id, status, blocked }
}

test('a batch claim where every purchase is claimed names the count', () => {
  const results = [claim('1', 'claimed'), claim('2', 'claimed'), claim('3', 'already_yours'), claim('4', 'claimed')]
  assert.deepEqual(claimBatchFeedback(items, results), { text: 'Claimed 4 purchases.' })
})

test('a partial batch claim names who holds the rest', () => {
  const results = [claim('1', 'claimed'), claim('2', 'claimed'), claim('3', 'claimed'), claim('4', 'held_by_other', 'bob')]
  assert.deepEqual(claimBatchFeedback(items, results), { text: 'Claimed 3 of 4 purchases. tx-4 is claimed by bob.' })
})

test('purchases that share a reason are named together', () => {
  const results = [claim('1', 'held_by_other', 'bob'), claim('2', 'already_decided'), claim('3', 'held_by_other', 'bob'),
    claim('4', 'held_by_other', 'carol')]
  assert.deepEqual(claimBatchFeedback(items, results), {
    text: 'Could not claim 4 purchases. tx-1 and tx-3 are claimed by bob. tx-2 was already decided. tx-4 is claimed by carol.',
    failed: true,
  })
})

test('a batch claim reports missing, finished and failed purchases', () => {
  const results = [claim('1', 'claimed'), claim('2', 'not_found'), claim('3', 'not_review'), claim('4', 'error')]
  assert.deepEqual(claimBatchFeedback(items, results), {
    text: 'Claimed 1 of 4 purchases. tx-2 was not found. tx-3 is no longer under review. tx-4 could not be saved.',
  })
})

test('a single approval uses the singular', () => {
  assert.deepEqual(decisionBatchFeedback('approve', items, [decided('1', 'saved')]), { text: 'Approved 1 purchase.' })
})

test('a batch rejection counts the blocklist entries it added by kind', () => {
  const results = [
    decided('1', 'saved', { device: true, card: true, player: false }),
    decided('2', 'saved', { device: true, card: true, player: false }),
    decided('3', 'saved', { device: false, card: true, player: false }),
  ]
  assert.deepEqual(decisionBatchFeedback('reject', items, results), { text: 'Rejected 3 purchases and blocked 2 devices and 3 cards.' })
})

test('a batch rejection names one blocked value in the singular', () => {
  const results = [decided('1', 'saved', { device: false, card: false, player: true }), decided('2', 'saved')]
  assert.deepEqual(decisionBatchFeedback('reject', items, results), { text: 'Rejected 2 purchases and blocked 1 player.' })
})

test('a partial batch decision explains the purchases it did not save', () => {
  const results = [decided('1', 'saved'), decided('2', 'saved'), decided('3', 'saved'), decided('4', 'already_decided')]
  assert.deepEqual(decisionBatchFeedback('reject', items, results), { text: 'Rejected 3 of 4 purchases. tx-4 was already decided.' })
})

test('a batch decision that saves nothing is marked failed', () => {
  const results = [decided('1', 'not_claimed'), decided('2', 'not_claimed'), decided('3', 'error')]
  assert.deepEqual(decisionBatchFeedback('approve', items, results), {
    text: 'Could not approve 3 purchases. tx-1 and tx-2 are not claimed by you. tx-3 could not be saved.',
    failed: true,
  })
})

test('three purchases with one reason are named as a list', () => {
  const results = [decided('1', 'not_review'), decided('2', 'not_review'), decided('3', 'not_review'), decided('4', 'saved')]
  assert.deepEqual(decisionBatchFeedback('approve', items, results), {
    text: 'Approved 1 of 4 purchases. tx-1, tx-2 and tx-3 are no longer under review.',
  })
})

test('an unknown purchase is named by its purchase ID', () => {
  assert.deepEqual(decisionBatchFeedback('approve', items, [decided('1', 'saved'), decided('99', 'not_found')]), {
    text: 'Approved 1 of 2 purchases. 99 was not found.',
  })
})
