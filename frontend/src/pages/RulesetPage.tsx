import { type FormEvent, useState } from 'react'
import { getActiveRuleset, publishRuleset, type ErrorResponse, type RuleSettings, type Ruleset, type RulesetConfig } from '../api'
import { usePolling } from '../usePolling'
import { ErrorState, LoadingSkeleton } from '../ViewState'

const ruleDetails = [
  { name: 'high_amount', label: 'High amount', limit: 'Minimum amount (minor units)' },
  { name: 'card_velocity', label: 'Card velocity', limit: 'Earlier purchases in one hour' },
  { name: 'new_account', label: 'New account', limit: 'Maximum account age (hours)' },
  { name: 'country_mismatch', label: 'Country mismatch' },
  {
    name: 'device_card_count',
    label: 'Device card count',
    limit: 'Distinct cards in 60 minutes (including the current card)',
    preset: { enabled: false, weight: '20', limit: '5' },
  },
  {
    name: 'new_device_spike',
    label: 'New device spike',
    limit: 'Minimum trusted purchases',
    preset: {
      enabled: false, weight: '50', limit: '3',
      window_days: '90', min_amount_minor: '1000', amount_multiplier: '3',
    },
  },
] as const

const maxInt32 = 2_147_483_647n
const maxInt64 = 9_223_372_036_854_775_807n

type RuleDraft = {
  enabled: boolean
  weight: string
  limit?: string
  window_days?: string
  min_amount_minor?: string
  amount_multiplier?: string
}
type RulesetDraft = {
  review_threshold: string
  block_threshold: string
  rules: Record<string, RuleDraft>
}
type FieldErrors = Record<string, string>

function toDraft(ruleset: Ruleset): RulesetDraft {
  return {
    review_threshold: String(ruleset.review_threshold),
    block_threshold: String(ruleset.block_threshold),
    rules: Object.fromEntries(Object.entries(ruleset.rules).map(([name, rule]) => [
      name,
      {
        enabled: rule.enabled, weight: String(rule.weight), limit: rule.limit?.toString(),
        window_days: rule.window_days?.toString(), min_amount_minor: rule.min_amount_minor?.toString(),
        amount_multiplier: rule.amount_multiplier?.toString(),
      },
    ])),
  }
}

function integer(value: string, field: string, min: bigint, max: bigint, errors: FieldErrors): bigint {
  const text = value.trim()
  if (/^\d+$/.test(text)) {
    const parsed = BigInt(text)
    if (parsed >= min && parsed <= max) return parsed
  }
  errors[field] = `Enter a whole number from ${min} to ${max}.`
  return 0n
}

function toConfig(draft: RulesetDraft, errors: FieldErrors): RulesetConfig {
  const config: RulesetConfig = {
    review_threshold: Number(integer(draft.review_threshold, 'review_threshold', 1n, maxInt32, errors)),
    block_threshold: Number(integer(draft.block_threshold, 'block_threshold', 1n, maxInt32, errors)),
    rules: {},
  }
  for (const [name, rule] of Object.entries(draft.rules)) {
    const field = `rules.${name}`
    const minLimit = name === 'device_card_count' || name === 'new_device_spike' ? 1n : 0n
    const settings: RuleSettings = {
      enabled: rule.enabled,
      weight: Number(integer(rule.weight, `${field}.weight`, 0n, maxInt32, errors)),
      limit: rule.limit === undefined ? undefined : integer(rule.limit, `${field}.limit`, minLimit, maxInt64, errors),
    }
    if (name === 'new_device_spike') {
      settings.window_days = Number(integer(rule.window_days ?? '', `${field}.window_days`, 1n, 3650n, errors))
      settings.min_amount_minor = integer(rule.min_amount_minor ?? '', `${field}.min_amount_minor`, 1n, maxInt64, errors)
      settings.amount_multiplier = Number(integer(rule.amount_multiplier ?? '', `${field}.amount_multiplier`, 2n, maxInt32, errors))
    }
    config.rules[name] = settings
  }
  return config
}

