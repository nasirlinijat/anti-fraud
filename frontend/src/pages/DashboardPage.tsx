import { lazy, Suspense, useCallback, useState } from 'react'
import { getStats, periods, type Period, type Stats } from '../api'
import { OutcomeBadge, TimeDisplayToggle, Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { usePolling } from '../usePolling'
import { useTimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

const DecisionChart = lazy(() => import('./DecisionChart'))

const REFRESH_MS = 5000

const periodNames: Record<Period, string> = {
  '15m': '15 minutes',
  '1h': '1 hour',
  '24h': '24 hours',
  '7d': '7 days',
  all: 'All time',
}

const ruleNames: Record<string, string> = {
  high_amount: 'High amount',
  card_velocity: 'Card velocity',
  new_account: 'New account',
  country_mismatch: 'Country mismatch',
}

const number = new Intl.NumberFormat()
const latency = new Intl.NumberFormat(undefined, { maximumFractionDigits: 3 })

function share(part: number, total: number): string {
  return total > 0 ? `${((part / total) * 100).toFixed(1)}%` : '–'
}

function DashboardPage() {
  const [period, setPeriod] = useState<Period>('24h')
  const [stats, setStats] = useState<Stats | null>(null)
  const [error, setError] = useState<string | null>(null)
  const time = useTimeDisplay()

  const load = useCallback((signal: AbortSignal) => getStats(period, signal), [period])
  const { stop, retry, pending } = usePolling(load, {
    intervalMs: REFRESH_MS,
    onSuccess: (result) => {
      setStats(result)
      setError(null)
    },
    onError: (error) => setError(error instanceof Error ? error.message : 'The statistics cannot be loaded.'),
  })

  const shown = stats?.period === period ? stats : null
  const outcomes = shown?.outcomes
  const maxFired = Math.max(1, ...(shown?.top_rules.map((r) => r.fired) ?? []))

  return (
    <div className="dashboard">
      <div className="dashboard-head">
        <h2>Dashboard</h2>
        <div className="periods" role="group" aria-label="Period">
          {periods.map((p) => (
            <button key={p} type="button" className={p === period ? '' : 'secondary'} aria-pressed={p === period} onClick={() => {
              if (p !== period) {
                stop()
                setError(null)
                setPeriod(p)
              }
            }}>
              {periodNames[p]}
            </button>
          ))}
        </div>
      </div>
      {error && (
        <ErrorState polite onRetry={retry} pending={pending}>
          {error} {shown && 'Showing the last successful update for this period. '}Retrying automatically.
        </ErrorState>
      )}
      {!shown && (!error || pending) && (
        <>
          <LoadingSkeleton shape="metrics" label="Loading dashboard…" />
          <LoadingSkeleton shape="chart" label="Loading decisions over time…" />
        </>
      )}
      {shown && (
        <div className="dashboard-update">
          <p className="muted small">
            {period === 'all' ? 'All decisions' : `Decisions made in the last ${periodNames[period]}`}.
            {' Updated '}<Timestamp value={shown.generated_at} display={time} />,
            refreshes every {REFRESH_MS / 1000} seconds.
          </p>
          <TimeDisplayToggle display={time} />
        </div>
      )}

      {outcomes && (
        <>
          <div className="tiles">
            <div className="tile">
              <span className="tile-label">Decisions</span>
              <span className="tile-value">{number.format(outcomes.total)}</span>
            </div>
            <div className="tile tile-allow">
              <span className="tile-label"><OutcomeBadge outcome="allow" /></span>
              <span className="tile-value">{number.format(outcomes.allow)}</span>
              <span className="tile-note">{share(outcomes.allow, outcomes.total)}</span>
            </div>
            <div className="tile tile-review">
              <span className="tile-label"><OutcomeBadge outcome="review" /></span>
              <span className="tile-value">{number.format(outcomes.review)}</span>
              <span className="tile-note">Review rate {share(outcomes.review, outcomes.total)}</span>
            </div>
            <div className="tile tile-block">
              <span className="tile-label"><OutcomeBadge outcome="block" /></span>
              <span className="tile-value">{number.format(outcomes.block)}</span>
              <span className="tile-note">Block rate {share(outcomes.block, outcomes.total)}</span>
            </div>
            <div className="tile">
              <span className="tile-label">p95 scoring latency</span>
              <span className="tile-value">
                {shown.p95_scoring_latency_us == null ? '–' : `${latency.format(shown.p95_scoring_latency_us / 1000)} ms`}
              </span>
            </div>
            <div className="tile">
              <span className="tile-label">Blocked value (USD)</span>
              <span className="tile-value tile-money">{formatUsdMinor(shown.blocked_value_minor)}</span>
            </div>
            <div className="tile">
              <span className="tile-label">Review queue now</span>
              <span className="tile-value">{number.format(shown.review_queue)}</span>
              <span className="tile-note">all time, waiting for an analyst</span>
            </div>
          </div>

          {shown.decision_series?.buckets.length > 0 && (
            <Suspense fallback={<LoadingSkeleton shape="chart" label="Loading decisions over time…" />}>
              <DecisionChart series={shown.decision_series} since={shown.since} until={shown.generated_at} />
            </Suspense>
          )}

          <div className="panels">
            <section className="panel">
              <h3>Most fired rules</h3>
              {shown.top_rules.length === 0 ? (
                <EmptyState>No rule fired in this period.</EmptyState>
              ) : (
                <ul className="bars">
                  {shown.top_rules.map((rule) => (
                    <li key={rule.name}>
                      <div className="bar-label">
                        <span>{ruleNames[rule.name] ?? rule.name}</span>
                        <span className="num">{number.format(rule.fired)}</span>
                      </div>
                      <div className="bar-track">
                        <div className="bar" style={{ width: `${(rule.fired / maxFired) * 100}%` }} />
                      </div>
                      <span className="muted small">
                        fired in {share(rule.fired, outcomes.total)} of decisions, {number.format(rule.points)} points in total
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <section className="panel">
              <h3>Fraud model</h3>
              {shown.model.scored === 0 ? (
                <EmptyState>No decision in this period has a model score. The backend may run with rules only.</EmptyState>
              ) : (
                <dl className="facts">
                  <dt>Scored by the model</dt>
                  <dd>{number.format(shown.model.scored)}</dd>
                  <dt>Sent to review by the model</dt>
                  <dd>
                    {number.format(shown.model.flagged)} <span className="muted small">the rules allowed them</span>
                  </dd>
                  <dt>Cleared by the model</dt>
                  <dd>
                    {number.format(shown.model.cleared)} <span className="muted small">the rules sent them to review</span>
                  </dd>
                </dl>
              )}
              <p className="muted small">The model can send a purchase to review or clear a review, but never blocks.</p>
            </section>
          </div>

          {outcomes.total === 0 && (
            <EmptyState>
              No decisions in this period yet. Choose another period, score a purchase in <a href="#/tester">Tester</a>, or run{' '}
              <code>make simulate</code>.
            </EmptyState>
          )}
        </>
      )}
    </div>
  )
}

export default DashboardPage
