import { type FormEvent, useCallback, useState } from 'react'
import { getActiveRuleset, getRuleset, listRulesets, previewRuleset, publishRuleset, type ErrorResponse, type RulesetPreview, type RuleSettings, type Ruleset, type RulesetConfig, type RulesetSummary } from '../api'
import { TimeDisplayToggle, Timestamp } from '../Display'
import { Button } from '../ui/Button'
import { PageHeader } from '../ui/PageHeader'
import { usePolling } from '../usePolling'
import { RulesetPreviewPanel } from './RulesetPreviewPanel'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'
import { ErrorState, LoadingSkeleton } from '../ViewState'
import { ModelSettingsFields } from './ModelSettingsFields'
import { toModelConfig, toModelDraft, type FieldErrors, type ModelDraft } from './modelSettingsDraft'

const ruleDetails = [
  {
    name: 'high_amount',
    label: 'High amount',
    description: 'Adds points when the purchase amount is at or above the limit.',
    limit: 'Minimum amount (minor units)',
  },
  {
    name: 'card_velocity',
    label: 'Card velocity',
    description: 'Adds points when the card already has at least the limit of purchases in the hour before.',
    limit: 'Earlier purchases in one hour',
  },
  {
    name: 'new_account',
    label: 'New account',
    description: 'Adds points when the account is younger than the limit.',
    limit: 'Maximum account age (hours)',
  },
  {
    name: 'country_mismatch',
    label: 'Country mismatch',
    description: 'Adds points when the IP country differs from the card country.',
  },
  {
    name: 'device_card_count',
    label: 'Device card count',
    description: 'Adds points when the device has used at least the limit of distinct cards in 60 minutes.',
    limit: 'Distinct cards in 60 minutes (including the current card)',
    preset: { enabled: false, weight: '20', limit: '5' },
  },
  {
    name: 'new_device_spike',
    label: 'New device spike',
    description: "Adds points when a large purchase comes from a device missing from the player's trusted purchase history.",
    limit: 'Minimum trusted purchases',
    preset: {
      enabled: false, weight: '50', limit: '3',
      window_days: '90', min_amount_minor: '1000', amount_multiplier: '3',
    },
  },
  {
    name: 'cvv_mismatch',
    label: 'CVV mismatch',
    description: "Adds points when the payment gateway reports that the security code did not match.",
    preset: { enabled: false, weight: '35' },
  },
  {
    name: 'avs_mismatch',
    label: 'AVS mismatch',
    description: 'Adds points when the payment gateway reports that the billing address did not match.',
    hint: 'Partial matches do not count.',
    preset: { enabled: false, weight: '15' },
  },
  {
    name: 'three_ds_failed',
    label: '3-D Secure failed',
    description: 'Adds points when the card issuer could not authenticate the cardholder.',
    preset: { enabled: false, weight: '50' },
  },
  {
    name: 'recent_password_reset',
    label: 'Recent password reset',
    description: 'Adds points when the player reset their password within the window before the purchase. Uses the account events the game reports.',
    limit: 'Window before the purchase (hours, 1 to 720)',
    preset: { enabled: false, weight: '30', limit: '24' },
  },
  {
    name: 'new_device_login',
    label: 'New device login',
    description: 'Adds points when a login within the window before the purchase was the player\'s first from a device that no earlier login or allowed or approved purchase used.',
    limit: 'Window before the purchase (hours, 1 to 720)',
    preset: { enabled: false, weight: '25', limit: '24' },
  },
  {
    name: 'new_country_login',
    label: 'New country login',
    description: 'Adds points when a login within the window before the purchase was the player\'s first from a country that no earlier login or allowed or approved purchase used.',
    limit: 'Window before the purchase (hours, 1 to 720)',
    preset: { enabled: false, weight: '20', limit: '24' },
  },
  {
    name: 'datacenter_ip',
    label: 'Data-centre IP address',
    description: 'Adds points when the IP address belongs to a hosting or data-centre network.',
    preset: { enabled: false, weight: '30' },
  },
  {
    name: 'anonymous_ip',
    label: 'VPN, proxy, or Tor IP address',
    description: 'Adds points when the IP address is a VPN, a public proxy, or a Tor exit.',
    preset: { enabled: false, weight: '20' },
  },
] as const

