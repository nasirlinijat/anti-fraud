import { expect, test } from '@playwright/test'
import { mockConsole, sessionToken } from './fixtures'

const account = { username: 'demo-user', role: 'admin' }

test('a correct sign-in opens the console and survives a reload', async ({ page }) => {
  await mockConsole(page, null)
  await page.route('**/auth/login', async route => {
    expect(route.request().postDataJSON()).toEqual({ username: 'demo-user', password: 'synthetic-password' })
    await route.fulfill({ json: { token: sessionToken, expires_at: '2026-11-13T00:00:00Z', account } })
  })
  await page.goto('/#/blocklist')
  await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Pages' })).toHaveCount(0)
  await expect(page.getByLabel('Username', { exact: true })).toBeFocused()
  await page.getByLabel('Username', { exact: true }).fill('demo-user')
  await page.getByLabel('Password', { exact: true }).fill('synthetic-password')
  await page.getByLabel('Password', { exact: true }).press('Enter')
  await expect(page.locator('main h1')).toHaveText('Blocklist')
  await expect(page.getByText('Signed in as demo-user')).toBeAttached()
  await page.reload()
  await expect(page.locator('main h1')).toHaveText('Blocklist')
})

test('a wrong password and a throttled sign-in are explained without opening the console', async ({ page }) => {
  await mockConsole(page, null)
  let attempts = 0
  await page.route('**/auth/login', route => ++attempts === 1
    ? route.fulfill({ status: 401, json: { error: 'invalid username or password' } })
    : route.fulfill({ status: 429, headers: { 'Retry-After': '120' }, json: { error: 'too many sign-in attempts, try again later' } }))
  await page.goto('/')
  await page.getByLabel('Username', { exact: true }).fill('demo-user')
  await page.getByLabel('Password', { exact: true }).fill('wrong-password')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('That username and password do not match an active account.')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Too many sign-in attempts for this username. Try again in 2 minutes.')
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toHaveAttribute('aria-disabled', 'true')
  // The button stays focusable while it waits, so a forced click checks that it sends nothing.
  await page.getByRole('button', { name: 'Sign in', exact: true }).click({ force: true })
  expect(attempts).toBe(2)
  await page.clock.fastForward(121_000)
  await expect(page.getByRole('alert')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).not.toHaveAttribute('aria-disabled', 'true')
})

test('the password can be shown and hidden', async ({ page }) => {
  await mockConsole(page, null)
  await page.goto('/')
  const password = page.getByLabel('Password', { exact: true })
  await password.fill('synthetic-password')
  await expect(password).toHaveAttribute('type', 'password')
  await page.getByRole('button', { name: 'Show password' }).click()
  await expect(password).toHaveAttribute('type', 'text')
  await expect(page.getByRole('button', { name: 'Show password' })).toHaveAttribute('aria-pressed', 'true')
})

test('an ended session returns to sign-in with an explanation', async ({ page }) => {
  await mockConsole(page)
  await page.goto('/#/blocklist')
  await expect(page.locator('main h1')).toHaveText('Blocklist')
  await page.route('**/blocklist', route => route.fulfill({ status: 401, json: { error: 'missing or invalid session' } }))
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText('Your session has ended. Sign in again to continue.')
  expect(await page.evaluate(() => window.localStorage.getItem('anti-fraud.session'))).toBeNull()
})

test('signing out ends the session on the backend and in the browser', async ({ page }) => {
  await mockConsole(page)
  const ended: (string | undefined)[] = []
  await page.route('**/auth/logout', async route => {
    ended.push(route.request().headers().authorization)
    await route.fulfill({ status: 204 })
  })
  await page.goto('/#/dashboard')
  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText('You have signed out.')
  expect(ended).toEqual([`Bearer ${sessionToken}`])
  expect(await page.evaluate(() => window.localStorage.getItem('anti-fraud.session'))).toBeNull()
})

test('a reviewer sees only the review queue and is told when a page is not for their role', async ({ page }) => {
  await mockConsole(page, 'reviewer')
  await page.goto('/')
  await expect(page.locator('main h1')).toHaveText('Review queue')
  await expect(page.getByRole('navigation', { name: 'Pages' }).getByRole('link')).toHaveText(['Review queue'])
  await page.goto('/#/accounts')
  await expect(page.locator('main h1')).toHaveText('This page is not available for your role')
  await page.getByRole('link', { name: 'Go to Review queue' }).click()
  await expect(page.locator('main h1')).toHaveText('Review queue')
})

test('an admin creates an account and disables another after confirmation', async ({ page }) => {
  await mockConsole(page)
  const accounts = [
    { username: 'demo-user', role: 'admin', disabled: false, created_at: '2026-11-10T10:00:00Z' },
    { username: 'demo-reviewer', role: 'reviewer', disabled: false, created_at: '2026-11-11T10:00:00Z' },
  ]
  await page.route('**/admin/accounts', async route => {
    if (route.request().method() !== 'POST') return route.fulfill({ json: { accounts } })
    const body = route.request().postDataJSON()
    if (body.password.length < 12) {
      return route.fulfill({ status: 400, json: { error: 'invalid account', fields: { password: 'password must be 12 to 72 bytes' } } })
    }
    const created = { username: body.username, role: body.role, disabled: false, created_at: '2026-11-12T12:00:00Z' }
    accounts.push(created)
    return route.fulfill({ status: 201, json: created })
  })
  await page.route('**/admin/accounts/demo-reviewer/disable', async route => {
    accounts[1].disabled = true
    await route.fulfill({ json: accounts[1] })
  })
  await page.goto('/#/accounts')
  await expect(page.getByRole('button', { name: 'Disable demo-user' })).toHaveCount(0)
  await page.getByLabel('Username', { exact: true }).fill('new-reviewer')
  await page.getByLabel('Password', { exact: true }).fill('short')
  await page.getByRole('button', { name: 'Create account' }).click()
  await expect(page.getByText('password must be 12 to 72 bytes')).toBeVisible()
  await page.getByLabel('Password', { exact: true }).fill('synthetic-password')
  await page.getByRole('button', { name: 'Create account' }).click()
  await expect(page.getByRole('rowheader', { name: 'new-reviewer' })).toBeVisible()
  await page.getByRole('button', { name: 'Disable demo-reviewer' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Disable account' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'demo-reviewer is disabled' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Disable demo-reviewer' })).toHaveCount(0)
})

test('an admin releases a reviewer claim after confirmation', async ({ page }) => {
  await mockConsole(page)
  let released = false
  const claim = { purchase_id: '103', amount_minor: '46099', rule_score: 52, model_score: 0.58, claimed_at: '2026-11-12T11:50:00Z' }
  await page.route('**/admin/assignments', route => route.fulfill({ json: { reviewers: [
    { username: 'demo-reviewer', role: 'reviewer', disabled: false, decided: 42, open_claims: released ? [] : [claim] },
  ] } }))
  await page.route('**/reviews/103/claim', async route => {
    expect(route.request().method()).toBe('DELETE')
    released = true
    await route.fulfill({ status: 204 })
  })
  await page.goto('/#/assignments')
  await expect(page.getByRole('cell', { name: '$460.99' })).toBeVisible()
  await page.getByRole('button', { name: 'Release purchase 103' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Release claim' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Purchase 103 is back in the shared queue' })).toBeVisible()
  await expect(page.getByText('No open claims.')).toBeVisible()
})
