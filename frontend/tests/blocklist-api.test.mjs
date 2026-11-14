import assert from 'node:assert/strict'
import { test } from 'node:test'
import { listBlocklist } from '../src/api.ts'
import { forgetSessionToken, keepSessionToken } from '../src/session.ts'

test('blocklist reads always request bounded pages and encode literal filters', async (t) => {
  const { mock } = t
  keepSessionToken('blocklist-admin')
  t.after(forgetSessionToken)
  const result = { entries: [], pagination: { page: 2, page_size: 25, total_pages: 2, total_items: 26 } }
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json(result))
  assert.deepEqual(await listBlocklist({ page: 2, page_size: 25, type: 'device', q: 'RING %_\\.* & +' }), result)
  const [path, options] = fetch.mock.calls[0].arguments
  const url = new URL(path, 'http://localhost')
  assert.equal(url.pathname, '/blocklist')
  assert.equal(url.searchParams.get('q'), 'RING %_\\.* & +')
  assert.equal(url.searchParams.get('type'), 'device')
  assert.equal(url.searchParams.get('page'), '2')
  assert.equal(url.searchParams.get('page_size'), '25')
  assert.equal(options.headers.get('Authorization'), 'Bearer blocklist-admin')
  assert.equal(options.headers.has('X-API-Key'), false)
})

test('cleared blocklist filters still use a bounded page', async ({ mock }) => {
  const fetch = mock.method(globalThis, 'fetch', async () => Response.json({ entries: [], pagination: {} }))
  await listBlocklist({ type: '', q: '', page: 1, page_size: 25 })
  assert.equal(fetch.mock.calls[0].arguments[0], '/blocklist?page=1&page_size=25')
})
