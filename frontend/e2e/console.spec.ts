import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { mockConsole, now, quality, runId, sessionToken, statistics } from './fixtures'

test.beforeEach(async ({ page }) => { await mockConsole(page) })

test('hash navigation preserves history, current page and the skip link route', async ({ page, browserName }) => {
  await page.goto('/#/reviews')
  await expect(page.getByRole('heading', { name: 'Review queue', exact: true })).toBeVisible()
  await page.keyboard.press(browserName === 'webkit' ? 'Alt+Tab' : 'Tab')
  await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/#\/reviews$/)
  for (const name of ['Blocklist', 'Rulesets', 'Tester', 'Simulator', 'Dashboard']) {
    await page.getByRole('navigation', { name: 'Pages' }).getByRole('link', { name, exact: true }).click()
    await expect(page.getByRole('link', { name, exact: true }).first()).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('main h1')).toBeVisible()
  }
  await page.goBack()
  await expect(page).toHaveURL(/#\/simulator$/)
  await page.goForward()
  await expect(page).toHaveURL(/#\/dashboard$/)
  await page.goto('/#/unknown')
  await expect(page.locator('main h1')).toHaveText('Dashboard')
})

test('drawer closes with Escape and route activation and restores focus', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/dashboard')
  const menu = page.getByRole('button', { name: 'Menu', exact: true })
  await menu.click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(menu).toBeFocused()
  await menu.press('Enter')
  await page.getByRole('link', { name: 'Review queue', exact: true }).press('Enter')
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await expect(page).toHaveURL(/#\/reviews$/)
  await menu.click()
  await page.setViewportSize({ width: 1280, height: 844 })
  await expect(page.getByRole('navigation', { name: 'Pages' })).toHaveCount(1)
  await expect(page.locator('main h1')).toHaveText('Review queue')
})

test('metrics preserve units, all-time queue, UTC table and period selection', async ({ page }) => {
  await page.goto('/#/dashboard')
  await expect(page.getByText('41.39 ms', { exact: true })).toBeVisible()
  await expect(page.getByText('$28,764.99', { exact: true })).toBeVisible()
  await expect(page.getByText('all time, waiting for an analyst')).toBeVisible()
  await page.getByRole('button', { name: '15 minutes', exact: true }).press('Space')
  await expect(page.getByRole('button', { name: '15 minutes', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByText(/Decisions made in the last 15 minutes/)).toBeVisible()
  await page.getByText('View decision counts by time', { exact: true }).click()
  await expect(page.getByRole('table', { name: 'Decision counts by time (UTC)', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: '180', exact: true }).first()).toBeVisible()
})

test('failed refresh retains the period data and retry recovers without overlap', async ({ page }) => {
  await page.goto('/#/dashboard')
  await expect(page.getByText('41.39 ms', { exact: true })).toBeVisible()
  await page.route('**/stats?*', route => route.fulfill({ status: 503, json: { error: 'Statistics unavailable.' } }))
  await page.clock.fastForward(5000)
  await expect(page.getByText(/Showing the last successful update for this period/)).toBeVisible()
  await expect(page.getByText('41.39 ms', { exact: true })).toBeVisible()
  await page.route('**/stats?*', route => route.fulfill({ json: statistics('24h') }))
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.getByText('Statistics unavailable.')).toHaveCount(0)
})

test('changing period hides prior data until that period succeeds', async ({ page }) => {
  await page.goto('/#/dashboard')
  await expect(page.getByText('41.39 ms', { exact: true })).toBeVisible()
  await page.route('**/stats?period=1h', route => route.fulfill({ status: 503, json: { error: 'Hour unavailable.' } }))
  await page.getByRole('button', { name: '1 hour', exact: true }).click()
  await expect(page.getByText(/Hour unavailable/)).toBeVisible()
  await expect(page.getByText('41.39 ms', { exact: true })).toHaveCount(0)
  await expect(page.getByLabel('Run', { exact: true })).toBeVisible()
})

test('quality preserves source/run isolation, pagination and unavailable rates', async ({ page }) => {
  await page.goto('/#/dashboard')
  await page.getByLabel('Run', { exact: true }).selectOption(runId)
  await expect(page.getByText('True positives: 114')).toBeVisible()
  await expect(page.getByText(/2 pending; 1 quarantined intents excluded/)).toBeVisible()
  await page.getByRole('button', { name: 'Load more runs' }).click()
  await expect(page.getByRole('button', { name: 'Load more runs' })).toHaveCount(0)
  await page.route('**/stats/quality?*', route => route.fulfill({ json: { ...quality('ieee-cis'), precision: null, recall: 0, coverage: { pending: 0, quarantined: 0, scored_decisions: null, run_status: null } } }))
  await page.getByRole('radio', { name: 'IEEE-CIS replay', exact: true }).check()
  await expect(page.getByText('True positives: 114')).toHaveCount(0)
  await expect(page.getByLabel('Run', { exact: true })).toHaveValue('')
  await page.getByLabel('Run', { exact: true }).selectOption(runId)
  await expect(page.getByText('Not applicable', { exact: true })).toBeVisible()
  await expect(page.getByText('0%', { exact: true })).toBeVisible()
  await expect(page.getByText(/Total run coverage is unknown/)).toBeVisible()
  await expect(page.getByText(/does not measure money recovered or prevented/)).toBeVisible()
})

test('quality failure is independent and its stale report remains visible', async ({ page }) => {
  await page.goto('/#/dashboard')
  await page.getByLabel('Run', { exact: true }).selectOption(runId)
  await expect(page.getByText('True positives: 114')).toBeVisible()
  await page.route('**/stats/quality?*', route => route.fulfill({ status: 503, json: { error: 'Evaluation unavailable.' } }))
  await page.clock.fastForward(5000)
  await expect(page.getByText(/Showing the last successful report for this run/)).toBeVisible()
  await expect(page.getByText('True positives: 114')).toBeVisible()
  await expect(page.getByText('41.39 ms', { exact: true })).toBeVisible()
})

test('empty periods and runs never invent quality measurements', async ({ page }) => {
  const empty = statistics()
  await page.route('**/stats?*', route => route.fulfill({ json: { ...empty, outcomes: { total: 0, allow: 0, review: 0, block: 0 }, top_rules: [], model: { scored: 0, flagged: 0, cleared: 0 }, p95_scoring_latency_us: null, decision_series: { bucket_seconds: 3600, buckets: [] } } }))
  await page.route('**/stats/quality/runs?*', route => route.fulfill({ json: { run_ids: [] } }))
  await page.goto('/#/dashboard')
  await expect(page.getByText(/No decisions in this period yet/)).toBeVisible()
  await expect(page.getByText('No evaluation runs for this source yet.')).toBeVisible()
  await expect(page.getByText('41.39 ms', { exact: true })).toHaveCount(0)
})

for (const scheme of ['light', 'dark'] as const) {
  test(`accessible Dashboard and loaded fonts in ${scheme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto('/#/dashboard')
    await page.getByLabel('Run', { exact: true }).selectOption(runId)
    await expect(page.getByText('True positives: 114')).toBeVisible()
    const faces = await page.evaluate(async () => {
      const heading = await document.fonts.load('500 32px Pilat')
      const body = await document.fonts.load('400 16px "Aktiv Grotesk"')
      return [...heading, ...body].map(face => face.status)
    })
    expect(faces.length).toBeGreaterThanOrEqual(2)
    expect(faces.every(status => status === 'loaded')).toBe(true)
    await expect(page.locator('main h1')).toHaveCSS('font-family', /^"?Pilat/)
    await expect(page.locator('body')).toHaveCSS('font-family', /^"?Aktiv Grotesk/)
    const audit = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
    expect(audit.violations).toEqual([])
    await page.emulateMedia({ colorScheme: scheme === 'light' ? 'dark' : 'light' })
    await expect(page.locator('html')).toHaveCSS('color-scheme', scheme === 'light' ? 'dark' : 'light')
  })
}

test('blocked fonts leave both themes usable with large money and identifiers', async ({ page }) => {
  const longRun = 'demo-evaluation-' + 'long-run-identifier-'.repeat(6)
  await page.route('https://cdn.xsolla.net/**', route => route.abort())
  await page.route('**/stats?*', route => route.fulfill({ json: { ...statistics(), blocked_value_minor: '92233720368547758079999' } }))
  await page.route('**/stats/quality/runs?*', route => route.fulfill({ json: { run_ids: [longRun] } }))
  await page.setViewportSize({ width: 390, height: 844 })
  for (const colorScheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme })
    await page.goto('/#/dashboard')
    await expect(page.getByText('$922,337,203,685,477,580,799.99', { exact: true })).toBeVisible()
    await page.getByLabel('Run', { exact: true }).selectOption(longRun)
    await expect(page.locator('.selected-run code')).toHaveText(longRun)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.getByRole('button', { name: 'Menu', exact: true }).click()
    await expect(page.getByRole('link', { name: 'Review queue', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
  }
})

const networkCase = { purchase_id: '41', transaction_id: 'demo-transaction', player_id: 'demo-player', device_id: 'demo-device',
  amount_minor: '1999', event_time: now, queued_at: now, rule_score: 40, model_score: 0.7, model_version: 'demo-model', fired_rules: [],
  claimed_by: null, claimed_at: null }
const linkedPlayers = Array.from({ length: 30 }, (_, index) => ({ type: 'player', value: `sim1-20261111T064452-player-${index}`,
  purchases: 30 - index, status: ['rejected', 'rejected', 'in_review', 'in_review'][index] ?? 'clear' }))
const network = { window_days: 30, anchors: [
  { type: 'device', value: 'demo-device', blocklisted: true, linked_total: 31, linked: linkedPlayers },
  { type: 'card', value: 'demo-card', blocklisted: false, linked_total: 1, linked: [{ type: 'player', value: 'demo-player-2', purchases: 1, status: 'blocked' }] },
  { type: 'player', value: 'demo-player', blocklisted: false, linked_total: 0, linked: [] },
] }

async function openNetworkCase(page: Page) {
  await page.route('**/reviews?*', route => route.fulfill({ json: { items: [networkCase], next_cursor: null, model_enabled: true, pagination: { page: 1, total_pages: 1, total_items: 0 } } }))
  await page.route('**/reviews/41', route => route.fulfill({ json: { ...networkCase, ruleset_version: 3, player_purchases: [], device_activity: null, account_events: [] } }))
  await page.goto('/#/reviews')
  await page.getByRole('button', { name: 'demo-transaction', exact: true }).click()
}

for (const scheme of ['light', 'dark'] as const) {
  test(`review case shows an accessible linked players, cards and devices panel in ${scheme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme })
    await page.route('**/reviews/41/network', route => route.fulfill({ json: network }))
    await openNetworkCase(page)
    await expect(page.getByRole('heading', { name: 'Linked players, cards and devices' })).toBeVisible()
    await expect(page.getByText('Device demo-device (blocklisted): 31 players, 2 rejected.')).toBeVisible()
    await expect(page.getByRole('group', { name: /Diagram of linked players/ })).toBeVisible()
    await expect(page.locator('.network-diagram').getByText('+23 more', { exact: true })).toBeVisible()
    await page.getByText('View linked players, cards and devices as a table').click()
    await expect(page.getByRole('rowheader', { name: 'sim1-20261111T064452-player-29', exact: true })).toBeVisible()
    const audit = await new AxeBuilder({ page }).include('.review-case').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
    expect(audit.violations).toEqual([])
  })
}

