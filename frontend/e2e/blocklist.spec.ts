import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import type { BlocklistEntry, BlocklistPage, EntryType } from '../src/api'
import { mockConsole, now, sessionToken } from './fixtures'

const blocklistPath = /\/blocklist(?:\?.*)?$/

function entries(count: number): BlocklistEntry[] {
  return Array.from({ length: count }, (_, index) => ({
    id: count - index, type: index % 2 ? 'card' : 'device', value: `entry-${count - index}`,
    reason: index % 3 ? 'Other reason' : 'Confirmed Ring', created_at: now,
  }))
}

function responseFor(all: BlocklistEntry[], url: URL): BlocklistPage {
  const query = (url.searchParams.get('q') ?? '').trim().toLowerCase()
  const type = url.searchParams.get('type')
  const matches = all.filter(entry => (!type || entry.type === type)
    && (!query || entry.value.toLowerCase().includes(query) || entry.reason.toLowerCase().includes(query)))
  const size = Number(url.searchParams.get('page_size'))
  const totalPages = Math.max(1, Math.ceil(matches.length / size))
  const page = Math.min(Number(url.searchParams.get('page')), totalPages)
  return { entries: matches.slice((page - 1) * size, page * size),
    pagination: { page, page_size: size, total_pages: totalPages, total_items: matches.length } }
}

async function mockBlocklist(page: Page, initial: BlocklistEntry[]) {
  const state = { entries: initial, failedReads: 0, uncertainAdd: false, reads: [] as URL[] }
  await page.route(blocklistPath, async route => {
    expect(route.request().headers().authorization).toBe(`Bearer ${sessionToken}`)
    expect(route.request().headers()['x-api-key']).toBeUndefined()
    if (route.request().method() === 'POST') {
      const entry = route.request().postDataJSON() as { type: EntryType; value: string; reason: string }
      if (state.entries.some(saved => saved.type === entry.type && saved.value === entry.value)) {
        return route.fulfill({ status: 409, json: { error: 'this value is already on the blocklist', fields: { value: 'is already on the blocklist' } } })
      }
      const saved = { ...entry, id: Math.max(1000, ...state.entries.map(item => item.id)) + 1, created_at: now }
      state.entries = [saved, ...state.entries]
      if (state.uncertainAdd) return route.fulfill({ status: 503, json: { error: 'database unavailable' } })
      return route.fulfill({ status: 201, json: saved })
    }
    const url = new URL(route.request().url())
    state.reads.push(url)
    expect(url.searchParams.get('page_size')).toBe('25')
    expect(Number(url.searchParams.get('page'))).toBeGreaterThan(0)
    if (state.failedReads > 0) {
      state.failedReads--
      return route.fulfill({ status: 503, json: { error: 'database unavailable' } })
    }
    if ([...(url.searchParams.get('q') ?? '')].length > 200) {
      return route.fulfill({ status: 400, json: { error: 'invalid request', fields: { q: 'must be at most 200 characters' } } })
    }
    return route.fulfill({ json: responseFor(state.entries, url) })
  })
  await page.route(/\/blocklist\/\d+$/, async route => {
    const id = Number(new URL(route.request().url()).pathname.split('/').at(-1))
    state.entries = state.entries.filter(entry => entry.id !== id)
    await route.fulfill({ status: 204 })
  })
  return state
}

async function addEntry(page: Page, entry: { type: EntryType; value: string; reason: string }) {
  const form = page.getByRole('form', { name: 'Add an entry', exact: true })
  await form.getByLabel('Type', { exact: true }).selectOption(entry.type)
  await form.getByLabel('Value', { exact: false }).fill(entry.value)
  await form.getByLabel('Reason', { exact: false }).fill(entry.reason)
  await page.getByRole('button', { name: 'Add to blocklist', exact: true }).click()
}

test.beforeEach(async ({ page }) => { await mockConsole(page) })

