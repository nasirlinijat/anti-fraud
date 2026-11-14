import { expect, test } from '@playwright/test'
import type { ReviewCase } from '../src/api'
import { mockConsole, now } from './fixtures'

const review: ReviewCase = {
  purchase_id: '42', transaction_id: 'txn-42', player_id: 'player-1', device_id: null,
  amount_minor: '4999', event_time: now, queued_at: now, rule_score: 55, model_score: 0.7, model_version: 'demo-model',
  fired_rules: [{ name: 'card_velocity', message: 'Six purchases in the last hour', points: 25 }],
  claimed_by: 'demo-user', claimed_at: now, ruleset_version: 3, player_purchases: [], device_activity: null,
  cvv_result: 'mismatch', avs_result: null, three_ds_result: null, account_events: [], ip_intel: null,
}
const missingCase = { status: 404, json: { error: 'review not found' } }
const address = '/#/reviews/42'

test.beforeEach(async ({ page }) => {
  await mockConsole(page, 'reviewer')
  await page.route('**/reviews/42/network', route => route.fulfill({ json: { window_days: 30, anchors: [] } }))
})

test('saving a case offers its decision and returning restores the queue position', async ({ page }) => {
  let saved = false
  let totalPages = 3
  await page.route('**/reviews?*', route => {
    const query = new URL(route.request().url()).searchParams
    const number = Math.min(Number(query.get('page')), totalPages)
    const matches = query.get('claimed') === 'mine' && query.get('sort') === 'model_score' && number === 3
    return route.fulfill({ json: {
      items: matches && !saved ? [review] : [], model_enabled: true, next_cursor: null,
      pagination: { page: number, total_pages: totalPages, total_items: totalPages * 100 },
    } })
  })
  await page.route('**/reviews/42', route => route.fulfill(saved ? missingCase : { json: review }))
  await page.route('**/reviews/42/resolved', route => route.fulfill(saved ? {
    json: { ...review, resolution: { decision: 'approve', analyst_id: 'demo-user', resolved_at: now } },
  } : missingCase))
  await page.route('**/reviews/42/decision', async route => {
    expect(route.request().postDataJSON()).toEqual({ decision: 'approve' })
    saved = true
    await route.fulfill({ status: 201, json: {} })
  })

  await page.goto('/#/reviews')
  await page.getByRole('button', { name: 'My claims' }).click()
  await page.getByRole('button', { name: 'Sort by Rule score' }).click()
  await page.getByRole('menuitemradio', { name: 'Model risk' }).click()
  const pager = page.getByRole('navigation', { name: 'Review queue pagination' })
  await pager.getByRole('button', { name: 'Page 3', exact: true }).click()
  await page.getByRole('button', { name: 'txn-42', exact: true }).click()
  await expect(page).toHaveURL(/#\/reviews\/42$/)
  await page.getByRole('button', { name: 'Approve txn-42' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Approve', exact: true }).click()
  const feedback = page.getByRole('status').filter({ hasText: 'Approved txn-42.' })
  const decisionLink = feedback.getByRole('link', { name: 'View decision' })
  await expect(decisionLink).toHaveAttribute('href', '#/reviews/42')
  await decisionLink.click()

  const decision = page.getByRole('region', { name: 'Analyst decision' })
  await expect(page).toHaveURL(/#\/reviews\/42$/)
  await expect(decision).toContainText('Approved')
  await expect(decision).toContainText('demo-user')
  await expect(decision.locator('time')).toHaveAttribute('datetime', new Date(now).toISOString())
  await expect(page.getByRole('button', { name: /^(Claim|Approve|Reject) txn-42$/ })).toHaveCount(0)
  const scoring = page.getByRole('region', { name: 'Evidence at scoring' })
  await expect(scoring.getByRole('region', { name: 'Fired rules' })).toContainText('Six purchases in the last hour')
  await expect(scoring.getByRole('region', { name: 'Why this model risk' })).toContainText('Model reasons are unavailable for this score.')
  await expect(page.getByRole('region', { name: 'Investigation context' }).getByRole('region', { name: 'Recent account events' })).toBeVisible()

  await page.reload()
  await expect(decision).toBeVisible()
  await page.getByRole('button', { name: 'Back to the queue' }).click()
  await expect(page.getByRole('button', { name: 'My claims' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Sort by Model risk' })).toBeVisible()
  await expect(pager).toContainText('Page 3 of 3')

  await page.goto(address)
  await expect(decision).toBeVisible()
  totalPages = 2
  await page.clock.runFor(3100)
  await page.getByRole('button', { name: 'Back to the queue' }).click()
  await expect(pager).toContainText('Page 2 of 2')
  await expect(page.getByRole('button', { name: 'My claims' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Sort by Model risk' })).toBeVisible()
})

test('the original URL shows another analyst’s decision when the case resolves', async ({ page }) => {
  let resolved = false
  await page.route('**/reviews/42', route => route.fulfill(resolved ? missingCase : {
    json: { ...review, claimed_by: 'other-reviewer' },
  }))
  await page.route('**/reviews/42/resolved', route => route.fulfill({
    json: { ...review, resolution: { decision: 'reject', analyst_id: 'other-reviewer', resolved_at: now } },
  }))
  await page.route('**/reviews/999', route => route.fulfill(missingCase))
  await page.route('**/reviews/999/resolved', route => route.fulfill(missingCase))

  await page.goto(address)
  await expect(page.getByText('Claimed by other-reviewer', { exact: true })).toBeVisible()
  resolved = true
  await page.clock.runFor(3100)
  const decision = page.getByRole('region', { name: 'Analyst decision' })
  await expect(decision).toContainText('Rejected')
  await expect(decision).toContainText('other-reviewer')
  await expect(page).toHaveURL(/#\/reviews\/42$/)

  await page.goto('/#/reviews/999')
  await expect(page.getByText('This review case is unavailable.', { exact: false })).toBeVisible()
  await expect(decision).toHaveCount(0)
})
