import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pipelineFlow } from '../src/pages/pipelineFlow.ts'

function stats({ allow, review, block, blocklist, scored = 0, flagged = 0, cleared = 0 }) {
  return {
    outcomes: { allow, review, block, total: allow + review + block },
    model: { scored, flagged, cleared },
    blocklist_blocks: blocklist,
  }
}

test('every stage count follows from the outcomes, the blocklist blocks and the model effects', () => {
  const flow = pipelineFlow(stats({ allow: 800, review: 120, block: 80, blocklist: 30, scored: 900, flagged: 20, cleared: 10 }))
  assert.deepEqual(flow, {
    entered: 1000,
    passedBlocklist: 970,
    blocklistBlocks: 30,
    rulesBlocks: 50,
    rulesReview: 110,
    rulesAllow: 810,
    modelCleared: 10,
    modelEscalated: 20,
    allow: 800,
    review: 120,
    block: 80,
  })
})

test('the purchases leaving the stages add up to the purchases that entered', () => {
  const flow = pipelineFlow(stats({ allow: 800, review: 120, block: 80, blocklist: 30, scored: 900, flagged: 20, cleared: 10 }))
  assert.equal(flow.blocklistBlocks + flow.passedBlocklist, flow.entered)
  assert.equal(flow.rulesBlocks + flow.rulesReview + flow.rulesAllow, flow.passedBlocklist)
  assert.equal(flow.rulesAllow - flow.modelEscalated + flow.modelCleared, flow.allow)
  assert.equal(flow.rulesReview - flow.modelCleared + flow.modelEscalated, flow.review)
})

test('a period without decisions has every count at zero', () => {
  const flow = pipelineFlow(stats({ allow: 0, review: 0, block: 0, blocklist: 0 }))
  assert.ok(Object.values(flow).every(count => count === 0))
})

test('without a model effect the rules decide allow and review', () => {
  const flow = pipelineFlow(stats({ allow: 70, review: 20, block: 10, blocklist: 4 }))
  assert.equal(flow.rulesAllow, 70)
  assert.equal(flow.rulesReview, 20)
  assert.equal(flow.rulesBlocks, 6)
  assert.equal(flow.modelCleared, 0)
  assert.equal(flow.modelEscalated, 0)
})

test('a derived count that would be negative is shown as zero', () => {
  const flow = pipelineFlow(stats({ allow: 9, review: 5, block: 2, blocklist: 3, scored: 14, flagged: 8, cleared: 9 }))
  assert.equal(flow.rulesBlocks, 0)
  assert.equal(flow.rulesAllow, 8)
  const lowReview = pipelineFlow(stats({ allow: 9, review: 5, block: 2, blocklist: 2, scored: 14, flagged: 8, cleared: 0 }))
  assert.equal(lowReview.rulesReview, 0)
  const overBlocked = pipelineFlow(stats({ allow: 0, review: 0, block: 1, blocklist: 3 }))
  assert.equal(overBlocked.passedBlocklist, 0)
})

test('the model has no path to block', () => {
  const flow = pipelineFlow(stats({ allow: 800, review: 120, block: 80, blocklist: 30, scored: 900, flagged: 20, cleared: 10 }))
  const modelKeys = Object.keys(flow).filter(key => key.startsWith('model'))
  assert.deepEqual(modelKeys, ['modelCleared', 'modelEscalated'])
  assert.equal(flow.rulesBlocks + flow.blocklistBlocks, flow.block)
})
