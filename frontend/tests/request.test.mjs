import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  checkReadiness, claimReviews, getActiveRuleset, getLinkedReviews, getReviewNetwork, listReviews, publishRuleset,
  removeBlocklistEntry, score, submitReviewDecision, submitReviewDecisions,
} from '../src/api.ts'
import { request } from '../src/request.ts'
import { forgetSessionToken, keepSessionToken, onSessionRejected } from '../src/session.ts'

test('preserves score validation errors and sends the request once', async ({ mock }) => {
  const body = { error: 'invalid purchase', fields: { amount_minor: 'must be positive' } }
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json(body, { status: 400 }))
  assert.deepEqual(await score({ amount_minor: 0 }), { ok: false, status: 400, error: body })
  assert.equal(fetch.mock.callCount(), 1)
  assert.equal(new Headers(fetch.mock.calls[0].arguments[1].headers).has('X-API-Key'), false)
})

test('session requests carry the token and a 401 reports the rejected session', async (t) => {
  const { mock } = t
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ error: 'missing or invalid session' }, { status: 401 }))
  let rejections = 0
  t.after(onSessionRejected(() => { rejections++ }))
  t.after(forgetSessionToken)
  keepSessionToken('session-token')

  await assert.rejects(listReviews({ sort: 'model_score', cursor: 'opaque+/=', claimed: 'mine' }, new AbortController().signal), {
    status: 401, message: 'missing or invalid session',
  })
  const [path, options] = fetch.mock.calls[0].arguments
  const query = new URL(path, 'http://localhost').searchParams
  assert.equal(query.get('cursor'), 'opaque+/=')
  assert.equal(query.get('sort'), 'model_score')
  assert.equal(query.get('claimed'), 'mine')
  assert.equal(options.headers.get('Authorization'), 'Bearer session-token')
  assert.equal(options.headers.has('X-API-Key'), false)
  assert.equal(rejections, 1)
})

test('score sends the session token for the console proxy to check', async (t) => {
  const { mock } = t
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ error: 'missing or invalid session' }, { status: 401 }))
  let rejections = 0
  t.after(onSessionRejected(() => { rejections++ }))
  t.after(forgetSessionToken)
  keepSessionToken('session-token')

  const scored = await score({ amount_minor: 1 })
  assert.equal(scored.status, 401)
  const headers = fetch.mock.calls[0].arguments[1].headers
  assert.equal(headers.get('Authorization'), 'Bearer session-token')
  assert.equal(headers.has('X-API-Key'), false)
  assert.equal(rejections, 1)
})

test('sign-in sends no session token and its 401 keeps the session', async (t) => {
  const { mock } = t
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ error: 'invalid key' }, { status: 401 }))
  let rejections = 0
  t.after(onSessionRejected(() => { rejections++ }))
  t.after(forgetSessionToken)
  keepSessionToken('session-token')

  await assert.rejects(request('/auth/login', { method: 'POST', auth: 'none' }), { status: 401, message: 'invalid key' })
  for (const call of fetch.mock.calls) assert.equal(call.arguments[1].headers.has('Authorization'), false)
  assert.equal(rejections, 0)
})

test('a throttled response reports how long to wait', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () => Response.json({ error: 'too many sign-in attempts, try again later' },
    { status: 429, headers: { 'Retry-After': '840' } }))
  await assert.rejects(request('/auth/login', { method: 'POST', auth: 'none' }), { status: 429, retryAfterSeconds: 840 })
})

test('reports proxy, invalid JSON, and network failures distinctly', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async (path) => {
    if (path === '/proxy') return new Response('<h1>Bad gateway</h1>', { status: 502 })
    if (path === '/invalid') return new Response('{')
    throw new TypeError('failed to fetch')
  })
  await assert.rejects(request('/proxy'), { kind: 'http', status: 502, message: /status 502/ })
  await assert.rejects(request('/invalid'), { kind: 'response', status: 200 })
  await assert.rejects(request('/offline'), { kind: 'network' })
})

test('accepts empty delete and readiness responses', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }))
  assert.deepEqual(await removeBlocklistEntry(17), { ok: true, value: null })
  assert.equal(await checkReadiness(), undefined)
})

function stalledBody(signal) {
  return new Response(new ReadableStream({
    start(stream) {
      signal.addEventListener('abort', () => stream.error(signal.reason), { once: true })
    },
  }))
}

test('the deadline covers reading the response body', async ({ mock }) => {
  mock.timers.enable({ apis: ['setTimeout'] })
  mock.method(globalThis, 'fetch', async (_path, { signal }) => stalledBody(signal))
  const failed = assert.rejects(request('/stats'), { kind: 'timeout', status: 200 })
  await Promise.resolve()
  mock.timers.tick(10_000)
  await failed
})

