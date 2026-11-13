import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import type { Period, QualityReport, QualitySource, RecentPipeline, Stats } from '../src/api'
import type { Role } from '../src/session'

export const evidenceDirectory = process.env.AF_EVIDENCE_DIR ?? join(tmpdir(), 'af121-evidence')

export const now = '2026-11-12T12:00:00Z'
export const runId = 'demo-2026-11-12'

export function statistics(period: Period = '24h'): Stats {
  const buckets = Array.from({ length: 24 }, (_, index) => ({
    start: new Date(Date.parse(now) - (24 - index) * 3600000).toISOString(),
    allow: 180 + (index * 37) % 220, review: 8 + (index * 7) % 26, block: 4 + (index * 5) % 19,
  }))
  const sum = (key: 'allow' | 'review' | 'block') => buckets.reduce((total, bucket) => total + bucket[key], 0)
  return {
    period, since: buckets[0].start, generated_at: now,
    outcomes: { total: sum('allow') + sum('review') + sum('block'), allow: sum('allow'), review: sum('review'), block: sum('block') },
    blocklist_blocks: Math.round(sum('block') * 0.4), review_queue: 42, p95_scoring_latency_us: 41390, blocked_value_minor: '2876499',
    top_rules: [{ name: 'card_velocity', fired: 368, points: 18400 }, { name: 'high_amount', fired: 241, points: 12050 },
      { name: 'new_account', fired: 156, points: 4680 }, { name: 'country_mismatch', fired: 89, points: 1780 }],
    model: { scored: 6048, flagged: 124, cleared: 87 }, decision_series: { bucket_seconds: 3600, buckets },
  }
}

export function quality(source: QualitySource = 'simulator', run = runId): QualityReport {
  return { source, run_id: run, generated_at: now, labelled_decisions: 800, true_positives: 114, false_positives: 12,
    true_negatives: 656, false_negatives: 18, precision: 114 / 126, recall: 114 / 132, false_positive_rate: 12 / 668,
    fraud_value_flagged_minor: '876000', fraud_value_allowed_minor: '96000',
    coverage: { pending: 2, quarantined: 1, scored_decisions: 803, run_status: 'running' } }
}

export const newestPurchase = 81200

// Without a cursor the feed starts from now; after it, every read has traffic on every path.
export function recentPipeline(after: number | null): RecentPipeline {
  if (after === null) {
    return { after: null, next_after: newestPurchase, outcomes: { allow: 0, review: 0, block: 0, total: 0 }, blocklist_blocks: 0, model: { flagged: 0, cleared: 0 } }
  }
  return { after, next_after: after + 36, outcomes: { allow: 30, review: 4, block: 2, total: 36 }, blocklist_blocks: 1, model: { flagged: 2, cleared: 1 } }
}

export const sessionToken = 'synthetic-session-token'

// mockConsole answers the console's reads with synthetic data. By default an
// admin is already signed in; pass null to start at the sign-in screen.
export async function mockConsole(page: Page, role: Role | null = 'admin') {
  await page.clock.install({ time: new Date(now) })
  if (role) await page.addInitScript(token => window.localStorage.setItem('anti-fraud.session', token), sessionToken)
  await page.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (url.origin !== 'http://127.0.0.1:4173') return route.continue()
    const source = url.searchParams.get('source') === 'ieee-cis' ? 'ieee-cis' : 'simulator'
    if (url.pathname === '/ready') return route.fulfill({ status: 200, body: '' })
    if (url.pathname === '/auth/me') {
      const signedIn = route.request().headers().authorization === `Bearer ${sessionToken}`
      return signedIn
        ? route.fulfill({ json: { username: 'demo-user', role: role ?? 'admin' } })
        : route.fulfill({ status: 401, json: { error: 'missing or invalid session' } })
    }
    if (url.pathname === '/admin/accounts') return route.fulfill({ json: { accounts: [{ username: 'demo-user', role: 'admin', disabled: false, created_at: now }] } })
    if (url.pathname === '/admin/assignments') return route.fulfill({ json: { reviewers: [] } })
    if (url.pathname === '/stats/pipeline/recent') {
      const after = url.searchParams.get('after')
      return route.fulfill({ json: recentPipeline(after === null ? null : Number(after)) })
    }
    if (url.pathname === '/stats') return route.fulfill({ json: statistics((url.searchParams.get('period') ?? '24h') as Period) })
    if (url.pathname === '/stats/quality/runs') return route.fulfill({ json: { source, run_ids: [runId], next_cursor: url.searchParams.has('after') ? undefined : runId } })
    if (url.pathname === '/stats/quality') return route.fulfill({ json: quality(source, url.searchParams.get('run_id') ?? runId) })
    if (url.pathname === '/blocklist') return route.fulfill({ json: { entries: [] } })
    if (url.pathname.startsWith('/simulations')) return route.fulfill({ status: 503, json: { error: 'Simulations are disabled.' } })
    if (url.pathname.endsWith('/network/reviews')) return route.fulfill({ json: { window_days: 30, total: 0, items: [] } })
    if (url.pathname.startsWith('/reviews')) return route.fulfill({ json: { items: [], next_cursor: null, model_enabled: true } })
    if (url.pathname === '/rulesets/active') return route.fulfill({ json: { version: 3, created_at: now, note: 'Demo configuration', review_threshold: 30, block_threshold: 80, rules: {} } })
    if (url.pathname === '/rulesets') return route.fulfill({ json: { versions: [] } })
    return route.continue()
  })
}

// signIn goes through the sign-in form, for tests where no saved session can be restored.
export async function signIn(page: Page) {
  await page.route('**/auth/login', route => route.fulfill({
    json: { token: sessionToken, expires_at: '2026-11-13T00:00:00Z', account: { username: 'demo-user', role: 'admin' } },
  }))
  await page.getByLabel('Username', { exact: true }).fill('demo-user')
  await page.getByLabel('Password', { exact: true }).fill('synthetic-password')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
}
