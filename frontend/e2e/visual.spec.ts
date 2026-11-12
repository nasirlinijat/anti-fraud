import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { evidenceDirectory, mockConsole, runId } from './fixtures'

for (const colorScheme of ['light', 'dark'] as const) {
  for (const width of [390, 768, 1280]) {
    test(`dashboard ${colorScheme} ${width}`, async ({ page }, info) => {
      await page.emulateMedia({ colorScheme, reducedMotion: 'reduce' })
      await page.setViewportSize({ width, height: 960 })
      await mockConsole(page)
      await page.goto('/#/dashboard')
      await page.getByLabel('Run', { exact: true }).selectOption(runId)
      await expect(page.getByText('True positives: 114')).toBeVisible()
      await expect(page.getByRole('application', { name: 'Decisions over time' })).toBeVisible()
      await page.evaluate(() => document.fonts.ready)
      if (process.env.AF_SCREENSHOT_PHASE !== 'before') {
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      }
      const directory = `${evidenceDirectory}/${process.env.AF_SCREENSHOT_PHASE ?? 'after'}`
      await mkdir(directory, { recursive: true })
      await page.screenshot({ path: `${directory}/${info.project.name}-${colorScheme}-${width}.png`, fullPage: true })
    })
  }
}
