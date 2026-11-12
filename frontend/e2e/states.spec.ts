import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { evidenceDirectory, mockConsole, runId, statistics } from './fixtures'

for (const colorScheme of ['light', 'dark'] as const) {
  for (const state of ['loading', 'empty', 'error', 'stale'] as const) {
    test(`${state} presentation in ${colorScheme}`, async ({ page }, info) => {
      await page.emulateMedia({ colorScheme, reducedMotion: 'reduce' })
      await page.setViewportSize({ width: 390, height: 960 })
      await mockConsole(page)
      let release = () => {}
      const gate = new Promise<void>(resolve => { release = resolve })
      if (state === 'loading') await page.route('**/stats?*', async route => { await gate; await route.fulfill({ json: statistics() }) })
      if (state === 'empty') {
        await page.route('**/stats?*', route => route.fulfill({ json: { ...statistics(), outcomes: { total: 0, allow: 0, review: 0, block: 0 }, top_rules: [], model: { scored: 0, flagged: 0, cleared: 0 }, p95_scoring_latency_us: null, blocked_value_minor: '0', decision_series: { bucket_seconds: 3600, buckets: [] } } }))
        await page.route('**/stats/quality/runs?*', route => route.fulfill({ json: { run_ids: [] } }))
      }
      if (state === 'error') await page.route('**/stats?*', route => route.fulfill({ status: 503, json: { error: 'Statistics temporarily unavailable.' } }))
      await page.goto('/#/dashboard')
      if (state === 'stale') {
        await page.getByLabel('Run', { exact: true }).selectOption(runId)
        await expect(page.getByText('True positives: 114')).toBeVisible()
        await page.route('**/stats?*', route => route.fulfill({ status: 503, json: { error: 'Statistics temporarily unavailable.' } }))
        await page.clock.fastForward(5000)
        await expect(page.getByText(/Showing the last successful update/)).toBeVisible()
      }
      const labels = { loading: 'Loading dashboard…', empty: 'No evaluation runs for this source yet.', error: /Statistics temporarily unavailable/, stale: /Showing the last successful update/ }
      await expect(page.getByText(labels[state], { exact: state === 'empty' })).toBeVisible()
      await page.evaluate(() => document.fonts.ready)
      const directory = `${evidenceDirectory}/states`
      await mkdir(directory, { recursive: true })
      await page.screenshot({ path: `${directory}/${info.project.name}-${colorScheme}-${state}.png`, fullPage: true })
      release()
    })
  }
}
