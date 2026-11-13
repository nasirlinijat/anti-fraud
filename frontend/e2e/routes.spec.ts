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
  const lateLogin = { event_id: 'demo-login', event_type: 'login', event_time: now, device_id: 'demo-device', ip_country: 'TR',
    received_at: now, recorded_after_decision: true }
  await page.route('**/reviews/demo-purchase', route => route.fulfill({ json: { ...item(), ruleset_version: 3, player_purchases: [], device_activity: null,
    cvv_result: 'mismatch', avs_result: null, three_ds_result: 'authenticated', account_events: [lateLogin],
    ip_intel: { provider: 'mock', available: true, hosting: false, vpn: true, proxy: false, tor: true } } }))
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
  await expect(page.getByRole('region', { name: 'Recent account events' }).getByText('After the decision')).toBeVisible()
  await expect(page.getByRole('region', { name: 'IP intelligence' })).toContainText('VPN, Tor exit')
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

const wideRow = { purchase_id: 'demo-purchase', transaction_id: 'demo-transaction-with-a-long-identifier', player_id: 'demo-player',
  device_id: null, amount_minor: '1999', event_time: now, queued_at: now, rule_score: 40, model_score: 0.7, model_version: 'demo-model',
  fired_rules: [{ name: 'high_amount', points: 40, message: 'Amount is above the limit for a new account' }],
  claimed_by: null, claimed_at: null }

test('row actions stay in view when the review table is wider than the page', async ({ page }) => {
  const rows = [wideRow, { ...wideRow, purchase_id: 'mine', transaction_id: 'demo-mine', claimed_by: 'demo-user', claimed_at: now }]
  await page.route('**/reviews?*', route => route.fulfill({ json: { items: rows, next_cursor: null, model_enabled: true } }))
  for (const width of [1024, 1280]) {
    await page.setViewportSize({ width, height: 800 })
    await page.goto('/#/reviews')
    const region = page.getByRole('region', { name: 'Purchases awaiting review' })
    await expect(region).toBeVisible()
    expect(await region.evaluate(element => element.scrollWidth > element.clientWidth), `table overflows at ${width}`).toBe(true)
    for (const name of ['Claim demo-transaction-with-a-long-identifier', 'Approve demo-mine', 'Reject demo-mine']) {
      await expect(page.getByRole('button', { name }), `${name} at ${width}`).toBeInViewport({ ratio: 1 })
    }
  }
})

