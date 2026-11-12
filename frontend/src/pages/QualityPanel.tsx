import { Button } from '@xsolla/xui-button'
import { Typography } from '@xsolla/xui-typography'
import { useCallback, useId, useState } from 'react'
import { getQuality, listQualityRuns, type QualityReport, type QualitySource } from '../api'
import { formatQualityRate, qualityCoverage } from '../quality'
import { formatUsdMinor } from '../displayFormat'
import { usePolling } from '../usePolling'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { Timestamp } from '../Display'
import { useTimeDisplay } from '../useTimeDisplay'

const REFRESH_MS = 5000
const number = new Intl.NumberFormat()
const sourceLabels: Record<QualitySource, string> = { simulator: 'Simulator', 'ieee-cis': 'IEEE-CIS replay' }

export function QualityPanel() {
  const [source, setSource] = useState<QualitySource>('simulator')
  const id = useId()
  return (
    <section className="panel quality-panel">
      <p className="eyebrow">Evaluation</p><Typography as="h2" variant="h5" marginBottom={16}>Decision quality</Typography>
      <p className="muted small">{source === 'simulator' ? 'Simulated traffic only' : 'IEEE-CIS replay only'}.
        {' '}Review and block count as flagged. Results evaluate saved scoring decisions, before analyst decisions.</p>
      <fieldset className="quality-sources">
        <legend>Traffic source</legend>
        {(['simulator', 'ieee-cis'] as const).map(value => (
          <label key={value} htmlFor={`${id}-${value}`}>
            <input id={`${id}-${value}`} type="radio" name={`${id}-source`} value={value}
              checked={source === value} onChange={() => setSource(value)} /> {sourceLabels[value]}
          </label>
        ))}
      </fieldset>
      <SourceQuality key={source} source={source} />
    </section>
  )
}

function useQualityRuns(source: QualitySource) {
  const [after, setAfter] = useState('')
  const [ids, setIds] = useState<string[]>([])
  const [next, setNext] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const load = useCallback((signal: AbortSignal) => listQualityRuns(source, after, signal), [source, after])
  const polling = usePolling(load, {
    intervalMs: REFRESH_MS,
    onSuccess: page => {
      setIds(current => [...new Set([...current, ...page.run_ids])].sort())
      setNext(page.next_cursor ?? null)
      setLoaded(true)
      setError(null)
    },
    onError: failure => setError(failure instanceof Error ? failure.message : 'The runs cannot be loaded.'),
  })
  function more() {
    if (!next) return
    polling.stop()
    setAfter(next)
  }
  function refresh() {
    setAfter('')
    polling.refresh()
  }
  return { ids, loaded, error, next, more, refresh, polling }
}

function SourceQuality({ source }: { source: QualitySource }) {
  const runs = useQualityRuns(source)
  const [run, setRun] = useState('')
  const id = useId()
  return (
    <>
      {runs.error && <ErrorState polite onRetry={runs.refresh} pending={runs.polling.pending}>
        {runs.error} {runs.loaded && 'Showing the last successful run list.'}
      </ErrorState>}
      {!runs.loaded && !runs.error && <LoadingSkeleton shape="form" label="Loading evaluation runs…" />}
      {runs.loaded && runs.ids.length === 0 && <EmptyState>No evaluation runs for this source yet.</EmptyState>}
      {runs.ids.length > 0 && <div className="quality-run-picker">
        <div>
          <label htmlFor={id}>Run</label>
          <select id={id} value={run} onChange={event => setRun(event.target.value)}>
            <option value="">Select a run</option>
            {runs.ids.map(value => <option key={value} value={value}>{value}</option>)}
          </select>
        </div>
        <Button type="button" tone="mono" variant="secondary" onPress={runs.refresh} disabled={runs.polling.pending}>Refresh runs</Button>
        {runs.next && <Button type="button" tone="mono" variant="secondary" onPress={runs.more}
          disabled={runs.polling.pending}>Load more runs</Button>}
      </div>}
      {run && <p className="selected-run"><span className="muted">Selected run</span> <code>{run}</code></p>}
      {run && <QualityResult key={run} source={source} run={run} />}
    </>
  )
}

function QualityResult({ source, run }: { source: QualitySource; run: string }) {
  const [report, setReport] = useState<QualityReport | null>(null)
  const [error, setError] = useState<string | null>(null)
  const time = useTimeDisplay()
  const load = useCallback((signal: AbortSignal) => getQuality(source, run, signal), [source, run])
  const polling = usePolling(load, {
    intervalMs: REFRESH_MS,
    onSuccess: data => { setReport(data); setError(null) },
    onError: failure => setError(failure instanceof Error ? failure.message : 'The quality report cannot be loaded.'),
  })
  return (
    <div className="quality-result">
      {error && <ErrorState polite onRetry={polling.retry} pending={polling.pending}>
        {error} {report && 'Showing the last successful report for this run; it may be out of date.'} Retrying automatically.
      </ErrorState>}
      {!report && (!error || polling.pending) && <LoadingSkeleton shape="metrics" label="Loading decision quality…" />}
      {report && <>
        <p className="muted small">{qualityCoverage(report)}{report.coverage.run_status && ` Console run: ${report.coverage.run_status}.`}</p>
        <p className="muted small">Whole run, independent of the dashboard period. Updated{' '}
          <Timestamp value={report.generated_at} display={time} />. Refreshes every five seconds.</p>
        {report.labelled_decisions === 0 ? <EmptyState>No finalized labels for this run yet. Quality rates are not available.</EmptyState>
          : <QualityMetrics report={report} />}
      </>}
    </div>
  )
}

function QualityMetrics({ report }: { report: QualityReport }) {
  const rates = [
    ['Precision', report.precision, 'Of flagged purchases, the share that was fraud.'],
    ['Recall', report.recall, 'Of fraudulent purchases, the share flagged.'],
    ['False-positive rate', report.false_positive_rate, 'Of legitimate purchases, the share flagged.'],
  ] as const
  return (
    <>
      <div className="tiles">
        {rates.map(([label, value, hint]) => <div className="tile" key={label}>
          <span className="tile-label">{label}</span>
          <span className="tile-value quality-rate">{formatQualityRate(value)}</span>
          <span className="tile-note">{hint}</span>
        </div>)}
      </div>
      <ConfusionCounts report={report} />
      <dl className="facts">
        <dt>Fraud value flagged (USD)</dt><dd>{formatUsdMinor(report.fraud_value_flagged_minor)}</dd>
        <dt>Fraud value allowed (USD)</dt><dd>{formatUsdMinor(report.fraud_value_allowed_minor)}</dd>
      </dl>
      <p className="muted small">Flagged value includes purchases sent to review; it does not measure money recovered or prevented.</p>
    </>
  )
}

function ConfusionCounts({ report }: { report: QualityReport }) {
  return (
    <div className="table-scroll" tabIndex={0} role="region" aria-label="Saved decisions compared with known answers"><table className="quality-counts">
      <caption>Saved decisions compared with known answers</caption>
      <thead><tr><th scope="col">Known answer</th><th scope="col">Review or block</th><th scope="col">Allow</th></tr></thead>
      <tbody>
        <tr><th scope="row">Fraud</th><td>True positives: {number.format(report.true_positives)}</td>
          <td>False negatives: {number.format(report.false_negatives)}</td></tr>
        <tr><th scope="row">Legitimate</th><td>False positives: {number.format(report.false_positives)}</td>
          <td>True negatives: {number.format(report.true_negatives)}</td></tr>
      </tbody>
    </table></div>
  )
}