test('caller cancellation stays distinct from timeout and prevents a pre-aborted request', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async (_path, { signal }) => stalledBody(signal))
  const controller = new AbortController()
  const failed = assert.rejects(request('/stats', { signal: controller.signal }), { name: 'AbortError' })
  controller.abort()
  await failed
  await assert.rejects(request('/stats', { signal: controller.signal }), { name: 'AbortError' })
  assert.equal(fetch.mock.callCount(), 1)
})

test('ruleset limits round trip as exact JSON integers', async ({ mock }) => {
  const response = '{"version":1,"review_threshold":1,"block_threshold":2,"rules":{"high_amount":{"enabled":true,"weight":1,"limit":9223372036854775807}}}'
  const fetch = mock.method(globalThis, 'fetch', async () => new Response(response))
  const { version, ...config } = await getActiveRuleset()
  assert.equal(version, 1)
  assert.equal(config.rules.high_amount.limit, 9223372036854775807n)
  assert.equal((await publishRuleset(config)).ok, true)
  assert.match(fetch.mock.calls[1].arguments[1].body, /"limit":9223372036854775807[},]/)
})

const noBlocks = { device: false, card: false, player: false }
const allBlocks = { device: true, card: true, player: true }

test('a review save remains confirmed on 201 and preserves conflict status on 409', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async (path) => path.includes('/saved/')
    ? new Response(null, { status: 201 })
    : Response.json({ error: 'purchase has already been decided' }, { status: 409 }))
  const { signal } = new AbortController()
  assert.deepEqual(await submitReviewDecision('saved', 'reject', { ...noBlocks, device: true }, signal), noBlocks)
  assert.deepEqual(JSON.parse(fetch.mock.calls[0].arguments[1].body), { decision: 'reject', block_device: true })
  await assert.rejects(submitReviewDecision('conflict', 'approve', noBlocks, signal), {
    status: 409, message: 'purchase has already been decided',
  })
  assert.equal(fetch.mock.callCount(), 2)
})

test('a review decision sends only the chosen block fields, and none with an approval', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({}, { status: 201 }))
  const { signal } = new AbortController()
  await submitReviewDecision('1', 'approve', allBlocks, signal)
  await submitReviewDecision('1', 'reject', noBlocks, signal)
  await submitReviewDecision('1', 'reject', { device: false, card: true, player: true }, signal)
  await submitReviewDecision('1', 'reject', allBlocks, signal)
  const bodies = fetch.mock.calls.map(call => JSON.parse(call.arguments[1].body))
  assert.deepEqual(bodies, [
    { decision: 'approve' },
    { decision: 'reject' },
    { decision: 'reject', block_card: true, block_player: true },
    { decision: 'reject', block_device: true, block_card: true, block_player: true },
  ])
})

test('a review decision returns the blocklist entries the response reports as added', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () => Response.json(
    { device_blocked: false, card_blocked: true, player_blocked: true }, { status: 201 }))
  const { signal } = new AbortController()
  assert.deepEqual(await submitReviewDecision('1', 'reject', allBlocks, signal), { device: false, card: true, player: true })
})

test('an unreadable 201 confirms the review decision with no blocklist entries reported', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () => new Response('{', { status: 201 }))
  const { signal } = new AbortController()
  assert.deepEqual(await submitReviewDecision('1', 'reject', allBlocks, signal), noBlocks)
})

test('the review network is read with the session and a 401 reports the rejected session', async (t) => {
  const { mock } = t
  const network = { window_days: 30, anchors: [{ type: 'card', value: 'card-1', blocklisted: false, linked_total: 0, linked: [] }] }
  const fetch = mock.method(globalThis, 'fetch', async () => fetch.mock.callCount() === 0
    ? Response.json(network)
    : Response.json({ error: 'missing or invalid session' }, { status: 401 }))
  let rejections = 0
  t.after(onSessionRejected(() => { rejections++ }))
  t.after(forgetSessionToken)
  keepSessionToken('session-token')
  const { signal } = new AbortController()

  assert.deepEqual(await getReviewNetwork('42', signal), network)
  await assert.rejects(getReviewNetwork('42', signal), { status: 401, message: 'missing or invalid session' })
  const [path, options] = fetch.mock.calls[0].arguments
  assert.equal(path, '/reviews/42/network')
  assert.equal(options.headers.get('Authorization'), 'Bearer session-token')
  assert.equal(options.headers.has('X-API-Key'), false)
  assert.equal(rejections, 1)
})