test('type and text filters use server counts and reset or retain the correct page', async ({ page }) => {
  const state = await mockBlocklist(page, entries(60))
  await page.goto('/#/blocklist')
  const pager = page.getByRole('navigation', { name: 'Blocklist pagination' })
  await expect(pager).toContainText('60 matching entries')
  await expect(page.getByRole('rowheader')).toHaveCount(25)
  await pager.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(pager).toContainText('Page 2 of 3')
  await page.getByLabel('Filter by type').selectOption('device')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(pager).toContainText('Page 1 of 2 · 30 matching entries')
  await pager.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(pager).toContainText('Page 2 of 2')
  expect(state.reads.at(-1)?.searchParams.get('type')).toBe('device')
  await page.getByLabel('Search values and reasons').fill('RING')
  await page.getByLabel('Search values and reasons').press('Enter')
  await expect(pager).toContainText('Page 1 of 1 · 10 matching entries')
  await page.getByLabel('Filter by type').selectOption('')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(pager).toContainText('20 matching entries')
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(pager).toContainText('Page 1 of 3 · 60 matching entries')
  await expect(page.getByLabel('Search values and reasons')).toHaveValue('')
  await expect(page.getByLabel('Filter by type')).toHaveValue('')
})

test('literal punctuation matches values and validation errors label the search field', async ({ page }) => {
  await mockBlocklist(page, [{ id: 1, type: 'card', value: 'Token%_\\.*', reason: 'r', created_at: now }])
  await page.goto('/#/blocklist')
  await page.getByLabel('Search values and reasons').fill('%_\\.*')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('rowheader')).toHaveText('Token%_\\.*')
  await page.getByLabel('Search values and reasons').fill('x'.repeat(201))
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByLabel('Search values and reasons')).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByText('must be at most 200 characters', { exact: true })).toBeVisible()
  await page.getByLabel('Search values and reasons').fill('token')
  await expect(page.getByLabel('Search values and reasons')).not.toHaveAttribute('aria-invalid')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('rowheader')).toHaveText('Token%_\\.*')
})

test('empty and filtered empty results have distinct messages and retry keeps filters', async ({ page }) => {
  const state = await mockBlocklist(page, [])
  state.failedReads = 100
  await page.goto('/#/blocklist')
  await expect(page.getByRole('alert')).toContainText('database unavailable')
  state.failedReads = 0
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.getByText('The blocklist is empty.', { exact: false })).toBeVisible()
  await page.getByLabel('Search values and reasons').fill('no match')
  state.failedReads = 1
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.getByText('No entries match these filters.', { exact: false })).toBeVisible()
  expect(state.reads.at(-1)?.searchParams.get('q')).toBe('no match')
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(page.getByText('The blocklist is empty.', { exact: false })).toBeVisible()
})

