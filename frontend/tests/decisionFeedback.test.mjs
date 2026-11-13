import assert from 'node:assert/strict'
import { test } from 'node:test'
import { decisionFeedback } from '../src/reviews/decisionFeedback.ts'

const none = { device: false, card: false, player: false }

test('a decision without blocklist entries names only the decision', () => {
  assert.equal(decisionFeedback('tx-1', 'reject', none), 'Rejected tx-1.')
  assert.equal(decisionFeedback('tx-1', 'approve', none), 'Approved tx-1.')
})

test('a rejection names one blocked value', () => {
  assert.equal(decisionFeedback('tx-1', 'reject', { ...none, device: true }), 'Rejected tx-1 and blocked its device.')
  assert.equal(decisionFeedback('tx-1', 'reject', { ...none, player: true }), 'Rejected tx-1 and blocked its player.')
})

test('a rejection names two blocked values in the order device, card, player', () => {
  assert.equal(decisionFeedback('tx-1', 'reject', { device: true, card: true, player: false }),
    'Rejected tx-1 and blocked its device and card.')
  assert.equal(decisionFeedback('tx-1', 'reject', { device: false, card: true, player: true }),
    'Rejected tx-1 and blocked its card and player.')
})

test('a rejection names three blocked values as a list', () => {
  assert.equal(decisionFeedback('tx-1', 'reject', { device: true, card: true, player: true }),
    'Rejected tx-1 and blocked its device, card and player.')
})
