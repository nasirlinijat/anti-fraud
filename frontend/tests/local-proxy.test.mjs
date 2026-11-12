import assert from 'node:assert/strict'
import { once } from 'node:events'
import { createServer, request } from 'node:http'
import { test } from 'node:test'
import { createServer as createViteServer } from 'vite'

function call(port, path, method, headers) {
  return new Promise((resolve, reject) => {
    const req = request({ host: '127.0.0.1', port, path, method, headers }, res => {
      res.resume()
      res.once('end', () => resolve(res.statusCode))
      res.once('error', reject)
    })
    req.once('error', reject)
    req.end()
  })
}

test('local console proxy checks origin and adds the service key only to score', async t => {
  const forwarded = []
  const upstream = createServer((req, res) => {
    forwarded.push([req.url, req.headers['x-api-key'], req.headers.authorization])
    res.writeHead(204).end()
  })
  upstream.listen(0, '127.0.0.1')
  await once(upstream, 'listening')
  t.after(() => upstream.close())

  const previous = { BACKEND_URL: process.env.BACKEND_URL, API_KEY: process.env.API_KEY }
  process.env.BACKEND_URL = `http://127.0.0.1:${upstream.address().port}`
  process.env.API_KEY = 'proxy-test-service-key'
  t.after(() => {
    for (const [name, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[name]
      else process.env[name] = value
    }
  })

  const vite = await createViteServer({ logLevel: 'silent', server: { host: '127.0.0.1', hmr: false } })
  t.after(() => vite.close())
  await vite.listen(0)
  const { port } = vite.httpServer.address()
  const origin = `http://127.0.0.1:${port}`

  const authorization = 'Bearer session-token'
  assert.equal(await call(port, '/score', 'POST', { origin, 'x-api-key': 'caller-key' }), 204)
  assert.equal(await call(port, '/auth/login', 'POST', { origin }), 204)
  assert.equal(await call(port, '/auth/me', 'GET', { authorization }), 204)
  assert.equal(await call(port, '/reviews?sort=model_score', 'GET', { authorization }), 204)
  assert.equal(await call(port, '/reviews/7/claim', 'POST', { origin, authorization }), 204)
  assert.equal(await call(port, '/rulesets', 'GET', { origin }), 204)
  assert.equal(await call(port, '/blocklist/1', 'DELETE', { origin, authorization }), 204)
  assert.equal(await call(port, '/simulations/run-1/stop', 'POST', { origin, authorization }), 204)
  assert.equal(await call(port, '/stats/quality?source=simulator&run_id=demo', 'GET', { authorization }), 204)
  assert.equal(await call(port, '/admin/accounts', 'GET', { authorization }), 204)
  assert.equal(await call(port, '/rulesets', 'POST', { origin: 'https://example.invalid' }), 403)
  assert.equal(await call(port, '/blocklist/1', 'DELETE', { 'sec-fetch-site': 'cross-site' }), 403)
  assert.equal(await call(port, '/score', 'POST', { host: 'example.invalid' }), 403)
  assert.equal(await call(port, '/auth/login', 'POST', { origin: 'https://example.invalid' }), 403)
  assert.equal(await call(port, '/admin/accounts', 'POST', { 'sec-fetch-site': 'same-site' }), 403)
  assert.equal(await call(port, '/stats/quality?run_id=demo', 'GET', { origin: 'https://example.invalid' }), 403)
  // Only POST /score gets the service key; every other request keeps its own session token.
  assert.deepEqual(forwarded, [
    ['/score', 'proxy-test-service-key', undefined],
    ['/auth/login', undefined, undefined],
    ['/auth/me', undefined, authorization],
    ['/reviews?sort=model_score', undefined, authorization],
    ['/reviews/7/claim', undefined, authorization],
    ['/rulesets', undefined, undefined],
    ['/blocklist/1', undefined, authorization],
    ['/simulations/run-1/stop', undefined, authorization],
    ['/stats/quality?source=simulator&run_id=demo', undefined, authorization],
    ['/admin/accounts', undefined, authorization],
  ])
})
