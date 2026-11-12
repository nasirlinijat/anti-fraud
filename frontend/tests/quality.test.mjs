import assert from 'node:assert/strict'
import { test } from 'node:test'
import { getQuality, listQualityRuns } from '../src/api.ts'
import { formatQualityRate, qualityCoverage } from '../src/quality.ts'

function report(coverage = {}) {
  return { labelled_decisions: 6, coverage: { pending: 0, quarantined: 0, scored_decisions: null, run_status: null, ...coverage } }
}

test('quality calls preserve source and encode IDs without sending credentials', async ({ mock }) => {
  const calls = []
  mock.method(globalThis, 'fetch', async (path, options) => {
    calls.push([path, options])
    return Response.json({})
  })
  await getQuality('simulator', 'a/b &x')
  await getQuality('ieee-cis', 'a/b &x')
  await listQualityRuns('ieee-cis', 'a/b &x')
  assert.equal(calls[0][0], '/stats/quality?source=simulator&run_id=a%2Fb+%26x')
  assert.equal(calls[1][0], '/stats/quality?source=ieee-cis&run_id=a%2Fb+%26x')
  assert.equal(calls[2][0], '/stats/quality/runs?source=ieee-cis&limit=20&after=a%2Fb+%26x')
  assert.ok(calls.every(([, options]) => options.headers === undefined))
})

test('undefined rates remain distinct from zero percent', () => {
  assert.equal(formatQualityRate(null), 'Not applicable')
  assert.match(formatQualityRate(0), /^0/)
  assert.match(formatQualityRate(2 / 3), /66[.,]67/)
})

test('coverage never asserts external completeness or hides excluded intents', () => {
  assert.match(qualityCoverage(report()), /Total run coverage is unknown/)
  assert.match(qualityCoverage(report({ pending: 2, quarantined: 3 })), /2 pending; 3 quarantined/)
  assert.match(qualityCoverage(report({ scored_decisions: 6, run_status: 'completed' })), /All recorded decisions labelled/)
  for (const coverage of [
    { scored_decisions: 7 }, { scored_decisions: 6, pending: 1 },
    { scored_decisions: 6, quarantined: 1 }, { scored_decisions: 6, run_status: 'running' },
    { scored_decisions: 5 },
  ]) assert.match(qualityCoverage(report(coverage)), /Partial results/)
})