// Rules that read the player's account events take a window of 1 to 720 hours.
const accountEventRules: readonly string[] = ['recent_password_reset', 'new_device_login', 'new_country_login']

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
  model?: ModelDraft
}

function toDraft(ruleset: Ruleset): RulesetDraft {
  return {
    review_threshold: String(ruleset.review_threshold),
    block_threshold: String(ruleset.block_threshold),
    note: '',
    model: ruleset.model && toModelDraft(ruleset.model),
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

function limitRange(name: string): IntegerRange {
  if (accountEventRules.includes(name)) return { min: 1n, max: 720n }
  if (name === 'device_card_count' || name === 'new_device_spike') return { min: 1n, max: maxInt64 }
  return { min: 0n, max: maxInt64 }
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
    model: draft.model && toModelConfig(draft.model, errors),
    rules: {},
  }
  for (const [name, rule] of Object.entries(draft.rules)) {
    const field = `rules.${name}`
    const settings: RuleSettings = {
      enabled: rule.enabled,
      weight: Number(integer(rule.weight, `${field}.weight`, { min: 0n, max: maxInt32 }, errors)),
      limit: rule.limit === undefined ? undefined : integer(rule.limit, `${field}.limit`, limitRange(name), errors),
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
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ErrorResponse | null>(null)
  const [notice, setNotice] = useState('')
  const [preview, setPreview] = useState<RulesetPreview | null>(null)
  const [previewing, setPreviewing] = useState(false)
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
    setPreview(null)
    setDraft((current) => ({ ...current, ...change }))
  }

  const updateRule = (name: string, rule: RuleDraft) => {
    setPreview(null)
    setDraft((current) => ({ ...current, rules: { ...current.rules, [name]: rule } }))
  }

  const runPreview = async () => {
    if (previewing || saving || readOnly) return
    setNotice('')
    const errors: FieldErrors = {}
    const config = toConfig(draft, errors)
    if (Object.keys(errors).length) {
      setError({ error: 'Check the highlighted fields.', fields: errors })
      return
    }
    setError(null)
    setPreviewing(true)
    const result = await previewRuleset(config)
    setPreviewing(false)
    if (!result.ok) {
      setError(result.error)
      return
    }
    setPreview(result.value)
  }

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (saving || activeCheck.checking || readOnly) return
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
    const result = await publishRuleset(config)
    setSaving(false)
    if (!result.ok) {
      setError(result.error)
      return
    }
    setPreview(null)
    setDraft(toDraft(result.value))
    setVersion(result.value.version)
    selected.show(result.value)
    setNotice(`Published ruleset version ${result.value.version}.`)
    onPublished(result.value)
  }

  return (
    <div className="ruleset-layout">
      <div className="ruleset-main">
        <div className="version-bar">
          <span className="badge badge-accent">Active version {version}</span>
          {readOnly && <span className="badge badge-muted">Viewing version {selected.version} (read-only)</span>}
          {readOnly && (
            <Button variant="secondary" size="sm" onClick={() => selected.select(version)}>
              Edit active version ({version})
            </Button>
          )}
        </div>
        {readOnly && selected.loading && <LoadingSkeleton shape="form" label={`Loading version ${selected.version}…`} />}
        {readOnly && selected.error && (
          <ErrorState onRetry={selected.retry} pending={selected.loading}>{selected.error}</ErrorState>
        )}
        {(!readOnly || selected.ruleset !== null) && (
          <form onSubmit={save} noValidate>
            <ThresholdFields draft={displayDraft} errors={shownErrors} disabled={saving || readOnly} onChange={updateDraft} />
            <ModelSettingsFields draft={displayDraft.model} errors={shownErrors} disabled={saving || readOnly}
              showEffectiveThresholds={!readOnly && initial.model?.clear_below === null && initial.model.flag_at === null}
              onChange={model => updateDraft({ model })} />
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
                  onCheckActive={activeCheck.start} previewing={previewing} onPreview={() => void runPreview()} />
                {preview && <RulesetPreviewPanel preview={preview} time={time} />}
              </>
            )}
          </form>
        )}
        <p className={notice ? 'notice' : 'visually-hidden'} role="status">{notice}</p>
      </div>
      <VersionHistory history={history} time={time} activeVersion={version} selectedVersion={selected.version} onSelect={selected.select} />
    </div>
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
  const { name, label, description } = detail
  const limitLabel = 'limit' in detail ? detail.limit : undefined
  const hint = 'hint' in detail ? detail.hint : undefined
  const preset = 'preset' in detail ? detail.preset : undefined
  const rule: RuleDraft = savedRule ?? preset ?? { enabled: false, weight: '0', limit: limitLabel ? '0' : undefined }
  const field = `rules.${name}`
  const enabledError = errors[`${field}.enabled`]
  return (
    <fieldset disabled={disabled}>
      <legend>{label}</legend>
      <p className="muted small">{description}</p>
      <label className="switch ruleset-enabled">
        <input
          type="checkbox"
          checked={rule.enabled}
          onChange={(event) => onChange({ ...rule, enabled: event.target.checked })}
          aria-label={`Enable ${label}`}
          aria-invalid={enabledError ? true : undefined}
          aria-describedby={enabledError ? `${field}.enabled-error` : undefined}
        />
        <span>Enabled</span>
      </label>
      {hint && <p className="field-hint">{hint}</p>}
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

function PublishControls({ error, checkError, checkingActive, saving, onCheckActive, previewing, onPreview }: {
  error: ErrorResponse | null
  checkError: string | null
  checkingActive: boolean
  saving: boolean
  onCheckActive: () => void
  previewing: boolean
  onPreview: () => void
}) {
  const fields = error?.fields ?? {}
  return (
    <div className="publish-bar">
      {error && (
        <ErrorState retryLabel="Check active version" pending={checkingActive}
          onRetry={Object.keys(fields).length === 0 ? onCheckActive : undefined}>
          {error.error}
          {fields.rules && <p>{fields.rules}</p>}
          {checkError && <p>Could not check the active version. {checkError}</p>}
        </ErrorState>
      )}
      <p className="muted small">Publishing adds a new version and makes it the only active one. Stored versions never change. Preview impact replays recent decisions with this draft first, without saving anything.</p>
      <div className="publish-actions">
        <Button type="button" variant="secondary" disabled={previewing || saving} onClick={onPreview}>
          {previewing ? 'Previewing…' : 'Preview impact'}
        </Button>
        <Button type="submit" disabled={saving || checkingActive}>{saving ? 'Publishing…' : 'Publish new version'}</Button>
      </div>
    </div>
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
    <aside className="panel version-history" aria-labelledby="ruleset-history">
      <div className="panel-head">
        <h2 id="ruleset-history">Version history</h2>
        <TimeDisplayToggle display={time} />
      </div>
      <div className="table-scroll" role="region" tabIndex={0} aria-label="Ruleset data"><table>
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
                  className="btn-link"
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
      </table></div>
    </aside>
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
      <PageHeader eyebrow="Configuration" title="Rulesets"
        lead="Weighted rules add points to a purchase; the total is compared with the review and block thresholds. Publish a new version to change how future purchases are scored." />
      {data && <RulesetForm initial={data.active} history={data.history} time={time} onPublished={handlePublished} />}
      {!data && (!loadError || pending) && <LoadingSkeleton shape="form" label="Loading the active ruleset…" />}
      {loadError && (
        <ErrorState onRetry={retry} pending={pending}>{loadError}</ErrorState>
      )}
    </section>
  )
}

export default RulesetPage
