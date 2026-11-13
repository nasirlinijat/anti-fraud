import { useState } from 'react'
import { getModelStatus, type ModelMode, type ModelStatus } from '../api'
import { usePolling } from '../usePolling'
import { ErrorState } from '../ViewState'
import type { FieldErrors, ModelDraft, ModelThreshold } from './modelSettingsDraft'

const blankDraft: ModelDraft = { mode: 'assist', clear_below: '', flag_at: '' }

const modes: readonly (readonly [ModelMode, string])[] = [
  ['off', 'Off'], ['shadow', 'Shadow'], ['assist', 'Assist'],
]

const thresholds: readonly (readonly [ModelThreshold, string])[] = [
  ['clear_below', 'Clear review below'], ['flag_at', 'Flag for review at or above'],
]

export function ModelSettingsFields({ draft = blankDraft, errors, disabled, showEffectiveThresholds, onChange }: {
  draft?: ModelDraft
  errors: FieldErrors
  disabled: boolean
  showEffectiveThresholds: boolean
  onChange: (draft: ModelDraft) => void
}) {
  const [status, setStatus] = useState<ModelStatus | null>(null)
  const [statusError, setStatusError] = useState(false)
  const { retry, pending } = usePolling(getModelStatus, {
    onSuccess: result => { setStatus(result); setStatusError(false) },
    onError: () => setStatusError(true),
  })
  const useCurrentThresholds = showEffectiveThresholds && status?.loaded
  return (
    <fieldset disabled={disabled}>
      <legend>Fraud model</legend>
      {statusError && <ErrorState onRetry={disabled ? undefined : retry} pending={pending}>Could not check whether a model is loaded.</ErrorState>}
      {status?.loaded === false && <p className="muted small" role="status">No model is loaded.</p>}
      <div className="fields">
        <div className={errors['model.mode'] ? 'field invalid' : 'field'}>
          <label htmlFor="model.mode">Model mode</label>
          <select id="model.mode" value={draft.mode} aria-describedby={errors['model.mode'] ? 'model-mode-error' : 'model-mode-hint'}
            aria-invalid={errors['model.mode'] ? true : undefined}
            onChange={event => onChange({ ...draft, mode: event.target.value as ModelMode })}>
            {modes.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          {errors['model.mode'] && <span id="model-mode-error" className="error-text">{errors['model.mode']}</span>}
        </div>
      </div>
      <p id="model-mode-hint" className="field-hint">
        Off skips model scoring. Shadow records model risk. Assist can send purchases to review or clear a review.
      </p>
      <div className="fields">
        {thresholds.map(([field, label]) => (
          <ModelThresholdField key={field} field={field} label={label} value={draft[field]}
            fallback={useCurrentThresholds ? status[field] : null} error={errors[`model.${field}`]}
            onChange={value => onChange({ ...draft, [field]: value })} />
        ))}
      </div>
      {errors.model && <p className="error-text">{errors.model}</p>}
      <p id="model-threshold-hint" className="field-hint">Blank thresholds use the loaded model’s report.</p>
    </fieldset>
  )
}

function ModelThresholdField({ field, label, value, fallback, error, onChange }: {
  field: ModelThreshold
  label: string
  value: string
  fallback: number | null
  error?: string
  onChange: (value: string) => void
}) {
  const id = `model.${field}`
  return (
    <div className={error ? 'field invalid' : 'field'}>
      <label htmlFor={id}>{label}</label>
      <input id={id} type="text" inputMode="decimal" value={value} placeholder={fallback?.toString()}
        aria-invalid={error ? true : undefined} aria-describedby={error ? `${id}-error` : 'model-threshold-hint'}
        onChange={event => onChange(event.target.value)} />
      {error && <span id={`${id}-error`} className="error-text">{error}</span>}
    </div>
  )
}