test('linked reviews are read from the case network path', async ({ mock }) => {
  const list = { window_days: 30, total: 0, items: [] }
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json(list))
  assert.deepEqual(await getLinkedReviews('4/2', new AbortController().signal), list)
  assert.equal(fetch.mock.calls[0].arguments[0], '/reviews/4%2F2/network/reviews')
})

test('a batch claim sends the purchase IDs and returns each result', async ({ mock }) => {
  const results = [
    { purchase_id: '12', status: 'claimed', claimed_by: 'alice', claimed_at: '2026-11-12T12:00:00Z' },
    { purchase_id: '15', status: 'held_by_other', claimed_by: 'bob', claimed_at: '2026-11-12T11:00:00Z' },
  ]
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ results }))
  assert.deepEqual(await claimReviews(['12', '15'], new AbortController().signal), results)
  const [path, options] = fetch.mock.calls[0].arguments
  assert.equal(path, '/reviews/claims')
  assert.equal(options.method, 'POST')
  assert.deepEqual(JSON.parse(options.body), { purchase_ids: ['12', '15'] })
})

test('a batch decision sends only the chosen block fields, and none with an approval', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ decision: 'reject', analyst_id: 'alice', results: [] }))
  const { signal } = new AbortController()
  await submitReviewDecisions(['12'], 'approve', allBlocks, signal)
  await submitReviewDecisions(['12', '15'], 'reject', { device: true, card: false, player: false }, signal)
  const calls = fetch.mock.calls.map(call => [call.arguments[0], JSON.parse(call.arguments[1].body)])
  assert.deepEqual(calls, [
    ['/reviews/decisions', { decision: 'approve', purchase_ids: ['12'] }],
    ['/reviews/decisions', { decision: 'reject', block_device: true, purchase_ids: ['12', '15'] }],
  ])
})

test('a batch decision maps each result to its status and added blocklist entries', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () => Response.json({ decision: 'reject', analyst_id: 'alice', results: [
    { purchase_id: '12', status: 'saved', resolved_at: '2026-11-12T12:00:00Z', device_blocked: true, card_blocked: false, player_blocked: true },
    { purchase_id: '15', status: 'already_decided', resolved_at: null, device_blocked: false, card_blocked: false, player_blocked: false },
  ] }))
  const outcomes = await submitReviewDecisions(['12', '15'], 'reject', allBlocks, new AbortController().signal)
  assert.deepEqual(outcomes, [
    { purchase_id: '12', status: 'saved', blocked: { device: true, card: false, player: true } },
    { purchase_id: '15', status: 'already_decided', blocked: noBlocks },
  ])
})

test('a rejected batch decision keeps its status', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () => Response.json({ error: 'purchase_ids must list 1 to 50 IDs' }, { status: 400 }))
  await assert.rejects(submitReviewDecisions([], 'approve', noBlocks, new AbortController().signal), {
    kind: 'http', status: 400, message: 'purchase_ids must list 1 to 50 IDs',
  })
})


test('a delayed rejection cannot clear a replacement session', async t => {
  let deliver
  t.mock.method(globalThis, 'fetch', () => new Promise(resolve => { deliver = resolve }))
  let rejections = 0
  t.after(onSessionRejected(() => { rejections++ }))
  t.after(forgetSessionToken)
  keepSessionToken('old-session')
  const pending = request('/auth/me').catch(error => error.status)
  keepSessionToken('new-session')
  deliver(Response.json({ error: 'ended' }, { status: 401 }))
  assert.equal(await pending, 401)
  assert.equal(rejections, 0)
})

test('cancelled requests cannot reject the session', async t => {
  let deliver
  t.mock.method(globalThis, 'fetch', () => new Promise(resolve => { deliver = resolve }))
  let rejections = 0
  t.after(onSessionRejected(() => { rejections++ }))
  const controller = new AbortController()
  const pending = request('/auth/me', { signal: controller.signal }).catch(error => error.name)
  controller.abort()
  deliver(Response.json({ error: 'ended' }, { status: 401 }))
  assert.equal(await pending, 'AbortError')
  assert.equal(rejections, 0)
})

test('a successful response from an old session is discarded', async t => {
  let deliver
  t.mock.method(globalThis, 'fetch', () => new Promise(resolve => { deliver = resolve }))
  t.after(forgetSessionToken)
  keepSessionToken('old-session')
  const pending = request('/auth/me').catch(error => error.name)
  keepSessionToken('new-session')
  deliver(Response.json({ username: 'old-user', role: 'admin' }))
  assert.equal(await pending, 'AbortError')
})
