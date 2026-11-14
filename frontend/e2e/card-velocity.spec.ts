import { expect, test } from '@playwright/test'
import type { CardVelocityEvidence, ReviewCase } from '../src/api'
import { mockConsole, now } from './fixtures'

const review: ReviewCase = {
  purchase_id: '42', transaction_id: 'txn-42', player_id: 'player-1', device_id: null,
  amount_minor: '4999', event_time: now, queued_at: now, rule_score: 55, model_score: null, model_version: null,
  fired_rules: [{ name: 'card_velocity', message: 'Card used 1 time in 60 minutes (limit 1)', points: 40 }],
  claimed_by: null, claimed_at: null, ruleset_version: 3, player_purchases: [], device_activity: null,
  cvv_result: null, avs_result: null, three_ds_result: null, account_events: [], ip_intel: null,
}

const evidence: CardVelocityEvidence = {
  count: 1, limit: 1, window_start: '2026-11-12T11:00:00Z', window_end: now,
  records_available: true, context_truncated: false,
  purchases: [
    { transaction_id: 'demo-counted', event_time: '2026-11-12T11:15:00Z', amount_minor: '499', contributed: true, recorded_after_decision: false },
    { transaction_id: 'demo-added-later', event_time: '2026-11-12T11:50:00Z', amount_minor: '1299', contributed: false, recorded_after_decision: true },
  ],
}

test.beforeEach(async ({ page }) => {
  await mockConsole(page, 'reviewer')
  await page.route('**/reviews/42/network', route => route.fulfill({ json: { window_days: 30, anchors: [] } }))
})

test('Card velocity highlights verified contributors and labels later arrivals', async ({ page }, info) => {
  await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })
  await page.route('**/reviews/42', route => route.fulfill({ json: { ...review, card_velocity_evidence: evidence } }))
  await page.goto('/#/reviews/42')
  await page.getByRole('checkbox', { name: 'Show UTC times' }).check()
  const rule = page.getByRole('button', { name: 'Card velocity', exact: true })
  await rule.focus()
  await page.keyboard.press('Enter')
  await expect(rule).toHaveAttribute('aria-expanded', 'true')

  const panel = page.getByRole('region', { name: 'Card velocity evidence' })
  await expect(panel).toContainText('1 earlier purchase on this card (limit 1)')
  await expect(panel).toContainText('2026-11-12 11:00:00')
  await expect(panel).toContainText('Card used 1 time in 60 minutes (limit 1)')
  const first = panel.getByRole('listitem').filter({ hasText: 'demo-counted' })
  const late = panel.getByRole('listitem').filter({ hasText: 'demo-added-later' })
  await expect(first).toContainText('Counted at scoring')
  await expect(late).toContainText('Investigation context · Recorded after scoring')

  await panel.getByRole('button', { name: 'At scoring' }).click()
  await expect(first).toBeVisible()
  await expect(late).toHaveCount(0)
  await panel.getByRole('button', { name: 'What we know now' }).click()
  await expect(late).toBeVisible()
  const screenshot = info.outputPath('card-velocity-evidence.png')
  await panel.screenshot({ path: screenshot })
  await info.attach('Card velocity demo', { path: screenshot, contentType: 'image/png' })
})

test('a legacy case shows its saved rule message without inferring record-level evidence', async ({ page }) => {
  await page.route('**/reviews/42', route => route.fulfill({ status: 404, json: { error: 'review not found' } }))
  await page.route('**/reviews/42/resolved', route => route.fulfill({ json: {
    ...review, resolution: { decision: 'approve', analyst_id: 'demo-user', resolved_at: now },
  } }))
  await page.goto('/#/reviews/42')
  await page.getByRole('button', { name: 'Card velocity', exact: true }).click()
  const panel = page.getByRole('region', { name: 'Card velocity evidence' })
  await expect(panel).toContainText('Card used 1 time in 60 minutes (limit 1)')
  await expect(panel).toContainText('Record-level evidence is unavailable.')
  await expect(panel.getByRole('list')).toHaveCount(0)
})
