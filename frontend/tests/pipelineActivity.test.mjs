import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  MAX_FLOW_SPEED, MIN_FLOW_SPEED, binPulses, easeSpeed, flowDashes, flowOpacity, flowSpeed, liveLabel, recentFlow,
} from '../src/pages/pipelineActivity.ts'

function recent({ allow, review, block, blocklist, flagged = 0, cleared = 0 }) {
  return {
    after: 81200,
    next_after: 81200 + allow + review + block,
    outcomes: { allow, review, block, total: allow + review + block },
    blocklist_blocks: blocklist,
    model: { flagged, cleared },
  }
}

test('recent decisions are split into paths like the dashboard statistics', () => {
  const flow = recentFlow(recent({ allow: 30, review: 3, block: 2, blocklist: 1, flagged: 1, cleared: 2 }))
  assert.deepEqual(flow, {
    decisions: 35,
    paths: {
      'chain-blocklist': 35,
      'chain-rules': 34,
      'chain-model': 33,
      'blocklist-block': 1,
      'rules-block': 1,
      'model-review': 3,
      'model-allow': 30,
    },
    bins: { allow: 30, review: 3, block: 2 },
  })
})

test('a read without new decisions leaves every path at zero', () => {
  const flow = recentFlow(recent({ allow: 0, review: 0, block: 0, blocklist: 0 }))
  assert.equal(flow.decisions, 0)
  assert.ok(Object.values(flow.paths).every(count => count === 0))
})

test('a path without recent decisions is paused', () => {
  assert.equal(flowSpeed(0, 2), 0)
  assert.equal(flowSpeed(-3, 2), 0)
  assert.equal(flowSpeed(5, 0), 0)
})

test('one decision already moves at the minimum speed or faster', () => {
  assert.ok(flowSpeed(1, 2) >= MIN_FLOW_SPEED)
  assert.ok(flowSpeed(1, 2) < 25)
})

test('more decisions always move faster, and one and five hundred a second look different', () => {
  const counts = [1, 2, 10, 100, 1000, 1999]
  const speeds = counts.map(count => flowSpeed(count, 2))
  for (let index = 1; index < speeds.length; index++) assert.ok(speeds[index] > speeds[index - 1])
  assert.ok(flowSpeed(1000, 2) > 4 * flowSpeed(2, 2))
})

test('very high traffic is clamped to the maximum speed', () => {
  assert.equal(flowSpeed(2000, 2), MAX_FLOW_SPEED)
  assert.equal(flowSpeed(5000, 2), MAX_FLOW_SPEED)
  assert.equal(flowSpeed(1_000_000, 2), MAX_FLOW_SPEED)
})

test('the speed eases towards a new target without jumping', () => {
  const next = easeSpeed(20, 120, 16)
  assert.ok(next > 20 && next < 30)
  assert.ok(Math.abs(easeSpeed(20, 120, 3000) - 120) < 1)
  assert.equal(easeSpeed(50, 50, 16), 50)
})

test('a path whose traffic stopped comes to rest and fades out', () => {
  assert.ok(easeSpeed(100, 0, 16) > 0)
  assert.equal(easeSpeed(0.4, 0, 16), 0)
  assert.equal(flowOpacity(0), 0)
  assert.equal(flowOpacity(MIN_FLOW_SPEED / 2), 0.5)
  assert.equal(flowOpacity(MAX_FLOW_SPEED), 1)
})

test('dots scale with the path and stay visible on a thin one', () => {
  assert.deepEqual(flowDashes(2), { width: 4, pattern: '4 26', thin: true })
  const thick = flowDashes(28)
  assert.equal(thick.thin, false)
  assert.ok(thick.width > 8 && thick.width < 28)
})

test('only bins that received decisions pulse, once for each read', () => {
  assert.deepEqual(binPulses({ allow: 12, review: 0, block: 1 }, 7), { allow: 7, block: 7 })
  assert.deepEqual(binPulses({ allow: 0, review: 0, block: 0 }, 8), {})
})

test('the live note counts the decisions of the last interval', () => {
  assert.equal(liveLabel(null, 2), 'Live')
  assert.equal(liveLabel(0, 2), 'Live · no traffic')
  assert.equal(liveLabel(1, 2), 'Live · 1 decision in the last 2 s')
  assert.equal(liveLabel(1234, 2), 'Live · 1,234 decisions in the last 2 s')
})
