import { expect, test } from '@playwright/test'
import { writeFile, mkdir } from 'node:fs/promises'
import { evidenceDirectory, mockConsole, runId } from './fixtures'

for (const colorScheme of ['light', 'dark'] as const) {
  test(`actual text, controls and focus combinations in ${colorScheme}`, async ({ page }, info) => {
    await page.emulateMedia({ colorScheme })
    await mockConsole(page)
    await page.goto('/#/dashboard')
    await page.getByLabel('Run', { exact: true }).selectOption(runId)
    await expect(page.getByText('True positives: 114')).toBeVisible()
    await expect(page.locator('.decision-legend-label').first()).toBeVisible()
    const period = page.getByRole('button', { name: '24 hours', exact: true })
    await period.focus()
    const samples = ['.tile-label', '.tile-note', '.outcome-allow', '.outcome-review', '.outcome-block',
      '.console-navigation a[aria-current]', '.console-navigation a:not([aria-current])', '.periods button[aria-pressed=true]',
      '.periods button[aria-pressed=false]', '.theme-toggle', '.theme-track', '.theme-thumb', '.quality-run-picker select', '.quality-sources label', '.decision-legend-label']
    const measurements = await page.evaluate(selectors => {
      const parse = (color: string) => color.match(/[\d.]+/g)!.map(Number)
      const blend = (front: number[], back: number[]) => front.slice(0, 3).map((channel, index) => channel * (front[3] ?? 1) + back[index] * (1 - (front[3] ?? 1)))
      function background(element: Element | null): number[] {
        if (!element) return [255, 255, 255]
        return blend(parse(getComputedStyle(element).backgroundColor), background(element.parentElement))
      }
      const luminance = (rgb: number[]) => rgb.map(channel => channel / 255).map(channel => channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4).reduce((total, channel, index) => total + channel * [.2126, .7152, .0722][index], 0)
      const ratio = (front: number[], back: number[]) => (Math.max(luminance(front), luminance(back)) + .05) / (Math.min(luminance(front), luminance(back)) + .05)
      return selectors.flatMap(selector => {
        const root = document.querySelector(selector)
        if (!root) throw new Error(`Missing contrast sample: ${selector}`)
        // XUI labels own their color independently of the button wrapper.
        const leaf = [...root.querySelectorAll('span')].findLast(child => child.childElementCount === 0 && child.textContent?.trim())
        const element = root.tagName === 'BUTTON' ? leaf ?? root : root
        const style = getComputedStyle(element)
        const bg = background(element)
        const pairs = [{ selector, foreground: style.color, background: bg, contrast: ratio(blend(parse(style.color), bg), bg), minimum: 4.5 }]
        if (element.tagName === 'SELECT' || element.matches('.theme-track, .theme-thumb')) pairs.push({ selector: `${selector} boundary`, foreground: style.borderTopColor, background: bg, contrast: ratio(blend(parse(style.borderTopColor), bg), bg), minimum: 3 })
        if (root.matches(':focus-visible')) {
          const outside = background(root.parentElement)
          const outline = getComputedStyle(root).outlineColor
          pairs.push({ selector: `${selector} focus`, foreground: outline, background: outside, contrast: ratio(blend(parse(outline), outside), outside), minimum: 3 })
        }
        return pairs
      })
    }, samples)
    for (const sample of measurements) expect(sample.contrast, sample.selector).toBeGreaterThanOrEqual(sample.minimum)
    await expect(period).toHaveCSS('outline-style', 'solid')
    await expect(period).toHaveCSS('outline-width', '2px')
    await mkdir(`${evidenceDirectory}/contrast`, { recursive: true })
    await writeFile(`${evidenceDirectory}/contrast/${info.project.name}-${colorScheme}.json`, JSON.stringify(measurements, null, 2))
  })
}
