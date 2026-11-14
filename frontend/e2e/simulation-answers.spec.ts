import { expect, test } from '@playwright/test'
import type { SimulationAnswer, SimulationRun } from '../src/api'
import { mockConsole, now } from './fixtures'

const run: SimulationRun = {
  id: 'sim-answers', status: 'completed', created_at: now, finished_at: now,
  settings: { request_id: 'answers', mode: 'backfill', scenarios: ['normal'], seed: 42, rate: 5, count: 20 },
  sent: 4, failed: 0, allowed: 2, reviewed: 1, blocked: 1, pending: false,
}

const knownRows: SimulationAnswer[] = [
  { purchase_id: '44', transaction_id: 'txn-44', outcome: 'allow', answer: 'unknown' },
  { purchase_id: '43', transaction_id: 'txn-43', outcome: 'block', answer: 'fraud' },
]
const misses: SimulationAnswer[] = [
  { purchase_id: '42', transaction_id: 'txn-42', outcome: 'review', answer: 'legitimate' },
  { purchase_id: '41', transaction_id: 'txn-41', outcome: 'allow', answer: 'fraud' },
]

test('reveal preserves rows, exceptions span pages, and a saved decision opens', async ({ page }) => {
  await mockConsole(page)
  await page.route('**/simulations?*', route => route.fulfill({ json: { items: [run], available_scenarios: ['normal'] } }))
  const queries: string[] = []
  await page.route('**/simulations/sim-answers/answers?*', route => {
    const query = new URL(route.request().url()).searchParams
    const before = query.get('before')
    const filter = query.get('filter')
    queries.push(`${filter ?? 'all'}:${before ?? 'first'}`)
    if (filter === 'exceptions') {
      return route.fulfill({ json: before ? { items: [misses[1]] } : { items: [misses[0]], next_cursor: '42' } })
    }
    return route.fulfill({ json: before ? { items: misses } : { items: knownRows, next_cursor: '43' } })
  })
  await page.route('**/purchases/42', route => route.fulfill({ json: {
    purchase_id: '42', transaction_id: 'txn-42', outcome: 'review',
    player_id: 'player', device_id: null, amount_minor: '499', event_time: now, queued_at: now,
    rule_score: 50, model_score: null, model_version: null, fired_rules: [], claimed_by: null, claimed_at: null,
    ruleset_version: 1, player_purchases: [], device_activity: null, cvv_result: null, avs_result: null,
    three_ds_result: null, account_events: [], ip_intel: null, model_reasons: [], resolution: null,
  } }))

  await page.goto('/#/simulator')
  const panel = page.getByRole('region', { name: 'Saved decisions' })
  const transactions = panel.getByRole('rowheader')
  await expect(transactions).toHaveText(['txn-44', 'txn-43'])
  await expect(panel.getByRole('columnheader', { name: 'Simulator answer' })).toHaveCount(0)
  await panel.getByLabel('Reveal answers').check()
  await expect(transactions).toHaveText(['txn-44', 'txn-43'])
  await expect(panel.getByRole('cell', { name: 'Unknown' })).toBeVisible()
  await expect(panel.getByRole('cell', { name: 'Fraud', exact: true })).toBeVisible()
  expect(queries.every(query => query === 'all:first')).toBe(true)

  await panel.getByLabel('Show misses and legitimate friction').check()
  await expect(transactions).toHaveText(['txn-42'])
  await expect(panel.getByRole('cell', { name: /\bReview$/ })).toBeVisible()
  await expect(panel.getByRole('cell', { name: 'Legitimate' })).toBeVisible()
  await panel.getByRole('button', { name: 'Older' }).click()
  await expect(transactions).toHaveText(['txn-41'])
  await panel.getByRole('button', { name: 'Newer' }).click()
  await expect(transactions).toHaveText(['txn-42'])
  expect(queries.filter(query => query.startsWith('exceptions:'))).toEqual(['exceptions:first', 'exceptions:42', 'exceptions:first'])

  await panel.getByRole('link', { name: 'txn-42' }).click()
  await expect(page).toHaveURL(/#\/purchases\/42$/)
  await expect(page.getByRole('heading', { name: 'txn-42' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Decision and review' })).toContainText('Sent to review')
  await expect(page.getByText('Simulator answer', { exact: true })).toHaveCount(0)
})

test('a running feed follows saved rows and a different run hides answers', async ({ page }) => {
  await mockConsole(page)
  const active = { ...run, status: 'running', finished_at: null }
  const other = { ...run, id: 'sim-previous' }
  await page.route('**/simulations?*', route => route.fulfill({ json: { items: [active, other], available_scenarios: ['normal'] } }))
  let items = [knownRows[1]]
  await page.route('**/simulations/sim-answers/answers?*', route => route.fulfill({ json: { items } }))
  await page.route('**/simulations/sim-previous/answers?*', route => route.fulfill({ json: { items: misses } }))

  await page.goto('/#/simulator')
  const panel = page.getByRole('region', { name: 'Saved decisions' })
  await expect(panel.getByRole('rowheader')).toHaveText(['txn-43'])
  await panel.getByLabel('Reveal answers').check()
  items = knownRows
  await page.clock.runFor(3100)
  await expect(panel.getByRole('rowheader')).toHaveText(['txn-44', 'txn-43'])
  await expect(panel.getByRole('cell', { name: 'Unknown' })).toBeVisible()
  await page.getByRole('button', { name: /sim-previous/ }).click()
  await expect(panel.getByRole('rowheader')).toHaveText(['txn-42', 'txn-41'])
  await expect(panel.getByLabel('Reveal answers')).not.toBeChecked()
})
