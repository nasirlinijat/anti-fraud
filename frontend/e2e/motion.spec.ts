import { expect, test, type Locator, type Page } from '@playwright/test'
import { mockConsole, recentPipeline } from './fixtures'

test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  await mockConsole(page)
})

test('the mobile menu slides in and out and stays usable while it moves', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/dashboard')
  const drawer = page.getByRole('dialog', { name: 'Application navigation' })
  await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await expect(drawer).toHaveCSS('transition-duration', /0\.28s/)
  await expect.poll(async () => (await drawer.boundingBox())?.x).toBe(0)
  await page.keyboard.press('Escape')
  await expect(drawer).not.toBeVisible()
  await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await drawer.getByRole('link', { name: 'Blocklist', exact: true }).click()
  await expect(page.locator('main h1')).toHaveText('Blocklist')
  await expect(drawer).not.toBeVisible()
})

test('buttons, pages and confirmations animate briefly', async ({ page }) => {
  await page.route(/\/blocklist(?:\?.*)?$/, route => route.fulfill({ json: {
    entries: [{ id: 1, type: 'player', value: 'demo-player', reason: 'Synthetic example', created_at: '2026-11-12T12:00:00Z' }],
    pagination: { page: 1, page_size: 25, total_pages: 1, total_items: 1 },
  } }))
  await page.goto('/#/blocklist')
  await expect(page.locator('.blocklist')).toHaveCSS('animation-name', 'rise-in')
  await expect(page.getByRole('button', { name: 'Add to blocklist' })).toHaveCSS('transition-property', /transform/)
  await page.getByRole('button', { name: /Remove player/ }).click()
  await expect(page.getByRole('dialog')).toHaveCSS('animation-name', 'pop-in')
  const durations = await page.evaluate(() => document.getAnimations()
    .filter(animation => animation.effect?.getTiming().iterations !== Infinity)
    .map(animation => Number(animation.effect?.getTiming().duration)))
  expect(Math.max(...durations)).toBeLessThanOrEqual(300)
})

test('reduced motion switches every animation off', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/blocklist')
  await expect(page.locator('.blocklist')).toHaveCSS('animation-name', 'none')
  await expect(page.getByRole('button', { name: 'Add to blocklist' })).toHaveCSS('transition-duration', /^(0s|1e-05s)$/)
  await page.getByRole('button', { name: 'Menu', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Application navigation' })
  expect((await drawer.boundingBox())?.x).toBe(0)
  await page.keyboard.press('Escape')
  await expect(drawer).not.toBeVisible()
})

const pathIds = ['chain-blocklist', 'chain-rules', 'chain-model', 'blocklist-block', 'rules-block', 'model-review', 'model-allow']

function dots(page: Page, path: string): Locator {
  return page.locator(`.pipeline-diagram-wide [data-path="${path}"] .pipeline-flow-dash`)
}

function dashOffset(locator: Locator): Promise<number> {
  return locator.evaluate(element => Number((element as SVGPathElement).style.strokeDashoffset || 0))
}

function recentReads(page: Page): (string | null)[] {
  const reads: (string | null)[] = []
  page.on('request', request => {
    const url = new URL(request.url())
    if (url.pathname === '/stats/pipeline/recent') reads.push(url.searchParams.get('after'))
  })
  return reads
}

async function setVisibility(page: Page, state: 'hidden' | 'visible') {
  await page.evaluate(value => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => value })
    document.dispatchEvent(new Event('visibilitychange'))
  }, state)
}

test('the scoring pipeline moves dots along the paths with recent traffic and pulses the bins', async ({ page }) => {
  await page.goto('/#/dashboard')
  await expect(page.getByText('Live · 36 decisions in the last 2 s')).toBeVisible()
  const chain = dots(page, 'chain-blocklist')
  const start = await dashOffset(chain)
  await expect.poll(() => dashOffset(chain)).toBeLessThan(start - 10)
  await expect(chain).toHaveCSS('opacity', '1')
  for (const outcome of ['allow', 'review', 'block']) {
    await expect(page.locator(`.pipeline-diagram-wide .pipeline-bin-${outcome} .pipeline-bin-pulse`)).toHaveCSS('animation-name', 'bin-pulse')
  }
  const paths = await page.locator('.pipeline-diagram-wide [data-path]').evaluateAll(groups => groups.map(group => group.getAttribute('data-path')))
  expect(paths).toEqual(pathIds)
})

test('a path without recent traffic stands still while the others move', async ({ page }) => {
  await page.route('**/stats/pipeline/recent*', route => {
    const after = new URL(route.request().url()).searchParams.get('after')
    const recent = recentPipeline(after === null ? null : Number(after))
    return route.fulfill({ json: after === null ? recent : { ...recent, outcomes: { allow: 36, review: 0, block: 0, total: 36 }, blocklist_blocks: 0, model: { flagged: 0, cleared: 0 } } })
  })
  await page.goto('/#/dashboard')
  await expect(page.getByText('Live · 36 decisions in the last 2 s')).toBeVisible()
  const allow = dots(page, 'model-allow')
  const start = await dashOffset(allow)
  await expect.poll(() => dashOffset(allow)).toBeLessThan(start - 10)
  for (const path of ['blocklist-block', 'rules-block', 'model-review']) {
    expect(await dashOffset(dots(page, path))).toBe(0)
    await expect(dots(page, path)).toHaveCSS('opacity', '0')
  }
  await expect(page.locator('.pipeline-diagram-wide .pipeline-bin-block .pipeline-bin-pulse')).toHaveCount(0)
})

test('a hidden tab stops the pipeline and starts counting again from now when it returns', async ({ page }) => {
  const reads = recentReads(page)
  await page.goto('/#/dashboard')
  await expect(page.getByText('Live · 36 decisions in the last 2 s')).toBeVisible()
  await setVisibility(page, 'hidden')
  await expect(page.locator('.pipeline-figure-live')).toHaveCount(0)
  const readsBeforeHidden = reads.length
  await page.clock.fastForward(6000)
  expect(reads).toHaveLength(readsBeforeHidden)
  await setVisibility(page, 'visible')
  await expect(page.getByText('Live', { exact: true })).toBeVisible()
  expect(reads[readsBeforeHidden]).toBeNull()
})

test('the pipeline stays still without a live note when the recent feed fails', async ({ page }) => {
  await page.route('**/stats/pipeline/recent*', route => route.fulfill({ status: 503, json: { error: 'statistics unavailable' } }))
  const failed = page.waitForResponse(response => response.url().includes('/stats/pipeline/recent'))
  await page.goto('/#/dashboard')
  await failed
  await expect(page.getByRole('heading', { name: 'Scoring pipeline' })).toBeVisible()
  await expect(page.locator('.pipeline-figure-live')).toHaveCount(0)
  await expect(page.getByText(/^Live/)).toHaveCount(0)
  await expect(dots(page, 'chain-blocklist')).toBeHidden()
})

test('reduced motion keeps the scoring pipeline still and does not read the recent feed', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const reads = recentReads(page)
  await page.goto('/#/dashboard')
  await expect(page.getByRole('heading', { name: 'Scoring pipeline' })).toBeVisible()
  await page.clock.fastForward(5000)
  expect(reads).toHaveLength(0)
  await expect(page.locator('.pipeline-figure-live')).toHaveCount(0)
  await expect(page.getByText(/^Live/)).toHaveCount(0)
  await expect(dots(page, 'chain-blocklist')).toBeHidden()
})
