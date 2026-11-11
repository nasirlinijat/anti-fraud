import { type FormEvent, useCallback, useState } from 'react'
import { getActiveRuleset, getRuleset, listRulesets, publishRuleset, type ErrorResponse, type RuleSettings, type Ruleset, type RulesetConfig, type RulesetSummary } from '../api'
import { TimeDisplayToggle, Timestamp } from '../Display'
import { usePolling } from '../usePolling'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'
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
  note: string
  rules: Record<string, RuleDraft>
}
type FieldErrors = Record<string, string>

function toDraft(ruleset: Ruleset): RulesetDraft {
  return {
    review_threshold: String(ruleset.review_threshold),
    block_threshold: String(ruleset.block_threshold),
    note: '',
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

type IntegerRange = { min: bigint; max: bigint }

function integer(value: string, field: string, { min, max }: IntegerRange, errors: FieldErrors): bigint {
  const text = value.trim()
  if (/^\d+$/.test(text)) {
    const parsed = BigInt(text)
    if (parsed >= min && parsed <= max) return parsed
  }
  errors[field] = `Enter a whole number from ${min} to ${max}.`
  return 0n
}

function toConfig(draft: RulesetDraft, errors: FieldErrors): RulesetConfig {
  const note = draft.note.trim()
  if ([...note].length > 500) {
    errors.note = 'Enter at most 500 characters.'
  }
  const threshold = { min: 1n, max: maxInt32 }
  const config: RulesetConfig = {
    review_threshold: Number(integer(draft.review_threshold, 'review_threshold', threshold, errors)),
    block_threshold: Number(integer(draft.block_threshold, 'block_threshold', threshold, errors)),
    note,
    rules: {},
  }
  for (const [name, rule] of Object.entries(draft.rules)) {
    const field = `rules.${name}`
    const minLimit = name === 'device_card_count' || name === 'new_device_spike' ? 1n : 0n
    const settings: RuleSettings = {
      enabled: rule.enabled,
      weight: Number(integer(rule.weight, `${field}.weight`, { min: 0n, max: maxInt32 }, errors)),
      limit: rule.limit === undefined ? undefined : integer(rule.limit, `${field}.limit`, { min: minLimit, max: maxInt64 }, errors),
    }
    if (name === 'new_device_spike') {
      settings.window_days = Number(integer(rule.window_days ?? '', `${field}.window_days`, { min: 1n, max: 3650n }, errors))
      settings.min_amount_minor = integer(rule.min_amount_minor ?? '', `${field}.min_amount_minor`, { min: 1n, max: maxInt64 }, errors)
      settings.amount_multiplier = Number(integer(rule.amount_multiplier ?? '', `${field}.amount_multiplier`, { min: 2n, max: maxInt32 }, errors))
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

function RulesetForm({ initial, history, time, onPublished }: {
  initial: Ruleset
  history: RulesetSummary[]
  time: TimeDisplay
  onPublished: (ruleset: Ruleset) => void
}) {
  const [version, setVersion] = useState(initial.version)
  const [draft, setDraft] = useState(() => toDraft(initial))
  const [operatorKey, setOperatorKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ErrorResponse | null>(null)
  const [notice, setNotice] = useState('')
  const selected = useSelectedVersion(initial, version)
  const activeCheck = useActiveVersionCheck((active) => {
    setVersion(active.version)
    setError(null)
    setNotice(`Active version is ${active.version}. Your draft is still available.`)
  })
  const { readOnly } = selected
  // A read-only version shows no validation messages: they belong to the draft.
  const shownErrors: FieldErrors = readOnly ? {} : error?.fields ?? {}
  const displayDraft = readOnly && selected.ruleset ? toDraft(selected.ruleset) : draft

  const updateDraft = (change: Partial<RulesetDraft>) => {
    setDraft((current) => ({ ...current, ...change }))
  }

  const updateRule = (name: string, rule: RuleDraft) => {
    setDraft((current) => ({ ...current, rules: { ...current.rules, [name]: rule } }))
  }

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (saving || activeCheck.checking || readOnly || !operatorKey.trim()) return
    setNotice('')
    activeCheck.clearError()
    const errors: FieldErrors = {}
    const config = toConfig(draft, errors)
    if (Object.keys(errors).length) {
      setError({ error: 'Check the highlighted fields.', fields: errors })
      return
    }
    setError(null)
    setSaving(true)
    const result = await publishRuleset(config, operatorKey.trim())
    setSaving(false)
    if (!result.ok) {
      setError(result.error)
      return
    }
    setDraft(toDraft(result.value))
    setVersion(result.value.version)
    selected.show(result.value)
    setNotice(`Published ruleset version ${result.value.version}.`)
    onPublished(result.value)
  }

  return (
    <>
      <p>
        <strong>Active version {version}</strong>
        {readOnly && <> — viewing version {selected.version} (read-only)</>}
      </p>
      {readOnly && (
        <p>
          <button type="button" className="secondary" onClick={() => selected.select(version)}>
            Edit active version ({version})
          </button>
        </p>
      )}
      {readOnly && selected.loading && <LoadingSkeleton shape="form" label={`Loading version ${selected.version}…`} />}
      {readOnly && selected.error && (
        <ErrorState onRetry={selected.retry} pending={selected.loading}>{selected.error}</ErrorState>
      )}
      {(!readOnly || selected.ruleset !== null) && (
        <form onSubmit={save} noValidate>
          <ThresholdFields draft={displayDraft} errors={shownErrors} disabled={saving || readOnly} onChange={updateDraft} />
          {ruleDetails.map((detail) => (
            <RuleFieldset
              key={detail.name}
              detail={detail}
              savedRule={displayDraft.rules[detail.name]}
              errors={shownErrors}
              disabled={saving || readOnly}
              onChange={(rule) => updateRule(detail.name, rule)}
            />
          ))}

          {!readOnly && (
            <>
              <VersionNoteField note={draft.note} error={shownErrors.note} disabled={saving} onChange={(note) => updateDraft({ note })} />
              <PublishControls error={error} checkError={activeCheck.error} checkingActive={activeCheck.checking} saving={saving}
                operatorKey={operatorKey} onOperatorKey={setOperatorKey} onCheckActive={activeCheck.start} />
            </>
          )}
        </form>
      )}
      <p className="muted" role="status">{notice}</p>

      <VersionHistory history={history} time={time} activeVersion={version} selectedVersion={selected.version} onSelect={selected.select} />
    </>
  )
}

// The version shown in the form is the active one, which can be edited, or an
// earlier one, which is loaded on demand and shown read-only.
function useSelectedVersion(initial: Ruleset, activeVersion: number) {
  const [version, setVersion] = useState(initial.version)
  const [ruleset, setRuleset] = useState<Ruleset | null>(initial)
  const [error, setError] = useState<string | null>(null)
  const readOnly = version !== activeVersion

  const load = useCallback((signal: AbortSignal) => getRuleset(version, signal), [version])
  const { retry, pending: loading } = usePolling(readOnly && ruleset?.version !== version ? load : null, {
    onSuccess: (loaded) => {
      setRuleset(loaded)
      setError(null)
    },
    onError: (failure) => {
      setError(failure instanceof Error ? failure.message : 'Could not load this ruleset version.')
    },
  })

  const select = (target: number) => {
    setError(null)
    setVersion(target)
    if (target === activeVersion || ruleset?.version !== target) setRuleset(null)
  }

  const show = (published: Ruleset) => {
    setVersion(published.version)
    setRuleset(published)
  }

  return { version, ruleset, error, readOnly, loading, retry, select, show }
}

// Reads the active version once each time start is called. It backs the
// "Check active version" action shown after a publish is refused.
function useActiveVersionCheck(onActive: (active: Ruleset) => void) {
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState<string | null>(null)

  usePolling(checking ? getActiveRuleset : null, {
    onSuccess: (active) => {
      onActive(active)
      setError(null)
      setChecking(false)
    },
    onError: (failure) => {
      setError(failure instanceof Error ? failure.message : 'Could not check the active version.')
      setChecking(false)
    },
  })

  return { checking, error, start: () => setChecking(true), clearError: () => setError(null) }
}

function ThresholdFields({ draft, errors, disabled, onChange }: {
  draft: RulesetDraft
  errors: FieldErrors
  disabled: boolean
  onChange: (change: Partial<RulesetDraft>) => void
}) {
  return (
    <fieldset disabled={disabled}>
      <legend>Decision thresholds</legend>
      <div className="fields">
        <IntegerField
          name="review_threshold"
          label="Review threshold (points)"
          value={draft.review_threshold}
          error={errors.review_threshold}
          onChange={(review_threshold) => onChange({ review_threshold })}
        />
        <IntegerField
          name="block_threshold"
          label="Block threshold (points)"
          value={draft.block_threshold}
          error={errors.block_threshold}
          onChange={(block_threshold) => onChange({ block_threshold })}
        />
      </div>
    </fieldset>
  )
}

function RuleFieldset({ detail, savedRule, errors, disabled, onChange }: {
  detail: (typeof ruleDetails)[number]
  savedRule: RuleDraft | undefined
  errors: FieldErrors
  disabled: boolean
  onChange: (rule: RuleDraft) => void
}) {
  const { name, label } = detail
  const limitLabel = 'limit' in detail ? detail.limit : undefined
  const preset = 'preset' in detail ? detail.preset : undefined
  const rule: RuleDraft = savedRule ?? preset ?? { enabled: false, weight: '0', limit: limitLabel ? '0' : undefined }
  const field = `rules.${name}`
  const enabledError = errors[`${field}.enabled`]
  return (
    <fieldset disabled={disabled}>
      <legend>{label}</legend>
      <label className="ruleset-enabled">
        <input
          type="checkbox"
          checked={rule.enabled}
          onChange={(event) => onChange({ ...rule, enabled: event.target.checked })}
          aria-label={`Enable ${label.toLowerCase()}`}
          aria-invalid={enabledError ? true : undefined}
          aria-describedby={enabledError ? `${field}.enabled-error` : undefined}
        />
        Enabled
      </label>
      {enabledError && (
        <span id={`${field}.enabled-error`} className="error-text">{enabledError}</span>
      )}
      <div className="fields">
        <IntegerField
          name={`${field}.weight`}
          label="Weight (points)"
          value={rule.weight}
          error={errors[`${field}.weight`]}
          onChange={(weight) => onChange({ ...rule, weight })}
          disabled={!savedRule && !preset}
        />
        {limitLabel && (
          <IntegerField
            name={`${field}.limit`}
            label={limitLabel}
            value={rule.limit ?? ''}
            error={errors[`${field}.limit`]}
            onChange={(limit) => onChange({ ...rule, limit })}
            disabled={!savedRule && !preset}
          />
        )}
        {name === 'new_device_spike' && <SpikeFields field={field} rule={rule} errors={errors} onChange={onChange} />}
      </div>
      {errors[field] && <p className="error-text">{errors[field]}</p>}
    </fieldset>
  )
}

function SpikeFields({ field, rule, errors, onChange }: {
  field: string
  rule: RuleDraft
  errors: FieldErrors
  onChange: (rule: RuleDraft) => void
}) {
  return (
    <>
      <IntegerField
        name={`${field}.window_days`}
        label="Trusted history window (days)"
        value={rule.window_days ?? ''}
        error={errors[`${field}.window_days`]}
        onChange={(window_days) => onChange({ ...rule, window_days })}
      />
      <IntegerField
        name={`${field}.min_amount_minor`}
        label="Minimum purchase amount (minor units)"
        value={rule.min_amount_minor ?? ''}
        error={errors[`${field}.min_amount_minor`]}
        onChange={(min_amount_minor) => onChange({ ...rule, min_amount_minor })}
      />
      <IntegerField
        name={`${field}.amount_multiplier`}
        label="Minimum multiple of the trusted average"
        value={rule.amount_multiplier ?? ''}
        error={errors[`${field}.amount_multiplier`]}
        onChange={(amount_multiplier) => onChange({ ...rule, amount_multiplier })}
      />
    </>
  )
}

function VersionNoteField({ note, error, disabled, onChange }: {
  note: string
  error?: string
  disabled: boolean
  onChange: (note: string) => void
}) {
  return (
    <fieldset disabled={disabled}>
      <legend>Version note</legend>
      <div className={error ? 'field wide invalid' : 'field wide'}>
        <label htmlFor="note">Why this version was made (optional)</label>
        <input
          id="note"
          name="note"
          type="text"
          maxLength={500}
          value={note}
          onChange={(event) => onChange(event.target.value)}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? 'note-error' : undefined}
        />
        {error && <span id="note-error" className="error-text">{error}</span>}
      </div>
    </fieldset>
  )
}

function PublishControls({ error, checkError, checkingActive, saving, operatorKey, onOperatorKey, onCheckActive }: {
  error: ErrorResponse | null
  checkError: string | null
  checkingActive: boolean
  saving: boolean
  operatorKey: string
  onOperatorKey: (key: string) => void
  onCheckActive: () => void
}) {
  const fields = error?.fields ?? {}
  return (
    <>
      {error && (
        <ErrorState retryLabel="Check active version" pending={checkingActive}
          onRetry={Object.keys(fields).length === 0 ? onCheckActive : undefined}>
          {error.error}
          {fields.rules && <p>{fields.rules}</p>}
          {checkError && <p>Could not check the active version. {checkError}</p>}
        </ErrorState>
      )}
      <div className="field">
        <label htmlFor="ruleset-operator-key">Operator key</label>
        <input id="ruleset-operator-key" type="password" autoComplete="off" value={operatorKey}
          disabled={saving} onChange={(event) => onOperatorKey(event.target.value)} />
      </div>
      <button type="submit" disabled={saving || checkingActive || !operatorKey.trim()}>{saving ? 'Publishing…' : 'Publish new version'}</button>
    </>
  )
}

function VersionHistory({ history, time, activeVersion, selectedVersion, onSelect }: {
  history: RulesetSummary[]
  time: TimeDisplay
  activeVersion: number
  selectedVersion: number
  onSelect: (version: number) => void
}) {
  return (
    <>
      <h3>Version history</h3>
      <TimeDisplayToggle display={time} />
      <table>
        <thead>
          <tr>
            <th scope="col">Version</th>
            <th scope="col">Published</th>
            <th scope="col">Note</th>
          </tr>
        </thead>
        <tbody>
          {history.map((item) => (
            <tr key={item.version}>
              <th scope="row">
                <button
                  type="button"
                  className="review-open"
                  aria-current={item.version === selectedVersion ? 'true' : undefined}
                  onClick={() => onSelect(item.version)}
                >
                  Version {item.version}{item.version === activeVersion ? ' (active)' : ''}
                </button>
              </th>
              <td><Timestamp value={item.created_at} display={time} /></td>
              <td>{item.note || 'None'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  )
}

type RulesetPageData = {
  active: Ruleset
  history: RulesetSummary[]
}

async function loadRulesetPage(signal?: AbortSignal): Promise<RulesetPageData> {
  const [active, history] = await Promise.all([
    getActiveRuleset(signal),
    listRulesets(signal),
  ])
  return { active, history }
}

function RulesetPage() {
  const [data, setData] = useState<RulesetPageData | null>(null)
  const [loadError, setLoadError] = useState('')
  const time = useTimeDisplay()

  const { retry, pending } = usePolling(loadRulesetPage, {
    onSuccess: (loaded) => {
      setData(loaded)
      setLoadError('')
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'Could not load the ruleset.'),
  })

  const handlePublished = (published: Ruleset) => {
    setData((current) => current && {
      active: published,
      history: [
        { version: published.version, note: published.note, created_at: published.created_at },
        ...current.history.filter((item) => item.version !== published.version),
      ],
    })
  }

  return (
    <section className="ruleset-editor">
      <h2>Rulesets</h2>
      <p className="muted">Publish a new ruleset to change how future purchases are scored.</p>
      {data && <RulesetForm initial={data.active} history={data.history} time={time} onPublished={handlePublished} />}
      {!data && (!loadError || pending) && <LoadingSkeleton shape="form" label="Loading the active ruleset…" />}
      {loadError && (
        <ErrorState onRetry={retry} pending={pending}>{loadError}</ErrorState>
      )}
    </section>
  )
}

export default RulesetPage