const linkedPurchases = { anchor: 'card', value: 'demo-player-2', window_days: 30, total: 3, purchases: [
  { purchase_id: '77', transaction_id: 'linked-transaction', event_time: now, amount_minor: '4999', outcome: 'review',
    review_decision: null, claimed_by: 'other-reviewer', awaiting_review: true },
  { purchase_id: '76', transaction_id: 'earlier-transaction', event_time: now, amount_minor: '1000', outcome: 'block',
    review_decision: null, claimed_by: null, awaiting_review: false },
] }

test('a linked player shows its purchases and opens a linked case', async ({ page }) => {
  let query = new URLSearchParams()
  await page.route('**/reviews/41/network', route => route.fulfill({ json: network }))
  await page.route('**/reviews/41/network/purchases?*', route => {
    query = new URL(route.request().url()).searchParams
    return route.fulfill({ json: linkedPurchases })
  })
  await page.route('**/reviews/77', route => route.fulfill({ json: { ...networkCase, purchase_id: '77', transaction_id: 'linked-transaction',
    claimed_by: 'other-reviewer', claimed_at: now, ruleset_version: 3, player_purchases: [], device_activity: null, account_events: [] } }))
  await page.route('**/reviews/77/network', route => route.fulfill({ json: { window_days: 30, anchors: [] } }))
  await openNetworkCase(page)
  const node = page.getByRole('button', { name: 'Player demo-player-2: Blocked, 1 shared purchase', exact: true })
  await node.press('Enter')
  await expect(node).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('heading', { name: 'Player demo-player-2', exact: true })).toBeFocused()
  const purchases = page.getByRole('region', { name: 'Purchases for Player demo-player-2' })
  await expect(purchases.getByRole('rowheader', { name: 'earlier-transaction' })).toBeVisible()
  await expect(purchases).toContainText('Awaiting review, claimed by other-reviewer')
  await expect(page.getByText('Showing the newest 2 of 3 purchases.')).toBeVisible()
  expect([query.get('anchor'), query.get('value')]).toEqual(['card', 'demo-player-2'])
  const audit = await new AxeBuilder({ page }).include('.case-network').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
  expect(audit.violations).toEqual([])
  await page.getByRole('button', { name: 'Open case linked-transaction' }).click()
  await expect(page.getByRole('heading', { name: 'linked-transaction', level: 2 })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Review queue' })).toBeFocused()
})

