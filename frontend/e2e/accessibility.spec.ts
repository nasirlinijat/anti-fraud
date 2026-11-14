import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { mockConsole, now } from './fixtures'

const rules = ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']
const routes = ['reviews', 'assignments', 'blocklist', 'rulesets', 'tester', 'simulator', 'accounts']

const review = { purchase_id: 'demo-purchase', transaction_id: 'demo-transaction', player_id: 'demo-player', device_id: 'demo-device',
  amount_minor: '1999', event_time: now, queued_at: now, rule_score: 40, model_score: 0.7, model_version: 'demo-model',
  fired_rules: [{ name: 'high_amount', points: 40, message: 'Amount is above the limit' }], claimed_by: null, claimed_at: null }

// Fills the lists, so the scan covers tables, badges, and row actions.
async function mockPopulatedLists(page: Page) {
  await page.route('**/reviews?*', route => route.fulfill({ json: {
    items: [review, { ...review, purchase_id: 'mine', transaction_id: 'demo-mine', claimed_by: 'demo-user', claimed_at: now },
      { ...review, purchase_id: 'other', transaction_id: 'demo-other', claimed_by: 'other-reviewer', claimed_at: now }],
    next_cursor: null, model_enabled: true, pagination: { page: 1, total_pages: 1, total_items: 0 },
  } }))
  await page.route('**/simulations?*', route => route.fulfill({ json: { items: [], available_scenarios: ['normal'] } }))
  await page.route(/\/blocklist(?:\?.*)?$/, route => route.fulfill({ json: {
    entries: [{ id: 1, type: 'player', value: 'demo-player', reason: 'Synthetic example', created_at: now }],
    pagination: { page: 1, page_size: 25, total_pages: 1, total_items: 1 },
  } }))
  await page.route('**/admin/accounts', route => route.fulfill({ json: { accounts: [
    { username: 'demo-user', role: 'admin', disabled: false, created_at: now },
    { username: 'demo-reviewer', role: 'reviewer', disabled: true, created_at: now },
  ] } }))
  await page.route('**/admin/assignments', route => route.fulfill({ json: { reviewers: [{
    username: 'demo-reviewer', role: 'reviewer', disabled: true, decided: 3,
    open_claims: [{ purchase_id: '103', amount_minor: '46099', rule_score: 52, model_score: null, claimed_at: now }],
  }] } }))
}

for (const colorScheme of ['light', 'dark'] as const) {
  test(`sign-in screen has no detectable accessibility violations in ${colorScheme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme })
    await mockConsole(page, null)
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto('/')
      await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible()
      const audit = await new AxeBuilder({ page }).withTags(rules).analyze()
      expect(audit.violations, `sign-in at ${width}`).toEqual([])
    }
  })

  test(`console pages have no detectable accessibility violations in ${colorScheme}`, async ({ page }) => {
    // Seven pages are scanned at two widths, which takes longer than one page's test.
    test.slow()
    await page.emulateMedia({ colorScheme })
    await mockConsole(page)
    await mockPopulatedLists(page)
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 })
      for (const route of routes) {
        await page.goto(`/#/${route}`)
        await expect(page.locator('main h1')).toBeVisible()
        await expect(page.locator('.loading-skeleton')).toHaveCount(0)
        const audit = await new AxeBuilder({ page }).withTags(rules).analyze()
        expect(audit.violations.map(violation => `${violation.id}: ${violation.nodes[0].target}`), `${route} at ${width}`).toEqual([])
      }
    }
  })
}
