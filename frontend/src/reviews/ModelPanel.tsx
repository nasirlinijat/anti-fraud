import { useId, useState } from 'react'
import { getModelStatus, type ModelStatus } from '../api'
import { usePolling } from '../usePolling'
import { ErrorState, LoadingSkeleton } from '../ViewState'

const modeLabels: Record<ModelStatus['mode'], string> = { off: 'Off', shadow: 'Shadow', assist: 'Assist' }

export function ModelPanel() {
  const heading = useId()
  const [status, setStatus] = useState<ModelStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { retry, pending } = usePolling(getModelStatus, {
    onSuccess: data => { setStatus(data); setError(null) },
    onError: failure => setError(failure instanceof Error ? failure.message : 'Could not load the model status.'),
  })

  return (
    <section className="panel" aria-labelledby={heading}>
      <h2 id={heading}>Fraud model</h2>
      {error && <ErrorState onRetry={retry} pending={pending}>{error}</ErrorState>}
      {!status && !error && <LoadingSkeleton shape="case" label="Loading model status…" />}
      {status && <ModelDetails status={status} />}
    </section>
  )
}

function ModelDetails({ status }: { status: ModelStatus }) {
  const { loaded, mode, version, clear_below, flag_at, evaluation } = status
  return (
    <>
      {(!loaded || mode === 'off') && <p role="status">Model is off</p>}
      {loaded && <>
        <dl className="facts">
          <dt>Version</dt><dd>{version}</dd>
          <dt>Mode</dt><dd>{modeLabels[mode]}</dd>
          <dt>Clear review below</dt><dd>{clear_below}</dd>
          <dt>Flag for review at or above</dt><dd>{flag_at}</dd>
        </dl>
        <p className="muted small">Use model risk to prioritize purchases for review. It does not estimate the probability of fraud in live traffic.</p>
        {evaluation ? <>
          <h3>Training report</h3>
          <p className="muted small">Evaluation dataset: {evaluation.dataset}</p>
          <dl className="facts">
            <dt>ROC-AUC</dt><dd>{evaluation.roc_auc}</dd>
            <dt>PR-AUC</dt><dd>{evaluation.pr_auc}</dd>
          </dl>
        </> : <p className="muted small">Training report metrics are unavailable.</p>}
      </>}
    </>
  )
}
