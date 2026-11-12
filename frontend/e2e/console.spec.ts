import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'
import { mockConsole, quality, runId, statistics } from './fixtures'

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