test('the sort menu opens below its button and changes the queue order', async ({ page }) => {
  const sorts: (string | null)[] = []
  await page.route('**/reviews?*', route => {
    sorts.push(new URL(route.request().url()).searchParams.get('sort'))
    return route.fulfill({ json: { items: [wideRow], next_cursor: null, model_enabled: true } })
  })
  // A short window leaves no room below the button, which is where a native list would move on top of it.
  await page.setViewportSize({ width: 1280, height: 300 })
  await page.goto('/#/reviews')
  const button = page.getByRole('button', { name: 'Sort by Rule score', exact: true })
  await button.press('ArrowDown')
  const menu = page.getByRole('menu', { name: 'Sort by' })
  await expect(menu.getByRole('menuitemradio', { name: 'Rule score' })).toBeFocused()
  const [buttonBox, menuBox] = [await button.boundingBox(), await menu.boundingBox()]
  expect(menuBox!.y).toBeGreaterThanOrEqual(buttonBox!.y + buttonBox!.height)

  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)
  await expect(button).toBeFocused()

  await button.click()
  await menu.getByRole('menuitemradio', { name: 'Model risk' }).click()
  await expect(page.getByRole('button', { name: 'Sort by Model risk', exact: true })).toBeFocused()
  await expect(page.getByRole('columnheader', { name: 'Model risk' })).toHaveAttribute('aria-sort', 'descending')
  await expect.poll(() => sorts.at(-1)).toBe('model_score')
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

test('the review queue shows the active model and evaluation dataset', async ({ page }) => {
  await page.goto('/#/reviews')
  const model = page.getByRole('region', { name: 'Fraud model' })
  await expect(model).toContainText('lgbm-game-v9')
  await expect(model).toContainText('Assist')
  await expect(model).toContainText('0.011527')
  await expect(model).toContainText('0.544632')
  await expect(model).toContainText('Held-out simulator traffic')
  await expect(model).toContainText('0.9624')
})

test('the review queue can retry model status and shows when the model is off', async ({ page }) => {
  let available = false
  await page.route('**/model', route => {
    expect(route.request().headers().authorization).toBe(`Bearer ${sessionToken}`)
    return !available
      ? route.fulfill({ status: 500, json: { error: 'Could not read model status.' } })
      : route.fulfill({ json: {
        loaded: false, version: null, mode: 'assist', clear_below: null, flag_at: null, evaluation: null,
      } })
  })
  await page.goto('/#/reviews')
  const model = page.getByRole('region', { name: 'Fraud model' })
  await expect(model.getByRole('alert')).toHaveText('Could not read model status.')
  available = true
  await model.getByRole('button', { name: 'Retry' }).click()
  await expect(model.getByRole('status')).toHaveText('Model is off')
})

test('Rulesets publishes edited model settings and retains inputs after validation errors', async ({ page }) => {
  const active = { version: 3, created_at: now, note: '', review_threshold: 30, block_threshold: 80, rules: {},
    model: { mode: 'assist', clear_below: 0.1, flag_at: 0.5 } }
  await page.route('**/rulesets/active', route => route.fulfill({ json: active }))
  let rejected = false
  await page.route('**/rulesets', route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: { versions: [active] } })
    const config = route.request().postDataJSON()
    if (!rejected) {
      rejected = true
      return route.fulfill({ status: 400, json: { error: 'invalid request', fields: { 'model.flag_at': 'Check the flag threshold.' } } })
    }
    expect(config.model).toEqual({ mode: 'shadow', clear_below: 0.15, flag_at: 0.7 })
    return route.fulfill({ status: 201, json: { ...active, model: config.model, version: 4 } })
  })
  await page.goto('/#/rulesets')
  const mode = page.getByLabel('Model mode')
  const clearBelow = page.getByLabel('Clear review below')
  const flagAt = page.getByLabel('Flag for review at or above')
  const publish = page.getByRole('button', { name: 'Publish new version' })
  await expect(mode).toHaveValue('assist')
  await expect(clearBelow).toHaveValue('0.1')
  await expect(flagAt).toHaveValue('0.5')
  await mode.selectOption('shadow')
  await clearBelow.fill('0.8')
  await publish.click()
  await expect(clearBelow).toHaveAccessibleDescription('Enter a value lower than the flag threshold.')
  await expect(clearBelow).toHaveValue('0.8')
  await clearBelow.fill('0.15')
  await flagAt.fill('0.7')
  await publish.click()
  await expect(flagAt).toHaveAccessibleDescription('Check the flag threshold.')
  await expect(flagAt).toHaveValue('0.7')
  await expect(mode).toHaveValue('shadow')
  await publish.click()
  await expect(page.getByText('Published ruleset version 4.')).toBeVisible()
})

test('Rulesets shows when there is no model loaded', async ({ page }) => {
  await page.route('**/model', route => route.fulfill({ json: {
    loaded: false, version: null, mode: 'off', clear_below: null, flag_at: null, evaluation: null,
  } }))
  await page.goto('/#/rulesets')
  await expect(page.getByText('No model is loaded.')).toBeVisible()
})

test('Rulesets shows effective thresholds for a version that uses the model report', async ({ page }) => {
  await page.route('**/rulesets/active', route => route.fulfill({ json: {
    version: 3, created_at: now, note: '', review_threshold: 30, block_threshold: 80, rules: {},
    model: { mode: 'assist', clear_below: null, flag_at: null },
  } }))
  await page.goto('/#/rulesets')
  const clearBelow = page.getByLabel('Clear review below')
  const flagAt = page.getByLabel('Flag for review at or above')
  await expect(clearBelow).toHaveAttribute('placeholder', '0.011527')
  await expect(flagAt).toHaveAttribute('placeholder', '0.544632')
  await expect(clearBelow).toHaveValue('')
  await expect(flagAt).toHaveValue('')
})
