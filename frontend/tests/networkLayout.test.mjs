import assert from 'node:assert/strict'
import { test } from 'node:test'
import { layoutNetwork, shortenId, statusRank, summarizeNetwork, VIEW_SIZE } from '../src/reviews/networkLayout.ts'

function players(count, status = 'clear') {
  return Array.from({ length: count }, (_, index) => ({ type: 'player', value: `player-${index}`, purchases: 1, status }))
}

function anchor(type, value, { blocklisted = false, linked = [], total = linked.length } = {}) {
  return { type, value, blocklisted, linked_total: total, linked }
}

const empty = {
  window_days: 30,
  anchors: [anchor('device', 'device-1'), anchor('card', 'card-1'), anchor('player', 'player-1')],
}

test('layout is deterministic and keeps every node inside the view box', () => {
  const network = {
    window_days: 30,
    anchors: [
      anchor('device', 'device-1', { linked: players(30, 'rejected'), total: 31 }),
      anchor('card', 'card-1', { linked: players(5) }),
      anchor('player', 'player-1', { linked: [{ type: 'card', value: 'card-2', purchases: 2, status: 'in_review' }] }),
    ],
  }
  const layout = layoutNetwork(network)
  assert.deepEqual(layoutNetwork(network), layout)
  const points = layout.anchors.flatMap(placed => [placed, ...placed.nodes, ...placed.nodes.map(node => node.label)])
  for (const point of points) {
    assert.ok(point.x >= 0 && point.x <= VIEW_SIZE, `x ${point.x}`)
    assert.ok(point.y >= 0 && point.y <= VIEW_SIZE, `y ${point.y}`)
  }
})

test('anchors run clockwise from the top in device, card, player order', () => {
  const [device, card, player] = layoutNetwork(empty).anchors
  assert.deepEqual([device.anchor.type, card.anchor.type, player.anchor.type], ['device', 'card', 'player'])
  assert.deepEqual({ x: device.x, y: device.y }, { x: 320, y: 200 })
  assert.deepEqual({ x: card.x, y: card.y }, { x: 423.92, y: 380 })
  assert.deepEqual({ x: player.x, y: player.y }, { x: 216.08, y: 380 })
})

test('a long list draws eight nodes and folds the rest into a more node', () => {
  const network = { window_days: 30, anchors: [anchor('device', 'device-1', { linked: players(30), total: 31 }), anchor('card', 'card-1')] }
  const nodes = layoutNetwork(network).anchors[0].nodes
  assert.equal(nodes.length, 9)
  assert.equal(nodes.filter(node => node.kind === 'entity').length, 8)
  assert.equal(nodes.at(-1).kind, 'more')
  assert.equal(nodes.at(-1).count, 23)
})

test('a short list has no more node', () => {
  const network = { window_days: 30, anchors: [anchor('card', 'card-1', { linked: players(3) }), anchor('player', 'player-1')] }
  const nodes = layoutNetwork(network).anchors[0].nodes
  assert.equal(nodes.length, 3)
  assert.ok(nodes.every(node => node.kind === 'entity'))
})

test('without a device the card and player anchors split the ring', () => {
  const network = { window_days: 30, anchors: [anchor('card', 'card-1'), anchor('player', 'player-1')] }
  const [card, player] = layoutNetwork(network).anchors
  assert.deepEqual({ x: card.x, y: card.y }, { x: 320, y: 200 })
  assert.deepEqual({ x: player.x, y: player.y }, { x: 320, y: 440 })
})

test('labels on the left half are turned upright and end at their node', () => {
  const card = { type: 'card', value: 'card-2', purchases: 1, status: 'clear' }
  const network = {
    window_days: 30,
    anchors: [anchor('device', 'device-1'), anchor('card', 'card-1', { linked: players(1) }), anchor('player', 'player-1', { linked: [card] })],
  }
  const [, right, left] = layoutNetwork(network).anchors
  assert.deepEqual([right.nodes[0].label.textAnchor, right.nodes[0].label.rotation], ['start', 30])
  assert.deepEqual([left.nodes[0].label.textAnchor, left.nodes[0].label.rotation], ['end', 330])
})

test('the summary names a blocklisted device with its worst linked players', () => {
  const network = {
    window_days: 30,
    anchors: [
      anchor('device', 'dev-12', { blocklisted: true, linked: [...players(2, 'rejected'), ...players(3)] }),
      anchor('card', 'card-1', { linked: players(9, 'in_review') }),
      anchor('player', 'player-1'),
    ],
  }
  assert.equal(summarizeNetwork(network), 'Device dev-12 (blocklisted): 5 players, 2 rejected.')
})

test('the summary picks the anchor with the worst linked status', () => {
  const network = {
    window_days: 30,
    anchors: [
      anchor('device', 'dev-12', { linked: players(6, 'in_review') }),
      anchor('card', 'card-7', { linked: [...players(1, 'rejected'), ...players(1)] }),
      anchor('player', 'player-1'),
    ],
  }
  assert.equal(summarizeNetwork(network), 'Card card-7: 2 players, 1 rejected.')
})

test('the summary says when more of the worst status may be unlisted', () => {
  const network = { window_days: 30, anchors: [anchor('card', 'card-7', { linked: players(30, 'blocked'), total: 45 }), anchor('player', 'player-1')] }
  assert.equal(summarizeNetwork(network), 'Card card-7: 45 players, at least 30 blocked.')
})

test('the summary for a player anchor counts cards', () => {
  const network = { window_days: 30, anchors: [anchor('card', 'card-1'), anchor('player', 'player-1', { linked: [{ type: 'card', value: 'card-2', purchases: 4, status: 'clear' }] })] }
  assert.equal(summarizeNetwork(network), 'Player player-1: 1 card, none flagged.')
})

test('the summary says so plainly when nothing is linked', () => {
  assert.equal(summarizeNetwork(empty),
    "No other players or cards share this purchase's player, card or device in the 30 days around it.")
  const withoutDevice = { window_days: 30, anchors: [anchor('card', 'card-1'), anchor('player', 'player-1')] }
  assert.equal(summarizeNetwork(withoutDevice),
    "No other players or cards share this purchase's player or card in the 30 days around it.")
})

test('a blocklisted anchor with no links is still named', () => {
  const network = { window_days: 30, anchors: [anchor('card', 'card-1', { blocklisted: true }), anchor('player', 'player-1')] }
  assert.equal(summarizeNetwork(network), 'Card card-1 (blocklisted): no other players share it.')
})

test('statuses rank from worst to clear', () => {
  const ranks = ['blocklisted', 'rejected', 'blocked', 'in_review', 'clear'].map(statusRank)
  assert.deepEqual(ranks, [0, 1, 2, 3, 4])
})

test('long identifiers are shortened in the middle', () => {
  assert.equal(shortenId('device-1'), 'device-1')
  assert.equal(shortenId('demo-device-12'), 'demo-device-12')
  assert.equal(shortenId('sim1-20261111T064452-player-123'), 'sim…player-123')
})
