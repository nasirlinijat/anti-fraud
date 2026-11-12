import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'
import { mockConsole, runId, signIn } from './fixtures'

test.beforeEach(async ({ page }) => { await mockConsole(page) })

for (const scheme of ['light', 'dark'] as const) {
  test(`initial ${scheme} system appearance supports saved switching and reload`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto('/#/dashboard')
    const toggle = page.getByRole('button', { name: 'Dark mode', exact: true })
    await expect(toggle).toHaveAttribute('aria-pressed', String(scheme === 'dark'))
    await toggle.focus()
    await toggle.press('Space')
    const chosen = scheme === 'dark' ? 'light' : 'dark'
    await expect(toggle).toHaveAttribute('aria-pressed', String(chosen === 'dark'))
    await expect(toggle).toBeFocused()
    await expect(toggle).toHaveCSS('outline-style', 'solid')
    expect(await page.evaluate(() => localStorage.getItem('anti-fraud.theme'))).toBe(chosen)
    await page.reload()
    await expect(page.locator('html')).toHaveCSS('color-scheme', chosen)
    await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', chosen)
    await page.emulateMedia({ colorScheme: chosen })
    await page.emulateMedia({ colorScheme: scheme })
    await expect(page.locator('html')).toHaveCSS('color-scheme', chosen)
    await toggle.press('Enter')
    await expect(toggle).toHaveAttribute('aria-pressed', String(scheme === 'dark'))
    expect(await page.evaluate(() => localStorage.getItem('anti-fraud.theme'))).toBe(scheme)
  })

  test(`stored ${scheme} overrides the opposite system and navigation`, async ({ page }) => {
    await page.addInitScript(mode => localStorage.setItem('anti-fraud.theme', mode), scheme)
    await page.emulateMedia({ colorScheme: scheme === 'dark' ? 'light' : 'dark' })
    await page.goto('/#/dashboard')
    await expect(page.locator('html')).toHaveCSS('color-scheme', scheme)
    await page.getByRole('link', { name: 'Tester', exact: true }).first().click()
    await expect(page.locator('html')).toHaveCSS('color-scheme', scheme)
    const audit = await new AxeBuilder({ page }).include('.console-topbar').include('.console-sidebar')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
    expect(audit.violations).toEqual([])
  })
}

test('invalid storage follows system changes until the first manual choice', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('anti-fraud.theme', 'invalid'))
  await page.emulateMedia({ colorScheme: 'light' })
  await page.goto('/#/dashboard')
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light')
  await page.emulateMedia({ colorScheme: 'dark' })
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark')
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await page.emulateMedia({ colorScheme: 'light' })
  await page.emulateMedia({ colorScheme: 'dark' })
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light')
})

test('another tab updates the saved preference and clearing restores the system default', async ({ page, context }) => {
  await page.emulateMedia({ colorScheme: 'light' })
  await page.goto('/#/dashboard')
  const other = await context.newPage()
  await mockConsole(other)
  await other.emulateMedia({ colorScheme: 'light' })
  await other.goto('/#/dashboard')
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await expect(other.locator('html')).toHaveCSS('color-scheme', 'dark')
  await other.getByRole('button', { name: 'Dark mode' }).click()
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light')
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await other.evaluate(() => localStorage.removeItem('anti-fraud.theme'))
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light')
  await other.close()
})

test('blocked storage keeps switching usable and explains persistence failure', async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(window, 'localStorage', {
    get() { throw new DOMException('Blocked', 'SecurityError') },
  }))
  await page.emulateMedia({ colorScheme: 'light' })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/dashboard')
  // Without storage no session is restored, and the one from the form lives in memory.
  await signIn(page)
  await expect(page.locator('main h1')).toHaveText('Dashboard')
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark')
  await expect(page.getByRole('status').filter({ hasText: 'Your choice could not be saved' })).toBeVisible()
  await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.emulateMedia({ colorScheme: 'light' })
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light')
})

test('logo proportions, clear space and header actions fit desktop and mobile', async ({ page }) => {
  for (const width of [1280, 768, 390, 195]) {
    await page.setViewportSize({ width, height: 960 })
    await page.goto('/#/dashboard')
    const logo = page.getByRole('img', { name: 'Xsolla', exact: true })
    await expect(logo).toHaveCount(1)
    await expect(logo).toBeVisible()
    await expect(logo).toHaveJSProperty('naturalWidth', 141)
    const bounds = await logo.boundingBox()
    expect(bounds?.width).toBe(141)
    expect(bounds?.height).toBe(38)
    const spacing = await logo.evaluate(element => element.nextElementSibling!.getBoundingClientRect().top - element.getBoundingClientRect().bottom)
    expect(spacing).toBe(16)
    await expect(page.locator('.brand-mark')).toHaveCount(0)
    const toggle = await page.getByRole('button', { name: 'Dark mode' }).boundingBox()
    expect(toggle?.width).toBe(64)
    expect(toggle?.height).toBe(44)
    if (width < 1024) {
      const menu = await page.getByRole('button', { name: 'Menu', exact: true }).boundingBox()
      expect(menu).not.toBeNull()
      expect(toggle).not.toBeNull()
      expect(menu!.y >= toggle!.y + toggle!.height || menu!.x + menu!.width <= toggle!.x).toBe(true)
      await page.getByRole('button', { name: 'Menu', exact: true }).click()
      await expect(page.getByRole('dialog').getByRole('img', { name: 'Xsolla', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
    }
  }
})

test('manual switching updates chart and native surfaces without discarding a selected run', async ({ page }) => {
  await page.goto('/#/dashboard')
  await page.getByLabel('Run', { exact: true }).selectOption(runId)
  await expect(page.getByText('True positives: 114')).toBeVisible()
  const panel = page.locator('.decision-panel')
  const before = await panel.evaluate(element => getComputedStyle(element).backgroundColor)
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await expect(page.getByLabel('Run', { exact: true })).toHaveValue(runId)
  await expect(page.getByText('True positives: 114')).toBeVisible()
  expect(await panel.evaluate(element => getComputedStyle(element).backgroundColor)).not.toBe(before)
  await expect(page.getByLabel('Run', { exact: true })).toHaveCSS('color-scheme', 'dark')
})

test('toggle motion respects reduced-motion preference', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  await page.goto('/#/dashboard')
  await expect(page.locator('.theme-thumb')).toHaveCSS('transition-duration', '0.2s')
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const duration = await page.locator('.theme-thumb').evaluate(element => parseFloat(getComputedStyle(element).transitionDuration))
  expect(duration).toBeLessThanOrEqual(.001)
  await page.getByRole('button', { name: 'Dark mode' }).click()
  await expect(page.getByRole('button', { name: 'Dark mode' })).toHaveAttribute('aria-pressed', 'true')
})