function IntegerField({ name, label, value, error, onChange, disabled }: {
  name: string
  label: string
  value: string
  error?: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  return (
    <div className={error ? 'field invalid' : 'field'}>
      <label htmlFor={name}>{label}</label>
      <input
        id={name}
        name={name}
        type="text"
        inputMode="numeric"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${name}-error` : undefined}
      />
      {error && <span id={`${name}-error`} className="error-text">{error}</span>}
    </div>
  )
}

function RulesetForm({ initial }: { initial: Ruleset }) {
  const [version, setVersion] = useState(initial.version)
  const [draft, setDraft] = useState(() => toDraft(initial))
  const [saving, setSaving] = useState(false)
  const [checkingActive, setCheckingActive] = useState(false)
  const [checkError, setCheckError] = useState<string | null>(null)
  const [error, setError] = useState<ErrorResponse | null>(null)
  const [notice, setNotice] = useState('')
  const fields = error?.fields ?? {}

  usePolling(checkingActive ? getActiveRuleset : null, {
    onSuccess: (active) => {
      setVersion(active.version)
      setError(null)
      setCheckError(null)
      setNotice(`Active version is ${active.version}. Your draft is still available.`)
      setCheckingActive(false)
    },
    onError: (failure) => {
      setCheckError(failure instanceof Error ? failure.message : 'Could not check the active version.')
      setCheckingActive(false)
    },
  })

  const updateRule = (name: string, rule: RuleDraft) => {
    setDraft((current) => ({ ...current, rules: { ...current.rules, [name]: rule } }))
  }

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (saving || checkingActive) return
    setNotice('')
    setCheckError(null)
    const errors: FieldErrors = {}
    const config = toConfig(draft, errors)
    if (Object.keys(errors).length) {
      setError({ error: 'Check the highlighted fields.', fields: errors })
      return
    }
    setError(null)
    setSaving(true)
    const result = await publishRuleset(config)
    setSaving(false)
    if (!result.ok) {
      setError(result.error)
      return
    }
    setDraft(toDraft(result.value))
    setVersion(result.value.version)
    setNotice(`Published ruleset version ${result.value.version}.`)
  }

  return (
    <>
      <p><strong>Active version {version}</strong></p>
      <form onSubmit={save} noValidate>
        <fieldset disabled={saving}>
          <legend>Decision thresholds</legend>
          <div className="fields">
            <IntegerField
              name="review_threshold"
              label="Review threshold (points)"
              value={draft.review_threshold}
              error={fields.review_threshold}
              onChange={(review_threshold) => setDraft((current) => ({ ...current, review_threshold }))}
            />
            <IntegerField
              name="block_threshold"
              label="Block threshold (points)"
              value={draft.block_threshold}
              error={fields.block_threshold}
              onChange={(block_threshold) => setDraft((current) => ({ ...current, block_threshold }))}
            />
          </div>
        </fieldset>

        {ruleDetails.map((detail) => {
          const { name, label } = detail
          const limitLabel = 'limit' in detail ? detail.limit : undefined
          const savedRule = draft.rules[name]
          const preset = 'preset' in detail ? detail.preset : undefined
          const rule: RuleDraft = savedRule ?? preset ?? { enabled: false, weight: '0', limit: limitLabel ? '0' : undefined }
          const field = `rules.${name}`
          return (
            <fieldset key={name} disabled={saving}>
              <legend>{label}</legend>
              <label className="ruleset-enabled">
                <input
                  type="checkbox"
                  checked={rule.enabled}
                  onChange={(event) => updateRule(name, { ...rule, enabled: event.target.checked })}
                  aria-label={`Enable ${label.toLowerCase()}`}
                  aria-invalid={fields[`${field}.enabled`] ? true : undefined}
                  aria-describedby={fields[`${field}.enabled`] ? `${field}.enabled-error` : undefined}
                />
                Enabled
              </label>
              {fields[`${field}.enabled`] && (
                <span id={`${field}.enabled-error`} className="error-text">{fields[`${field}.enabled`]}</span>
              )}
              <div className="fields">
                <IntegerField
                  name={`${field}.weight`}
                  label="Weight (points)"
                  value={rule.weight}
                  error={fields[`${field}.weight`]}
                  onChange={(weight) => updateRule(name, { ...rule, weight })}
                  disabled={!savedRule && !preset}
                />
                {limitLabel && (
                  <IntegerField
                    name={`${field}.limit`}
                    label={limitLabel}
                    value={rule.limit ?? ''}
                    error={fields[`${field}.limit`]}
                    onChange={(limit) => updateRule(name, { ...rule, limit })}
                    disabled={!savedRule && !preset}
                  />
                )}
                {name === 'new_device_spike' && (
                  <>
                    <IntegerField
                      name={`${field}.window_days`}
                      label="Trusted history window (days)"
                      value={rule.window_days ?? ''}
                      error={fields[`${field}.window_days`]}
                      onChange={(window_days) => updateRule(name, { ...rule, window_days })}
                    />
                    <IntegerField
                      name={`${field}.min_amount_minor`}
                      label="Minimum purchase amount (minor units)"
                      value={rule.min_amount_minor ?? ''}
                      error={fields[`${field}.min_amount_minor`]}
                      onChange={(min_amount_minor) => updateRule(name, { ...rule, min_amount_minor })}
                    />
                    <IntegerField
                      name={`${field}.amount_multiplier`}
                      label="Minimum multiple of the trusted average"
                      value={rule.amount_multiplier ?? ''}
                      error={fields[`${field}.amount_multiplier`]}
                      onChange={(amount_multiplier) => updateRule(name, { ...rule, amount_multiplier })}
                    />
                  </>
                )}
              </div>
              {fields[field] && <p className="error-text">{fields[field]}</p>}
            </fieldset>
          )
        })}
        {error && (
          <ErrorState retryLabel="Check active version" pending={checkingActive}
            onRetry={Object.keys(fields).length === 0 ? () => setCheckingActive(true) : undefined}>
            {error.error}
            {fields.rules && <p>{fields.rules}</p>}
            {checkError && <p>Could not check the active version. {checkError}</p>}
          </ErrorState>
        )}
        <button type="submit" disabled={saving || checkingActive}>{saving ? 'Publishing…' : 'Publish new version'}</button>
      </form>
      <p className="muted" role="status">{notice}</p>
    </>
  )
}

function RulesetPage() {
  const [active, setActive] = useState<Ruleset | null>(null)
  const [loadError, setLoadError] = useState('')

  const { retry, pending } = usePolling(getActiveRuleset, {
    onSuccess: (ruleset) => {
      setActive(ruleset)
      setLoadError('')
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'Could not load the ruleset.'),
  })

  return (
    <section className="ruleset-editor">
      <h2>Rulesets</h2>
      <p className="muted">Publish a new ruleset to change how future purchases are scored.</p>
      {active && <RulesetForm initial={active} />}
      {!active && (!loadError || pending) && <LoadingSkeleton shape="form" label="Loading the active ruleset…" />}
      {loadError && (
        <ErrorState onRetry={retry} pending={pending}>{loadError}</ErrorState>
      )}
    </section>
  )
}

export default RulesetPage
