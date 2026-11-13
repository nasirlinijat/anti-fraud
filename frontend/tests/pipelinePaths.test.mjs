import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pipelineFlow } from '../src/pages/pipelineFlow.ts'
import { MAX_PATH_WIDTH, MIN_PATH_WIDTH, pathWidth, pipelinePaths, shareOfEntered } from '../src/pages/pipelinePaths.ts'

function flow() {
  return pipelineFlow({
    outcomes: { allow: 800, review: 120, block: 80, total: 1000 },
    model: { scored: 900, flagged: 20, cleared: 10 },
    blocklist_blocks: 30,
  })
}

test('a path width is linear in its share of the purchases entering', () => {
  assert.equal(pathWidth(1000, 1000), MAX_PATH_WIDTH)
  assert.equal(pathWidth(500, 1000), MAX_PATH_WIDTH / 2)
  assert.equal(pathWidth(250, 1000), MAX_PATH_WIDTH / 4)
})

test('a path with a few purchases keeps the minimum width', () => {
  assert.equal(pathWidth(3, 7396), MIN_PATH_WIDTH)
  assert.equal(pathWidth(1, 1_000_000), MIN_PATH_WIDTH)
})

test('a path without purchases has no width', () => {
  assert.equal(pathWidth(0, 1000), 0)
  assert.equal(pathWidth(0, 0), 0)
})

test('a path never grows wider than the purchases entering', () => {
  assert.equal(pathWidth(1200, 1000), MAX_PATH_WIDTH)
})

test('a share has one decimal of the purchases entering', () => {
  assert.equal(shareOfEntered(119, 7396), '1.6%')
  assert.equal(shareOfEntered(6612, 7396), '89.4%')
  assert.equal(shareOfEntered(1000, 1000), '100.0%')
  assert.equal(shareOfEntered(1, 2000), '0.1%')
})

test('a share below a twentieth of a percent shows as less than 0.1%', () => {
  assert.equal(shareOfEntered(1, 10_000), '<0.1%')
  assert.equal(shareOfEntered(4, 10_000), '<0.1%')
})

test('a share of no purchases is 0% and a share without purchases entering is empty', () => {
  assert.equal(shareOfEntered(0, 1000), '0%')
  assert.equal(shareOfEntered(0, 0), '')
})

test('the main chain gets thinner as purchases leave it', () => {
  const paths = pipelinePaths(flow())
  assert.equal(paths['chain-blocklist'].count, 1000)
  assert.equal(paths['chain-rules'].count, 970)
  assert.equal(paths['chain-model'].count, 920)
  assert.ok(paths['chain-blocklist'].width > paths['chain-rules'].width)
  assert.ok(paths['chain-rules'].width > paths['chain-model'].width)
})

test('every path that ends in an outcome carries that outcome', () => {
  const paths = pipelinePaths(flow())
  assert.deepEqual(
    Object.values(paths).map(path => [path.id, path.outcome, path.count, path.share]),
    [
      ['chain-blocklist', null, 1000, '100.0%'],
      ['chain-rules', null, 970, '97.0%'],
      ['chain-model', null, 920, '92.0%'],
      ['blocklist-block', 'block', 30, '3.0%'],
      ['rules-block', 'block', 50, '5.0%'],
      ['model-review', 'review', 120, '12.0%'],
      ['model-allow', 'allow', 800, '80.0%'],
    ],
  )
})

test('only the blocklist and the rules have a path to block', () => {
  const paths = pipelinePaths(flow())
  const toBlock = Object.values(paths).filter(path => path.outcome === 'block').map(path => path.id)
  assert.deepEqual(toBlock, ['blocklist-block', 'rules-block'])
  assert.ok(Object.values(paths).filter(path => path.id.startsWith('model')).every(path => path.outcome !== 'block'))
})
