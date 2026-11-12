import { expect, test } from '@playwright/test'
import { mockConsole } from './fixtures'

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
  await page.route('**/blocklist', route => route.fulfill({ json: {
    entries: [{ id: 1, type: 'player', value: 'demo-player', reason: 'Synthetic example', created_at: '2026-11-12T12:00:00Z' }],
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
