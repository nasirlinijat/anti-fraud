import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  checkReadiness, getActiveRuleset, listReviews, publishRuleset, removeBlocklistEntry,
  score, submitReviewDecision,
} from '../src/api.ts'
import { request } from '../src/request.ts'

test('preserves score validation errors and sends the request once', async ({ mock }) => {
  const body = { error: 'invalid purchase', fields: { amount_minor: 'must be positive' } }
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json(body, { status: 400 }))
  assert.deepEqual(await score({ amount_minor: 0 }), { ok: false, status: 400, error: body })
  assert.equal(fetch.mock.callCount(), 1)
  assert.equal(new Headers(fetch.mock.calls[0].arguments[1].headers).has('X-API-Key'), false)
})

test('distinguishes service and analyst authentication failures', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ error: 'invalid key' }, { status: 401 }))
  await assert.rejects(request('/stats'), { status: 401, message: /Check API_KEY/ })
  await assert.rejects(listReviews('test-analyst', 'opaque+/=', new AbortController().signal), {
    status: 401, message: 'invalid key',
  })
  const [path, options] = fetch.mock.calls[1].arguments
  assert.equal(new URL(path, 'http://localhost').searchParams.get('cursor'), 'opaque+/=')
  assert.equal(new Headers(options.headers).get('X-API-Key'), 'test-analyst')
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

test('a review save remains confirmed on 201 and preserves conflict status on 409', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async (path) => path.includes('/saved/')
    ? new Response(null, { status: 201 })
    : Response.json({ error: 'purchase has already been decided' }, { status: 409 }))
  const { signal } = new AbortController()
  assert.equal(await submitReviewDecision('test-analyst', 'saved', 'reject', true, signal), false)
  assert.deepEqual(JSON.parse(fetch.mock.calls[0].arguments[1].body), { decision: 'reject', block_device: true })
  await assert.rejects(submitReviewDecision('test-analyst', 'conflict', 'approve', false, signal), {
    status: 409, message: 'purchase has already been decided',
  })
  assert.equal(fetch.mock.callCount(), 2)
})
