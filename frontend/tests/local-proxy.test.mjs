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

test('local console proxy checks origin before forwarding credentials', async t => {
  const forwarded = []
  const upstream = createServer((req, res) => {
    forwarded.push([req.url, req.headers['x-api-key']])
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

  assert.equal(await call(port, '/score', 'POST', { origin, 'x-api-key': 'caller-key' }), 204)
  assert.equal(await call(port, '/reviews?sort=model_score', 'GET', { 'x-api-key': 'analyst-key' }), 204)
  assert.equal(await call(port, '/reviews', 'GET', {}), 204)
  assert.equal(await call(port, '/rulesets', 'POST', { origin, 'x-api-key': 'operator-key' }), 204)
  assert.equal(await call(port, '/blocklist/1', 'DELETE', { origin }), 204)
  assert.equal(await call(port, '/rulesets', 'GET', { origin }), 204)
  assert.equal(await call(port, '/rulesets', 'POST', { origin: 'https://example.invalid' }), 403)
  assert.equal(await call(port, '/blocklist/1', 'DELETE', { 'sec-fetch-site': 'cross-site' }), 403)
  assert.equal(await call(port, '/score', 'POST', { host: 'example.invalid' }), 403)
  assert.deepEqual(forwarded, [
    ['/score', 'proxy-test-service-key'],
    ['/reviews?sort=model_score', 'analyst-key'],
    ['/reviews', undefined],
    ['/rulesets', 'operator-key'],
    ['/blocklist/1', undefined],
    ['/rulesets', 'proxy-test-service-key'],
  ])
})
