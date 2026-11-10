import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatTimestamp, formatUsdMinor } from '../src/displayFormat.ts'

test('USD formatting preserves cents for small values and large totals', () => {
  assert.equal(formatUsdMinor('0'), '$0.00')
  assert.equal(formatUsdMinor('1'), '$0.01')
  assert.equal(formatUsdMinor('499'), '$4.99')
  assert.equal(formatUsdMinor('9223372036854775807'), '$92,233,720,368,547,758.07')
  assert.equal(formatUsdMinor('92233720368547758079999'), '$922,337,203,685,477,580,799.99')
})

test('missing or malformed amounts are unavailable', () => {
  for (const value of [null, undefined, '', '49.9', '1e3', '-1', 'abc']) {
    assert.equal(formatUsdMinor(value), 'Unavailable')
  }
})

test('timestamps keep their UTC date across offsets', () => {
  const now = Date.parse('2026-11-10T12:00:00Z')
  assert.deepEqual(formatTimestamp('2026-11-10T15:56:00+04:00', now), {
    iso: '2026-11-10T11:56:00.000Z',
    utc: '2026-11-10 11:56:00.000 UTC',
    relative: '4 min. ago',
  })
  assert.equal(formatTimestamp('invalid', now), null)
})

test('relative timestamps switch units at whole minute, hour and day boundaries', () => {
  const now = Date.parse('2026-11-10T12:00:00Z')
  const at = (offset) => formatTimestamp(new Date(now + offset).toISOString(), now).relative
  assert.equal(at(-59_999), 'just now')
  assert.equal(at(-60_000), '1 min. ago')
  assert.equal(at(-3_599_999), '59 min. ago')
  assert.equal(at(-3_600_000), '1 hr. ago')
  assert.equal(at(-86_400_000), '1 day ago')
  assert.equal(at(59_999), 'just now')
  assert.equal(at(120_000), 'in 2 min.')
})
