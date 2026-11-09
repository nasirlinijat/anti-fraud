import { type FormEvent, useCallback, useEffect, useState } from 'react'
import { getActiveRuleset, publishRuleset, type ErrorResponse, type Ruleset, type RulesetConfig } from '../api'

const ruleDetails = [
  { name: 'high_amount', label: 'High amount', limit: 'Minimum amount (minor units)' },
  { name: 'card_velocity', label: 'Card velocity', limit: 'Earlier purchases in one hour' },
  { name: 'new_account', label: 'New account', limit: 'Maximum account age (hours)' },
  { name: 'country_mismatch', label: 'Country mismatch' },
] as const

const maxInt32 = 2_147_483_647n
const maxInt64 = 9_223_372_036_854_775_807n

type RuleDraft = { enabled: boolean; weight: string; limit?: string }
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
      { enabled: rule.enabled, weight: String(rule.weight), limit: rule.limit?.toString() },
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
    config.rules[name] = {
      enabled: rule.enabled,
      weight: Number(integer(rule.weight, `${field}.weight`, 0n, maxInt32, errors)),
      limit: rule.limit === undefined ? undefined : integer(rule.limit, `${field}.limit`, 0n, maxInt64, errors),
    }
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
  const [error, setError] = useState<ErrorResponse | null>(null)
  const [notice, setNotice] = useState('')
  const fields = error?.fields ?? {}

  const updateRule = (name: string, rule: RuleDraft) => {
    setDraft((current) => ({ ...current, rules: { ...current.rules, [name]: rule } }))
  }

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (saving) return
    setNotice('')
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
          const rule = savedRule ?? { enabled: false, weight: '0', limit: limitLabel ? '0' : undefined }
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
                  disabled={!savedRule}
                />
                {limitLabel && (
                  <IntegerField
                    name={`${field}.limit`}
                    label={limitLabel}
                    value={rule.limit ?? ''}
                    error={fields[`${field}.limit`]}
                    onChange={(limit) => updateRule(name, { ...rule, limit })}
                    disabled={!savedRule}
                  />
                )}
              </div>
              {fields[field] && <p className="error-text">{fields[field]}</p>}
            </fieldset>
          )
        })}
        {error && (
          <p className="alert" role="alert">
            {fields.rules ?? (Object.keys(fields).length ? 'Check the highlighted fields.' : error.error)}
          </p>
        )}
        <button type="submit" disabled={saving}>{saving ? 'Publishing…' : 'Publish new version'}</button>
      </form>
      <p className="muted" role="status">{notice}</p>
    </>
  )
}

function RulesetPage() {
  const [active, setActive] = useState<Ruleset | null>(null)
  const [loadError, setLoadError] = useState('')

  const load = useCallback(() => {
    getActiveRuleset()
      .then(setActive)
      .catch((error: unknown) => setLoadError(error instanceof Error ? error.message : 'Could not load the ruleset.'))
  }, [])

  useEffect(load, [load])

  return (
    <section className="ruleset-editor">
      <h2>Rulesets</h2>
      <p className="muted">Publish a new ruleset to change how future purchases are scored.</p>
      {active && <RulesetForm initial={active} />}
      {!active && !loadError && <p role="status">Loading the active ruleset…</p>}
      {loadError && (
        <>
          <p className="alert" role="alert">{loadError}</p>
          <button type="button" className="secondary" onClick={() => { setLoadError(''); load() }}>Retry</button>
        </>
      )}
    </section>
  )
}

export default RulesetPage