test('a failed network load leaves the review case usable and can be retried', async ({ page }) => {
  let failing = true
  await page.route('**/reviews/41/network', route => failing
    ? route.fulfill({ status: 500, json: { error: 'The linked players and cards could not be loaded.' } })
    : route.fulfill({ json: network }))
  await openNetworkCase(page)
  await expect(page.getByText('The linked players and cards could not be loaded.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Fired rules' })).toBeVisible()
  failing = false
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.getByText('Device demo-device (blocklisted): 31 players, 2 rejected.')).toBeVisible()
})

const linkedReview = (id: string, claimedBy: string | null, ruleScore: number) => ({ ...networkCase, purchase_id: id,
  transaction_id: `linked-${id}`, player_id: `player-${id}`, rule_score: ruleScore, claimed_by: claimedBy, claimed_at: claimedBy && now })

test('linked purchases awaiting review are claimed and rejected together', async ({ page }) => {
  let unclaimedHolder: string | null = null
  const claims: unknown[] = []
  const decisions: unknown[] = []
  await page.route('**/reviews/41/network/reviews', route => route.fulfill({ json: { window_days: 30, total: 3, items: decisions.length ? [] : [
    linkedReview('51', unclaimedHolder, 70), linkedReview('52', 'demo-user', 60), linkedReview('53', 'demo-user', 50),
  ] } }))
  await page.route('**/reviews/claims', async route => {
    claims.push(route.request().postDataJSON())
    unclaimedHolder = 'demo-user'
    await route.fulfill({ json: { results: [{ purchase_id: '51', status: 'claimed', claimed_by: 'demo-user', claimed_at: now }] } })
  })
  await page.route('**/reviews/decisions', async route => {
    decisions.push(route.request().postDataJSON())
    const saved = (id: string) => ({ purchase_id: id, status: 'saved', resolved_at: now, device_blocked: true, card_blocked: false, player_blocked: false })
    await route.fulfill({ json: { decision: 'reject', analyst_id: 'demo-user', results: [saved('51'), saved('52'),
      { purchase_id: '53', status: 'already_decided', resolved_at: null, device_blocked: false, card_blocked: false, player_blocked: false }] } })
  })
  await page.route('**/reviews/41/network', route => route.fulfill({ json: { window_days: 30, anchors: [] } }))
  await openNetworkCase(page)
  const panel = page.getByRole('region', { name: 'Linked purchases awaiting review' })
  await expect(panel.getByRole('rowheader', { name: /linked-51/ })).toBeVisible()
  await expect(panel.getByRole('button', { name: 'Reject selected (0)' })).toHaveAttribute('aria-disabled', 'true')

  await panel.getByRole('button', { name: 'Claim all unclaimed (1)' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Claimed 1 purchase.' })).toBeVisible()
  expect(claims).toEqual([{ purchase_ids: ['51'] }])
  await expect(panel.getByRole('checkbox', { name: 'Select linked-51' })).toBeVisible()

  await panel.getByRole('checkbox', { name: 'Select all claimed by you' }).check()
  await panel.getByRole('button', { name: 'Reject selected (3)' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Reject 3 purchases?' })).toBeVisible()
  await expect(dialog.getByRole('region', { name: 'Transactions' })).toContainText('linked-53')
  await dialog.getByRole('checkbox', { name: "Each purchase's device", exact: true }).check()
  const audit = await new AxeBuilder({ page }).include('dialog[open]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
  expect(audit.violations).toEqual([])
  await dialog.getByRole('button', { name: 'Reject', exact: true }).click()

  await expect(page.getByRole('status').filter({ hasText: 'Rejected 2 of 3 purchases and blocked 2 devices. linked-53 was already decided.' })).toBeVisible()
  expect(decisions).toEqual([{ decision: 'reject', block_device: true, purchase_ids: ['51', '52', '53'] }])
  await expect(page.getByRole('heading', { name: 'demo-transaction', level: 2 })).toBeVisible()
  await expect(panel.getByText('No other purchases awaiting review share this purchase')).toBeVisible()
})

test('the linked purchases awaiting review panel has no detectable accessibility violations', async ({ page }) => {
  await page.route('**/reviews/41/network/reviews', route => route.fulfill({ json: { window_days: 30, total: 120, items: [
    linkedReview('51', null, 70), linkedReview('52', 'demo-user', 60), linkedReview('53', 'other-reviewer', 50),
  ] } }))
  await page.route('**/reviews/41/network', route => route.fulfill({ json: { window_days: 30, anchors: [] } }))
  await openNetworkCase(page)
  const panel = page.getByRole('region', { name: 'Linked purchases awaiting review' })
  await panel.getByRole('checkbox', { name: 'Select linked-52' }).check()
  await expect(page.getByText('Showing the 3 highest rule scores of 120.')).toBeVisible()
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme })
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await expect(panel.getByRole('checkbox', { name: 'Select linked-52' })).toBeChecked()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${scheme} at ${width}`).toBe(true)
      const audit = await new AxeBuilder({ page }).include('.linked-reviews').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
      expect(audit.violations, `${scheme} at ${width}`).toEqual([])
    }
  }
})

test('a resolved linked case shows evidence and returns to the selected connection', async ({ page }) => {
  const resolved = { purchase_id: '78', transaction_id: 'resolved-transaction', event_time: now, amount_minor: '499',
    outcome: 'review', review_decision: 'reject', claimed_by: null, awaiting_review: false }
  await page.route('**/reviews/41/network', route => route.fulfill({ json: network }))
  await page.route('**/reviews/41/network/purchases?*', route => route.fulfill({
    json: { ...linkedPurchases, purchases: [resolved], total: 1 },
  }))
  await page.route('**/reviews/78/resolved', route => route.fulfill({ json: {
    ...networkCase, purchase_id: '78', transaction_id: resolved.transaction_id, amount_minor: resolved.amount_minor,
    ruleset_version: 3, player_purchases: [], device_activity: null, account_events: [],
    fired_rules: [{ name: 'card_velocity', points: 40, message: 'Five earlier purchases used this card.' }],
    resolution: { decision: 'reject', analyst_id: 'other-reviewer', resolved_at: now },
  } }))
  await openNetworkCase(page)
  const node = page.getByRole('button', { name: 'Player demo-player-2: Blocked, 1 shared purchase', exact: true })
  await node.press('Enter')
  const open = page.getByRole('button', { name: 'View resolved case resolved-transaction' })
  await open.click()
  const dialog = page.getByRole('dialog', { name: 'Resolved case · resolved-transaction' })
  await expect(dialog.getByText('other-reviewer', { exact: true })).toBeVisible()
  await expect(dialog.getByText('Rejected', { exact: true })).toBeVisible()
  await expect(dialog.getByText('Five earlier purchases used this card.')).toBeVisible()
  await expect(dialog.getByRole('button', { name: /claim|approve|reject/i })).toHaveCount(0)
  await dialog.getByRole('button', { name: 'Back to connection' }).click()
  await expect(node).toHaveAttribute('aria-pressed', 'true')
  await expect(open).toBeFocused()
  await expect(page.getByRole('heading', { name: 'demo-transaction', exact: true })).toBeVisible()
})

test('the dashboard exports the decisions of the chosen period as CSV', async ({ page }) => {
  const asked: string[] = []
  await page.route('**/stats/export?*', async route => {
    asked.push(`${new URL(route.request().url()).search} ${route.request().headers().authorization ?? ''}`)
    await route.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'text/csv; charset=utf-8',
        'Content-Disposition': 'attachment; filename="decisions-7d-20261114T1305Z.csv"',
        'X-Export-Rows': '2',
        'X-Export-Truncated': 'false',
      },
      body: '\ufeffdecided_at,transaction_id\n2026-11-14T13:04:00Z,txn-1\n2026-11-14T12:05:00Z,txn-2\n',
    })
  })
  await page.goto('/#/dashboard')
  await page.getByRole('button', { name: '7 days' }).click()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export the decisions of the last 7 days as CSV' }).click()
  expect((await download).suggestedFilename()).toBe('decisions-7d-20261114T1305Z.csv')
  await expect(page.getByRole('status').filter({ hasText: 'Downloaded 2 decisions as decisions-7d-20261114T1305Z.csv.' })).toBeVisible()
  expect(asked).toEqual([`?period=7d Bearer ${sessionToken}`])
})

test('the dashboard says when an export was cut off', async ({ page }) => {
  await page.route('**/stats/export?*', route => route.fulfill({
    status: 200,
    headers: { 'Content-Disposition': 'attachment; filename="decisions-all.csv"', 'X-Export-Rows': '50000', 'X-Export-Truncated': 'true' },
    body: 'decided_at\n',
  }))
  await page.goto('/#/dashboard')
  await page.getByRole('button', { name: 'All time' }).click()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export the decisions of all time as CSV' }).click()
  await download
  await expect(page.getByRole('status').filter({ hasText: 'Downloaded the newest 50,000 decisions. The period has more' })).toBeVisible()
})
