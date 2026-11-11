import { test } from 'node:test'
import assert from 'node:assert/strict'
import { defaultSimulationDraft, validateSimulation, isActive, uncertainStart } from '../src/simulation.ts'
import { ApiError, request } from '../src/request.ts'
import { startSimulation, stopSimulation, listSimulations, getSimulation } from '../src/api.ts'
const available = ['normal', 'card_testing']

test('simulation defaults and mode-specific counts match the API', () => {
  const live = validateSimulation(defaultSimulationDraft, available)
  assert.deepEqual(live.settings, { mode: 'live', seed: 1, rate: 5, count: 0, scenarios: available })
  assert.notEqual(live.settings.scenarios, available)
  assert.deepEqual(
    validateSimulation({ ...defaultSimulationDraft, mode: 'backfill', scenario: 'card_testing' }, available)
      .settings,
    { mode: 'backfill', seed: 1, rate: 5, count: 500, scenarios: ['card_testing'] },
  )
  assert.equal(validateSimulation({ ...defaultSimulationDraft, count: '' }, available).settings.count, 0)
})
test('simulation seed is exact and bounded, rate allows fractions, and count requires an integer', () => {
  for (const seed of ['', '-1', '1.5', '9007199254740992', '1e3', 'NaN'])
    assert.ok(validateSimulation({ ...defaultSimulationDraft, seed }, available).fields.seed, seed)
  for (const seed of ['0', '9007199254740991'])
    assert.equal(
      validateSimulation({ ...defaultSimulationDraft, seed }, available).settings.seed,
      Number(seed),
    )
  for (const rate of ['', '0', '50.1', 'Infinity', 'bad'])
    assert.ok(validateSimulation({ ...defaultSimulationDraft, rate }, available).fields.rate, rate)
  assert.equal(validateSimulation({ ...defaultSimulationDraft, rate: '1.5' }, available).settings.rate, 1.5)
  for (const count of ['', '0', '10001', '1.1'])
    assert.ok(
      validateSimulation({ ...defaultSimulationDraft, mode: 'backfill', count }, available).fields.count,
      count,
    )
  assert.equal(
    validateSimulation({ ...defaultSimulationDraft, mode: 'backfill', count: '10000' }, available).settings
      .count,
    10000,
  )
  assert.ok(validateSimulation(defaultSimulationDraft, []).fields.scenario)
  assert.ok(validateSimulation({ ...defaultSimulationDraft, scenario: 'unknown' }, available).fields.scenario)
})
test('active lifecycle states reserve the start slot', () => {
  for (const status of ['starting', 'running', 'stopping']) assert.equal(isActive(status), true)
  for (const status of ['completed', 'stopped', 'failed', 'interrupted'])
    assert.equal(isActive(status), false)
})
test('only uncertain starts need to retain their original request', () => {
  for (const status of [400, 401, 403, 404, 409])
    assert.equal(uncertainStart(new ApiError('http', 'test', status)), false)
  for (const status of [408, 429, 500, 503])
    assert.equal(uncertainStart(new ApiError('http', 'test', status)), true)
  for (const kind of ['network', 'response', 'timeout'])
    assert.equal(uncertainStart(new ApiError(kind, 'test')), true)
})
test('conflict errors preserve a validated active run ID and field errors', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () =>
    Response.json(
      { error: 'already running', active_run_id: 'sim-current', fields: { count: 'invalid' } },
      { status: 409 },
    ),
  )
  await assert.rejects(request('/simulations'), {
    status: 409,
    activeRunId: 'sim-current',
    fields: { count: 'invalid' },
  })
})
test('non-string conflict metadata is not trusted', async ({ mock }) => {
  mock.method(globalThis, 'fetch', async () =>
    Response.json({ error: 'conflict', active_run_id: { id: 'bad' } }, { status: 409 }),
  )
  await assert.rejects(
    request('/simulations'),
    (error) => error instanceof ApiError && error.activeRunId === undefined,
  )
})
test('start retry uses the same request and accepts 202 and idempotent 200', async ({ mock }) => {
  const settings = {
    ...validateSimulation(defaultSimulationDraft, available).settings,
    request_id: 'stable-id',
  }
  const calls = []
  mock.method(globalThis, 'fetch', async (path, init) => {
    calls.push({ path, body: init.body, key: init.headers['X-API-Key'] })
    if (calls.length === 1) throw new TypeError('connection lost')
    return Response.json({ id: 'sim-saved' }, { status: calls.length === 2 ? 202 : 200 })
  })
  await assert.rejects(startSimulation(settings, 'operator'), { kind: 'network' })
  assert.equal((await startSimulation(settings, 'operator')).id, 'sim-saved')
  assert.equal((await startSimulation(settings, 'operator')).id, 'sim-saved')
  assert.deepEqual(
    calls,
    Array(3).fill({ path: '/simulations', body: JSON.stringify(settings), key: 'operator' }),
  )
})
test('reads omit operator credentials and stop encodes the run ID', async ({ mock }) => {
  const calls = []
  mock.method(globalThis, 'fetch', async (path, init) => {
    calls.push([path, init])
    return Response.json({ items: [] })
  })
  await listSimulations()
  await getSimulation('run/id')
  await stopSimulation('run/id', 'operator')
  assert.equal(calls[0][0], '/simulations?limit=10')
  assert.equal(calls[0][1].headers, undefined)
  assert.equal(calls[1][0], '/simulations/run%2Fid')
  assert.equal(calls[2][0], '/simulations/run%2Fid/stop')
  assert.equal(calls[2][1].method, 'POST')
  assert.equal(calls[2][1].headers['X-API-Key'], 'operator')
})
