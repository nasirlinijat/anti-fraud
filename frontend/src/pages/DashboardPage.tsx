import { lazy, Suspense, useEffect, useState } from 'react'
import { getStats, periods, type Period, type Stats } from '../api'

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

  // Poll every few seconds. A failed refresh keeps the last data on screen.
  useEffect(() => {
    const controller = new AbortController()
    let pending = false
    const load = () => {
      if (pending) return
      pending = true
      void getStats(period, controller.signal)
        .then((s) => {
          if (controller.signal.aborted) return
          setStats(s)
          setError(null)
        })
        .catch((e: unknown) => {
          if (!controller.signal.aborted) setError(e instanceof Error ? e.message : 'The statistics cannot be loaded.')
        })
        .finally(() => { pending = false })
    }
    load()
    const timer = window.setInterval(load, REFRESH_MS)
    return () => {
      controller.abort()
      window.clearInterval(timer)
    }
  }, [period])

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
                setError(null)
                setPeriod(p)
              }
            }}>
              {periodNames[p]}
            </button>
          ))}
        </div>
      </div>
      <p className="muted small" aria-live="polite">
        {error
          ? `${error} Retrying every ${REFRESH_MS / 1000} seconds.`
          : shown
            ? `${period === 'all' ? 'All decisions' : `Decisions made in the last ${periodNames[period]}`}. Updated ${new Date(shown.generated_at).toLocaleTimeString()}, refreshes every ${REFRESH_MS / 1000} seconds.`
            : 'Loading…'}
      </p>

      {outcomes && (
        <>
          <div className="tiles">
            <div className="tile">
              <span className="tile-label">Decisions</span>
              <span className="tile-value">{number.format(outcomes.total)}</span>
            </div>
            <div className="tile tile-allow">
              <span className="tile-label">Allowed</span>
              <span className="tile-value">{number.format(outcomes.allow)}</span>
              <span className="tile-note">{share(outcomes.allow, outcomes.total)}</span>
            </div>
            <div className="tile tile-review">
              <span className="tile-label">Sent to review</span>
              <span className="tile-value">{number.format(outcomes.review)}</span>
              <span className="tile-note">Review rate {share(outcomes.review, outcomes.total)}</span>
            </div>
            <div className="tile tile-block">
              <span className="tile-label">Blocked</span>
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
              <span className="tile-label">Blocked value (minor units)</span>
              <span className="tile-value tile-money">
                {shown.blocked_value_minor == null ? '–' : number.format(BigInt(shown.blocked_value_minor))}
              </span>
            </div>
            <div className="tile">
              <span className="tile-label">Review queue now</span>
              <span className="tile-value">{number.format(shown.review_queue)}</span>
              <span className="tile-note">all time, waiting for an analyst</span>
            </div>
          </div>

          {shown.decision_series?.buckets.length > 0 && (
            <Suspense fallback={<p className="muted">Loading decisions over time…</p>}>
              <DecisionChart series={shown.decision_series} since={shown.since} until={shown.generated_at} />
            </Suspense>
          )}

          <div className="panels">
            <section className="panel">
              <h3>Most fired rules</h3>
              {shown.top_rules.length === 0 ? (
                <p className="muted">No rule fired in this period.</p>
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
                <p className="muted">No decision in this period has a model score. The backend may run with rules only.</p>
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
            <p className="muted">
              No decisions in this period yet. Send purchases from the Tester page, or run the simulator with{' '}
              <code>make simulate</code>.
            </p>
          )}
        </>
      )}
    </div>
  )
}

export default DashboardPage
