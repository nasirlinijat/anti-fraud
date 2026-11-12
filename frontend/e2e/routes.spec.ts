import { expect, test } from '@playwright/test'
import { mockConsole, now, sessionToken } from './fixtures'

test.beforeEach(async ({ page }) => { await mockConsole(page) })

for (const colorScheme of ['light', 'dark'] as const) {
  test(`every route fits target widths and zoom-equivalent widths in ${colorScheme}`, async ({ page }) => {
    // Eight pages are checked at six widths, which takes longer than one page's test.
    test.slow()
    await page.emulateMedia({ colorScheme })
    for (const width of [390, 768, 1280, 195, 384, 640]) {
      await page.setViewportSize({ width, height: 960 })
      // The console swaps the sidebar and the menu after the resize event, so wait for the swap.
      await expect(page.locator('.console-sidebar')).toHaveCount(width < 1024 ? 0 : 1)
      for (const route of ['dashboard', 'reviews', 'assignments', 'blocklist', 'rulesets', 'tester', 'simulator', 'accounts']) {
        await page.goto(`/#/${route}`)
        await expect(page.locator('main h1')).toBeVisible()
        const overflow = await page.evaluate(() => [...document.querySelectorAll('main *')].filter(element => element.getBoundingClientRect().right > innerWidth + 1 && !element.closest('.decision-table, .table-scroll')).slice(0, 5).map(element => `${element.tagName}.${element.className}`))
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${route} at ${width}: ${overflow.join(', ')}`).toBe(true)
        if (width < 1024) {
          await page.getByRole('button', { name: 'Menu', exact: true }).click()
          await expect(page.getByRole('dialog').getByRole('link', { name: 'Simulator', exact: true })).toBeVisible()
          await page.keyboard.press('Escape')
        }
      }
    }
  })
}

test('Tester native form submits once and retains the saved outcome', async ({ page }) => {
  let posts = 0
  await page.route('**/score', async route => {
    posts++
    await route.fulfill({ json: { transaction_id: 'demo-purchase', outcome: 'review', score: 40, fired_rules: [], ruleset_version: 3, model_score: 0.7, model_version: 'demo-model' } })
  })
  await page.goto('/#/tester')
  await page.getByRole('button', { name: 'Score purchase', exact: true }).press('Enter')
  await expect(page.locator('.result .outcome')).toContainText('Sent to review')
  expect(posts).toBe(1)
})

test('a purchase is claimed, shows its evidence, and is approved only after confirmation', async ({ page }) => {
  let claimed = false
  let posts = 0
  const item = () => ({ purchase_id: 'demo-purchase', transaction_id: 'demo-transaction', player_id: 'demo-player', device_id: null,
    amount_minor: '1999', event_time: now, queued_at: now, rule_score: 40, model_score: 0.7, model_version: 'demo-model', fired_rules: [],
    claimed_by: claimed ? 'demo-user' : null, claimed_at: claimed ? now : null })
  await page.route('**/reviews?*', route => route.fulfill({ json: { items: posts ? [] : [item()], next_cursor: null, model_enabled: true } }))
  await page.route('**/reviews/demo-purchase', route => route.fulfill({ json: { ...item(), ruleset_version: 3, player_purchases: [], device_activity: null,
    cvv_result: 'mismatch', avs_result: null, three_ds_result: 'authenticated' } }))
  await page.route('**/reviews/demo-purchase/network', route => route.fulfill({ json: { window_days: 30, anchors: [
    { type: 'card', value: 'demo-card', blocklisted: false, linked_total: 0, linked: [] },
    { type: 'player', value: 'demo-player', blocklisted: false, linked_total: 0, linked: [] },
  ] } }))
  await page.route('**/reviews/demo-purchase/claim', async route => { claimed = true; await route.fulfill({ status: 201, json: { purchase_id: 'demo-purchase', claimed_by: 'demo-user', claimed_at: now } }) })
  await page.route('**/reviews/demo-purchase/decision', async route => { posts++; await route.fulfill({ status: 201, json: {} }) })
  await page.goto('/#/reviews')
  await expect(page.getByRole('button', { name: 'Approve demo-transaction' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Claim demo-transaction' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'You claimed demo-transaction' })).toBeVisible()
  await page.getByRole('button', { name: 'demo-transaction', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'demo-transaction' })).toBeVisible()
  await expect(page.getByText('Claimed by you')).toBeVisible()
  await expect(page.getByText("No other players or cards share this purchase's player or card in the 30 days around it.")).toBeVisible()
  const verification = page.getByRole('region', { name: 'Payment verification' })
  await expect(verification).toContainText('Mismatch')
  await expect(verification).toContainText('Not sent')
  await expect(verification).toContainText('Authenticated')
  await page.getByRole('button', { name: 'Back to the queue' }).click()
  await page.getByRole('button', { name: 'Approve demo-transaction' }).click()
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  expect(posts).toBe(0)
  await page.getByRole('button', { name: 'Approve demo-transaction' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Approve', exact: true }).click()
  await expect.poll(() => posts).toBe(1)
})

test('a purchase claimed by another account offers no decision', async ({ page }) => {
  const item = { purchase_id: 'demo-purchase', transaction_id: 'demo-transaction', player_id: 'demo-player', device_id: null,
    amount_minor: '1999', event_time: now, queued_at: now, rule_score: 40, model_score: null, model_version: null, fired_rules: [],
    claimed_by: 'other-reviewer', claimed_at: now }
  await page.route('**/reviews?*', route => route.fulfill({ json: { items: [item], next_cursor: null, model_enabled: false } }))
  await page.goto('/#/reviews')
  await expect(page.getByText('Claimed by other-reviewer', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: /^(Claim|Approve|Reject) demo-transaction$/ })).toHaveCount(0)
})

test('Blocklist entries are added with the session and removed after confirmation', async ({ page }) => {
  let saved = false
  const writes: (string | undefined)[] = []
  const entry = { id: 1, type: 'player', value: 'demo-player', reason: 'Synthetic example', created_at: now }
  await page.route('**/blocklist', async route => {
    if (route.request().method() === 'POST') {
      saved = true
      writes.push(route.request().headers().authorization)
      return route.fulfill({ status: 201, json: entry })
    }
    return route.fulfill({ json: { entries: saved ? [entry] : [] } })
  })
  await page.route('**/blocklist/1', async route => { saved = false; await route.fulfill({ status: 204 }) })
  await page.goto('/#/blocklist')
  await page.getByLabel('Type', { exact: true }).selectOption('player')
  await page.getByLabel('Value', { exact: false }).fill('demo-player')
  await page.getByLabel('Reason', { exact: false }).fill('Synthetic example')
  await page.getByRole('button', { name: 'Add to blocklist' }).click()
  await expect(page.getByRole('rowheader', { name: 'demo-player', exact: true })).toBeVisible()
  expect(writes).toEqual([`Bearer ${sessionToken}`])
  await page.getByRole('button', { name: /Remove player/ }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('rowheader', { name: 'demo-player', exact: true })).toBeVisible()
  await page.getByRole('button', { name: /Remove player/ }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Remove entry', exact: true }).click()
  await expect(page.getByText('The blocklist is empty.', { exact: false })).toBeVisible()
})

test('Rulesets can select read-only history and return to the editable version', async ({ page }) => {
  const ruleset = { version: 3, note: 'Synthetic configuration', created_at: now, review_threshold: 30, block_threshold: 80, rules: {} }
  await page.route('**/rulesets', route => route.fulfill({ json: { versions: [ruleset, { ...ruleset, version: 2 }] } }))
  await page.route('**/rulesets/2', route => route.fulfill({ json: { ...ruleset, version: 2 } }))
  await page.goto('/#/rulesets')
  await page.getByRole('button', { name: 'Version 2', exact: true }).click()
  await expect(page.getByText('Viewing version 2 (read-only)')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Publish new version' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Edit active version (3)' }).click()
  await expect(page.getByRole('button', { name: 'Publish new version' })).toBeEnabled()
})

test('Simulator can start, follow and stop a synthetic run', async ({ page }) => {
  let active = false
  let status = 'running'
  const run = () => ({ id: 'demo-simulation', status, created_at: now, finished_at: status === 'stopped' ? now : null,
    settings: { request_id: 'demo', mode: 'live', scenarios: ['normal'], seed: 1, rate: 5, count: 0 },
    sent: 15, failed: 0, allowed: 12, reviewed: 2, blocked: 1, pending: false })
  await page.route('**/simulations?*', route => route.fulfill({ json: { items: active ? [run()] : [], available_scenarios: ['normal'], next_cursor: null } }))
  await page.route('**/simulations', async route => { active = true; await route.fulfill({ status: 202, json: run() }) })
  await page.route('**/simulations/demo-simulation', route => route.fulfill({ json: run() }))
  await page.route('**/simulations/demo-simulation/stop', async route => { status = 'stopped'; await route.fulfill({ json: run() }) })
  await page.goto('/#/simulator')
  await page.getByRole('button', { name: 'Start run', exact: true }).click()
  await expect(page.locator('.simulation-progress .simulation-status')).toHaveText('Running')
  await page.getByRole('button', { name: 'Stop run', exact: true }).click()
  await expect(page.locator('.simulation-progress .simulation-status')).toHaveText('Stopped')
})