test('adding entries preserves filters and duplicate field errors', async ({ page }) => {
  await mockBlocklist(page, entries(60))
  await page.goto('/#/blocklist')
  await page.getByLabel('Filter by type').selectOption('device')
  await page.getByLabel('Search values and reasons').fill('ring')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toContainText('10 matching entries')
  await addEntry(page, { type: 'card', value: 'new-card', reason: 'ring' })
  await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toContainText('10 matching entries')
  await expect(page.getByRole('rowheader', { name: 'new-card' })).toHaveCount(0)
  await addEntry(page, { type: 'device', value: 'new-device', reason: 'RING' })
  await expect(page.getByRole('rowheader', { name: 'new-device' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toContainText('11 matching entries')
  await expect(page.getByLabel('Filter by type')).toHaveValue('device')
  await addEntry(page, { type: 'device', value: 'new-device', reason: 'RING' })
  await expect(page.getByRole('form', { name: 'Add an entry' }).getByLabel('Value', { exact: false })).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toContainText('11 matching entries')
})

test('deleting the last page invalidates stale entries even when refresh fails', async ({ page }) => {
  const state = await mockBlocklist(page, entries(26))
  await page.goto('/#/blocklist')
  const pager = page.getByRole('navigation', { name: 'Blocklist pagination' })
  await pager.getByRole('button', { name: 'Last', exact: true }).click()
  await expect(pager).toContainText('Page 2 of 2')
  await page.getByRole('button', { name: /^Remove card token entry-1$/ }).click()
  state.failedReads = 1
  await page.getByRole('dialog').getByRole('button', { name: 'Remove entry', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('database unavailable')
  await expect(page.getByRole('rowheader', { name: 'entry-1', exact: true })).toHaveCount(0)
  await expect(page.getByText('is no longer blocked.', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(pager).toContainText('Page 1 of 1 · 25 matching entries')
  await expect(page.getByRole('rowheader')).toHaveCount(25)
})

test('an uncertain add reconciles saved state without claiming success', async ({ page }) => {
  const state = await mockBlocklist(page, [])
  await page.goto('/#/blocklist')
  await expect(page.getByText('The blocklist is empty.', { exact: false })).toBeVisible()
  state.uncertainAdd = true
  await addEntry(page, { type: 'card', value: 'uncertain-card', reason: 'test' })
  await expect(page.getByRole('alert')).toContainText('database unavailable')
  await expect(page.getByRole('rowheader', { name: 'uncertain-card' })).toBeVisible()
  await expect(page.getByText('is blocked from the next purchase on.', { exact: false })).toHaveCount(0)
})

test('late results from a replaced filter cannot overwrite the current results', async ({ page }) => {
  await mockBlocklist(page, entries(60))
  let release = () => {}
  const gate = new Promise<void>(resolve => { release = resolve })
  let started = false
  let delivered = Promise.resolve()
  await page.route(blocklistPath, async route => {
    const url = new URL(route.request().url())
    if (url.searchParams.get('q') !== 'slow') return route.fallback()
    started = true
    delivered = gate.then(() => route.fulfill({ json: responseFor([], url) }))
    await delivered
  })
  await page.goto('/#/blocklist')
  await expect(page.getByRole('rowheader')).toHaveCount(25)
  await page.getByLabel('Search values and reasons').fill('slow')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect.poll(() => started).toBe(true)
  await page.getByLabel('Search values and reasons').fill('ring')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toContainText('20 matching entries')
  release()
  await delivered
  await expect(page.getByRole('rowheader')).toHaveCount(20)
})

test('a saved addition survives an older initial read arriving late', async ({ page }) => {
  await mockBlocklist(page, [])
  let release = () => {}
  const gate = new Promise<void>(resolve => { release = resolve })
  let blockInitialReads = true
  let started = false
  const deliveries: Promise<void>[] = []
  await page.route(blocklistPath, async route => {
    if (route.request().method() === 'POST') {
      blockInitialReads = false
      return route.fallback()
    }
    if (!blockInitialReads) return route.fallback()
    started = true
    const delivery = gate.then(() => route.fulfill({ json: responseFor([], new URL(route.request().url())) }))
    deliveries.push(delivery)
    await delivery
  })
  await page.goto('/#/blocklist')
  await expect.poll(() => started).toBe(true)
  await expect(page.getByText('Loading blocklist…')).toBeVisible()
  await addEntry(page, { type: 'device', value: 'saved-device', reason: 'r' })
  await expect(page.getByRole('rowheader', { name: 'saved-device' })).toBeVisible()
  release()
  await Promise.all(deliveries)
  await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toContainText('1 matching entries')
})

for (const colorScheme of ['light', 'dark'] as const) {
  test(`search and multi-page results remain accessible in ${colorScheme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme })
    await mockBlocklist(page, entries(60))
    await page.goto('/#/blocklist')
    await expect(page.getByRole('navigation', { name: 'Blocklist pagination' })).toBeVisible()
    for (const width of [1280, 390, 195]) {
      await page.setViewportSize({ width, height: 1000 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      const audit = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
      expect(audit.violations.map(violation => `${violation.id}: ${violation.nodes[0].target}`)).toEqual([])
    }
  })
}
