import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import {
  getSimulation,
  listSimulations,
  startSimulation,
  stopSimulation,
  type SimulationHistory,
  type SimulationRun,
  type SimulationSettings,
} from '../api'
import { ApiError } from '../request'
import {
  defaultSimulationDraft,
  isActive,
  scenarioLabel,
  uncertainStart,
  validateSimulation,
  type SimulationDraft,
} from '../simulation'
import { TimeDisplayToggle, Timestamp } from '../Display'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { usePolling } from '../usePolling'
import { useTimeDisplay } from '../useTimeDisplay'

type Attempt = { settings: SimulationSettings; operatorKey: string }
const number = new Intl.NumberFormat('en-US')
const statusNames = {
  starting: 'Starting',
  running: 'Running',
  stopping: 'Stopping…',
  completed: 'Completed',
  stopped: 'Stopped',
  failed: 'Failed',
  interrupted: 'Interrupted',
}

export default function SimulatorPage() {
  const [draft, setDraft] = useState<SimulationDraft>(defaultSimulationDraft)
  const [operatorKey, setOperatorKey] = useState('')
  const [history, setHistory] = useState<SimulationHistory | null>(null)
  const [run, setRun] = useState<SimulationRun | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [disabled, setDisabled] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [writeError, setWriteError] = useState<string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [notice, setNotice] = useState('')
  const [conflictId, setConflictId] = useState<string | null>(null)
  const [attempt, setAttempt] = useState<Attempt | null>(null)
  const [writing, setWriting] = useState(false)
  const [pendingStopId, setPendingStopId] = useState<string | null>(null)
  const [checkedAt, setCheckedAt] = useState<string | null>(null)
  const [missing, setMissing] = useState(false)
  const busy = useRef(false)
  const mutation = useRef<AbortController | null>(null)
  const form = useRef<HTMLFormElement>(null)
  const time = useTimeDisplay()

  useEffect(() => () => mutation.current?.abort(), [])
  const load = useCallback(
    async (signal: AbortSignal) => {
      const page = await listSimulations(signal)
      let selected = selectedId
        ? page.items.find((item) => item.id === selectedId)
        : (page.items.find((item) => isActive(item.status)) ?? page.items[0])
      let missingRun = false
      if (selectedId && !selected) {
        try {
          selected = await getSimulation(selectedId, signal)
        } catch (error) {
          if (error instanceof ApiError && error.status === 404) missingRun = true
          else throw error
        }
      }
      return { page, selected: selected ?? null, missingRun }
    },
    [selectedId],
  )
  const polling = usePolling(!disabled && !writing ? load : null, {
    intervalMs: 3000,
    waitAfterCompletion: true,
    onSuccess: ({ page, selected, missingRun }) => {
      setHistory(page)
      setRun(selected)
      setMissing(missingRun)
      setLoadError(null)
      setCheckedAt(new Date().toISOString())
      const stopped =
        pendingStopId &&
        (page.items.find((item) => item.id === pendingStopId) ??
          (selected?.id === pendingStopId ? selected : null))
      if (stopped) {
        setPendingStopId(null)
        if (!isActive(stopped.status)) {
          setWriteError(null)
          setNotice(`Stop outcome confirmed: run ${statusNames[stopped.status].toLowerCase()}.`)
        }
      }
      const conflict =
        conflictId &&
        (page.items.find((item) => item.id === conflictId) ?? (selected?.id === conflictId ? selected : null))
      if (conflict && !isActive(conflict.status)) {
        setConflictId(null)
        setWriteError(null)
        setNotice('The other run has finished. You can start a new run.')
      }
      const confirmed =
        attempt && page.items.find((item) => item.settings.request_id === attempt.settings.request_id)
      if (confirmed) {
        setAttempt(null)
        setSelectedId(confirmed.id)
        setRun(confirmed)
        setWriteError(null)
        setNotice('The start is confirmed. Following the saved run.')
      }
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 404) {
        setDisabled(true)
        setLoadError(null)
      } else setLoadError(error instanceof Error ? error.message : 'Simulation progress cannot be loaded.')
    },
  })
  const active = history?.items.find((item) => isActive(item.status))
  // A write response can precede its first appearance in history.
  const activeId = active?.id ?? (run && isActive(run.status) ? run.id : null)
  const available = history?.available_scenarios ?? []
  const locked = writing || attempt !== null

  function change(name: keyof SimulationDraft, value: string) {
    const nextDraft = { ...draft, [name]: value }
    setDraft(nextDraft)
    if (!validateSimulation(nextDraft, available).fields[name])
      setFields((current) => {
        const next = { ...current }
        delete next[name]
        return next
      })
  }
  function follow(id: string) {
    polling.stop()
    setSelectedId(id)
    setMissing(false)
    if (conflictId === id) setWriteError(null)
    setConflictId(null)
    polling.refresh()
  }
  function recheck() {
    setDisabled(false)
    polling.refresh()
  }

  async function launch(saved: Attempt) {
    if (busy.current) return
    busy.current = true
    polling.stop()
    setWriting(true)
    setWriteError(null)
    setConflictId(null)
    setNotice('')
    const controller = new AbortController()
    mutation.current = controller
    try {
      const result = await startSimulation(saved.settings, saved.operatorKey, controller.signal)
      if (controller.signal.aborted) return
      setRun(result)
      setSelectedId(result.id)
      setAttempt(null)
      setFields({})
      setNotice('Start confirmed. Following the saved run.')
    } catch (error) {
      if (controller.signal.aborted) return
      if (error instanceof ApiError && error.fields) {
        setFields(
          Object.fromEntries(
            Object.entries(error.fields).map(([name, value]) => [
              name === 'scenarios' ? 'scenario' : name,
              value,
            ]),
          ),
        )
      }
      if (error instanceof ApiError && error.status === 409) {
        setAttempt(null)
        setConflictId(error.activeRunId ?? null)
        setWriteError(
          error.activeRunId
            ? 'Another run is active. View it to follow progress or stop it.'
            : `${error.message} Refresh recent runs before trying again.`,
        )
      } else if (uncertainStart(error)) {
        setAttempt(saved)
        setWriteError(
          'The start could not be confirmed. It may already be running. Check recent runs or retry this same start with the original operator key.',
        )
      } else {
        setAttempt(null)
        setWriteError(
          error instanceof ApiError && error.status === 401
            ? 'The operator key was rejected. Check it and try again.'
            : error instanceof Error
              ? error.message
              : 'The run could not be started.',
        )
      }
    } finally {
      busy.current = false
      if (!controller.signal.aborted) {
        setWriting(false)
        polling.refresh()
      }
    }
  }
  function submit(event: FormEvent) {
    event.preventDefault()
    if (busy.current || attempt || disabled || !history || loadError || activeId) return
    const validated = validateSimulation(draft, available)
    if (!operatorKey.trim()) validated.fields.operator_key = 'Enter your operator key to start a run.'
    setFields(validated.fields)
    if (Object.keys(validated.fields).length || !validated.settings) {
      const name = Object.keys(validated.fields)[0]
      form.current?.querySelector<HTMLElement>(`[name="${name}"]`)?.focus()
      return
    }
    const saved = {
      settings: { ...validated.settings, request_id: crypto.randomUUID() },
      operatorKey: operatorKey.trim(),
    }
    setAttempt(saved)
    void launch(saved)
  }
  async function stopRun() {
    if (!run || !isActive(run.status) || run.status === 'stopping' || pendingStopId || busy.current) return
    if (!operatorKey.trim()) {
      setWriteError('Enter your operator key to stop this run.')
      return
    }
    busy.current = true
    polling.stop()
    setWriting(true)
    setWriteError(null)
    setNotice('')
    const controller = new AbortController()
    mutation.current = controller
    try {
      const result = await stopSimulation(run.id, operatorKey.trim(), controller.signal)
      if (controller.signal.aborted) return
      setRun(result)
      setPendingStopId(null)
      setNotice('Stop request confirmed.')
    } catch (error) {
      if (controller.signal.aborted) return
      if (!(error instanceof ApiError && error.status === 401)) setPendingStopId(run.id)
      setWriteError(
        error instanceof ApiError && error.status === 401
          ? 'The operator key was rejected. The run may still be active.'
          : 'The stop could not be confirmed. Refreshing progress; you can safely request Stop again if it is still active.',
      )
    } finally {
      busy.current = false
      if (!controller.signal.aborted) {
        setWriting(false)
        polling.refresh()
      }
    }
  }
  function numeric(name: 'seed' | 'rate' | 'count', label: string, hint: string, min: number, max: number) {
    return (
      <div className={fields[name] ? 'field invalid' : 'field'}>
        <label htmlFor={`simulation-${name}`}>{label}</label>
        <span id={`simulation-${name}-hint`} className="muted small">
          {hint}
        </span>
        <input
          id={`simulation-${name}`}
          name={name}
          type={name === 'seed' ? 'text' : 'number'}
          inputMode={name === 'rate' ? 'decimal' : 'numeric'}
          min={min}
          max={max}
          step={name === 'rate' ? 'any' : 1}
          pattern={name === 'seed' ? '[0-9]+' : undefined}
          required
          value={draft[name]}
          disabled={locked}
          onChange={(event) => change(name, event.target.value)}
          onBlur={() => {
            const error = validateSimulation(draft, available).fields[name]
            if (error) setFields((current) => ({ ...current, [name]: error }))
          }}
          aria-invalid={Boolean(fields[name])}
          aria-describedby={`simulation-${name}-hint${fields[name] ? ` simulation-${name}-error` : ''}`}
        />
        {fields[name] && (
          <span id={`simulation-${name}-error`} className="error-text">
            {fields[name]}
          </span>
        )}
      </div>
    )
  }

  return (
    <div className="simulator">
      <h2>Simulator</h2>
      <p className="muted">
        Send simulated purchases through scoring and follow the actual decisions. Runs keep going when you
        leave this page.
      </p>
      {disabled && (
        <div className="panel" role="status">
          <h3>Simulations are disabled or unavailable</h3>
          <p>
            Ask the operator to enable <code>SIMULATION_ENABLED</code> on this backend and restart it.
          </p>
          <button type="button" className="secondary" onClick={recheck}>
            Check availability
          </button>
        </div>
      )}
      {loadError && (
        <ErrorState onRetry={polling.retry} pending={polling.pending} polite>
          {loadError}
          {history && ' Showing the last successful snapshot; these counts may be out of date.'}
        </ErrorState>
      )}
      {!history && !disabled && (!loadError || polling.pending) && (
        <LoadingSkeleton shape="form" label="Loading simulation controls…" />
      )}
      <div className="simulator-layout">
        <form
          ref={form}
          className="panel simulator-form"
          aria-labelledby="simulation-settings-title"
          noValidate
          onSubmit={submit}
        >
          <h3 id="simulation-settings-title">Start a run</h3>
          <fieldset disabled={locked || disabled || !history}>
            <legend>Traffic settings</legend>
            <div className="field">
              <label htmlFor="simulation-scenario">Scenario</label>
              <select
                id="simulation-scenario"
                name="scenario"
                value={draft.scenario}
                onChange={(event) => change('scenario', event.target.value)}
                aria-invalid={Boolean(fields.scenario)}
                aria-describedby={fields.scenario ? 'simulation-scenario-error' : undefined}
              >
                <option value="mixed">Mixed traffic</option>
                {available.map((name) => (
                  <option key={name} value={name}>
                    {scenarioLabel(name)}
                  </option>
                ))}
              </select>
              {fields.scenario && (
                <span id="simulation-scenario-error" className="error-text">
                  {fields.scenario}
                </span>
              )}
              <p className="muted small">
                Mixed traffic includes normal purchases, legitimate edge cases and suspicious patterns. A
                scenario can produce several purchases; outcomes depend on rules, model and history.
              </p>
            </div>
            <fieldset className="simulation-modes">
              <legend>Mode</legend>
              {(['live', 'backfill'] as const).map((mode) => (
                <label key={mode}>
                  <input
                    type="radio"
                    name="mode"
                    value={mode}
                    checked={draft.mode === mode}
                    onChange={() => change('mode', mode)}
                  />
                  {mode === 'live' ? 'Live — until stopped' : 'Backfill — fixed purchase count'}
                </label>
              ))}
            </fieldset>
            <div className="simulation-numbers">
              {numeric(
                'seed',
                'Seed',
                'Whole number; determines the traffic sequence.',
                0,
                Number.MAX_SAFE_INTEGER,
              )}
              {numeric(
                'rate',
                'Rate (purchases/second)',
                'From 1 to 50; fractional rates are allowed.',
                1,
                50,
              )}
              {draft.mode === 'backfill' &&
                numeric('count', 'Purchase count', 'From 1 to 10,000 purchases.', 1, 10000)}
            </div>
          </fieldset>
          <div className={fields.operator_key ? 'field invalid' : 'field'}>
            <label htmlFor="simulation-operator-key">Operator key</label>
            <input
              id="simulation-operator-key"
              name="operator_key"
              type="password"
              value={operatorKey}
              disabled={writing || disabled}
              onChange={(event) => {
                setOperatorKey(event.target.value)
                setFields((current) => {
                  const next = { ...current }
                  delete next.operator_key
                  return next
                })
              }}
              aria-invalid={Boolean(fields.operator_key)}
              aria-describedby={fields.operator_key ? 'simulation-key-error' : 'simulation-key-hint'}
            />
            {fields.operator_key ? (
              <span id="simulation-key-error" className="error-text">
                {fields.operator_key}
              </span>
            ) : (
              <span id="simulation-key-hint" className="muted small">
                Required to start or stop. Kept in memory for this page only.
              </span>
            )}
          </div>
          <div className="simulation-actions">
            <button
              type="submit"
              disabled={locked || disabled || !history || Boolean(loadError) || Boolean(activeId)}
            >
              {writing ? 'Working…' : 'Start run'}
            </button>
          </div>
          {activeId && (
            <p className="muted small">
              A run is already active.{' '}
              <button className="secondary" type="button" onClick={() => follow(activeId)}>
                View active run
              </button>
            </p>
          )}
        </form>
        <section className="panel simulation-progress" aria-labelledby="simulation-progress-title">
          <h3 id="simulation-progress-title">Run progress</h3>
          {missing && (
            <ErrorState>
              The selected run is no longer available. Start a new run or choose another recent run.
            </ErrorState>
          )}
          {!run && !missing && history && (
            <EmptyState>No runs yet. Choose settings and start a run.</EmptyState>
          )}
          {run && (
            <>
              <div className="simulation-run-head">
                <strong className={`simulation-status simulation-${run.status}`}>
                  {statusNames[run.status]}
                </strong>
                <code className="simulation-id">{run.id}</code>
              </div>
              <p className="muted small">
                {run.settings.mode === 'live'
                  ? 'Live'
                  : `Backfill · ${number.format(run.settings.count)} purchases`}{' '}
                · Seed {run.settings.seed} · {run.settings.rate}/second
                <br />
                {run.settings.scenarios.map(scenarioLabel).join(', ')}
              </p>
              <div className="tiles simulation-tiles">
                {(
                  [
                    ['Sent', run.sent, ''],
                    ['Allowed', run.allowed, 'tile-allow'],
                    ['Reviewed', run.reviewed, 'tile-review'],
                    ['Blocked', run.blocked, 'tile-block'],
                  ] as const
                ).map(([label, value, className]) => (
                  <div className={`tile ${className}`} key={label}>
                    <span className="tile-label">{label}</span>
                    <strong className="tile-value">{number.format(value)}</strong>
                  </div>
                ))}
              </div>
              {run.failed > 0 && <p className="error-text">Failed: {number.format(run.failed)}</p>}
              {run.pending && !isActive(run.status) && (
                <ErrorState polite>
                  The last purchase outcome is still unconfirmed. These counts may be incomplete.
                </ErrorState>
              )}
              {run.error && <p className="error-text">{run.error}</p>}
              {run.status === 'interrupted' && (
                <p>
                  The backend interrupted this run. Its history is saved; starting again creates a new run.
                </p>
              )}
              <TimeDisplayToggle display={time} />
              <p className="muted small">
                Created <Timestamp value={run.created_at} display={time} />
                {run.finished_at && (
                  <>
                    {' '}
                    · Finished <Timestamp value={run.finished_at} display={time} />
                  </>
                )}
              </p>
              <button
                type="button"
                className="secondary"
                disabled={
                  writing ||
                  disabled ||
                  pendingStopId === run.id ||
                  !isActive(run.status) ||
                  run.status === 'stopping'
                }
                onClick={() => void stopRun()}
              >
                {run.status === 'stopping' ? 'Stopping…' : 'Stop run'}
              </button>
            </>
          )}
          {checkedAt && (
            <p className="muted small">
              Last checked <Timestamp value={checkedAt} display={time} /> · Refreshes every 3 seconds.
            </p>
          )}
        </section>
      </div>
      {writeError && <ErrorState>{writeError}</ErrorState>}
      {conflictId && conflictId !== activeId && (
        <button type="button" className="secondary" onClick={() => follow(conflictId)}>
          View active run
        </button>
      )}
      {attempt && !writing && (
        <div className="panel simulation-retry">
          <p>This retry keeps the original settings and request ID. Use the same operator key.</p>
          <button
            type="button"
            disabled={operatorKey.trim() !== attempt.operatorKey}
            onClick={() => void launch(attempt)}
          >
            Retry start
          </button>{' '}
          <button
            type="button"
            className="secondary"
            onClick={() => {
              setAttempt(null)
              setWriteError(null)
              setNotice('Retry discarded. Review recent runs before starting again.')
            }}
          >
            Discard retry
          </button>
        </div>
      )}
      <p className="muted small" role="status">
        {notice}
        {run ? ` Run status: ${statusNames[run.status].toLowerCase()}.` : ''}
      </p>
      {history && history.items.length > 0 && (
        <section className="panel simulation-history" aria-labelledby="simulation-history-title">
          <h3 id="simulation-history-title">Recent runs</h3>
          <ul>
            {history.items.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  className="secondary"
                  aria-current={run?.id === item.id ? 'true' : undefined}
                  onClick={() => follow(item.id)}
                >
                  <span className="simulation-id">{item.id}</span> · {statusNames[item.status]} ·{' '}
                  {number.format(item.sent)} sent
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}
