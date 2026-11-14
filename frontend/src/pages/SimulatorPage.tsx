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
import { BlockedPurchases } from './BlockedPurchases'
import { Button } from '../ui/Button'
import { PageHeader } from '../ui/PageHeader'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { usePolling } from '../usePolling'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'

type Attempt = { settings: SimulationSettings }
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
  const time = useTimeDisplay()
  const writes = useWrites()
  const outcome = useWriteOutcome()
  const runs = useSimulationRuns(writes.writing, outcome)
  const form = useSimulationForm(runs.available)
  const sim = { form, writes, outcome, runs }
  const { history, run, loadError, activeId, polling } = runs
  const { attempt, conflictId } = outcome
  const locked = writes.writing || attempt !== null

  return (
    <div className="simulator">
      <PageHeader eyebrow="Tools" title="Simulator"
        lead="Send simulated purchases through scoring and follow the actual decisions. Runs keep going when you leave this page." />
      {runs.disabled && <DisabledNotice onRecheck={runs.recheck} />}
      {loadError && (
        <ErrorState onRetry={polling.retry} pending={polling.pending} polite>
          {loadError}
          {history && ' Showing the last successful snapshot; these counts may be out of date.'}
        </ErrorState>
      )}
      {!history && !runs.disabled && (!loadError || polling.pending) && (
        <LoadingSkeleton shape="form" label="Loading simulation controls…" />
      )}
      <div className="simulator-layout">
        <StartForm form={form} runs={runs} writing={writes.writing} locked={locked} onSubmit={(event) => submit(sim, event)} />
        <RunProgress runs={runs} time={time} stopBlocked={writes.writing || runs.disabled}
          pendingStopId={outcome.pendingStopId} onStop={() => void stopRun(sim)} />
      </div>
      {outcome.writeError && <ErrorState>{outcome.writeError}</ErrorState>}
      {conflictId && conflictId !== activeId && (
        <Button variant="secondary" onClick={() => runs.follow(conflictId)}>View active run</Button>
      )}
      {attempt && !writes.writing && (
        <RetryPanel onRetry={() => void launch(sim, attempt)} onDiscard={outcome.discardAttempt} />
      )}
      <p className="muted small" role="status">
        {outcome.notice}
        {run ? ` Run status: ${statusNames[run.status].toLowerCase()}.` : ''}
      </p>
      {run && <BlockedPurchases key={run.id} run={run} time={time} />}
      {history && history.items.length > 0 && (
        <RecentRuns items={history.items} currentId={run?.id} onFollow={runs.follow} />
      )}
    </div>
  )
}

type Polling = Pick<ReturnType<typeof usePolling>, 'stop' | 'refresh'>
type WriteOutcome = ReturnType<typeof useWriteOutcome>
type Runs = ReturnType<typeof useSimulationRuns>
type Form = ReturnType<typeof useSimulationForm>
type Simulator = { form: Form; writes: ReturnType<typeof useWrites>; outcome: WriteOutcome; runs: Runs }

// One write runs at a time. Polling stops while it is in flight and restarts
// when it settles. Leaving the page aborts it, and an aborted write reports nothing.
function useWrites() {
  const [writing, setWriting] = useState(false)
  const busy = useRef(false)
  const mutation = useRef<AbortController | null>(null)

  useEffect(() => () => mutation.current?.abort(), [])

  async function run<T>({ polling, send, onSaved, onFailed }: {
    polling: Polling
    send: (signal: AbortSignal) => Promise<T>
    onSaved: (result: T) => void
    onFailed: (error: unknown) => void
  }) {
    busy.current = true
    polling.stop()
    setWriting(true)
    const controller = new AbortController()
    mutation.current = controller
    try {
      const result = await send(controller.signal)
      if (controller.signal.aborted) return
      onSaved(result)
    } catch (error) {
      if (controller.signal.aborted) return
      onFailed(error)
    } finally {
      busy.current = false
      if (!controller.signal.aborted) {
        setWriting(false)
        polling.refresh()
      }
    }
  }

  return { writing, busy, run }
}

// Holds what the last write left open: its error and notice, a start or stop
// whose result is unknown, and the run that refused a start by being active.
function useWriteOutcome() {
  const [writeError, setWriteError] = useState<string | null>(null)
  const [notice, setNotice] = useState('')
  const [conflictId, setConflictId] = useState<string | null>(null)
  const [attempt, setAttempt] = useState<Attempt | null>(null)
  const [pendingStopId, setPendingStopId] = useState<string | null>(null)

  // reconcile settles whatever a fresh read answers. It returns the run of a
  // start that the read confirmed, so the page can follow it.
  function reconcile(page: SimulationHistory, selected: SimulationRun | null) {
    const find = (id: string | null) =>
      id && (page.items.find((item) => item.id === id) ?? (selected?.id === id ? selected : null))
    const stopped = find(pendingStopId)
    if (stopped) {
      setPendingStopId(null)
      if (!isActive(stopped.status)) {
        setWriteError(null)
        setNotice(`Stop outcome confirmed: run ${statusNames[stopped.status].toLowerCase()}.`)
      }
    }
    const conflict = find(conflictId)
    if (conflict && !isActive(conflict.status)) {
      setConflictId(null)
      setWriteError(null)
      setNotice('The other run has finished. You can start a new run.')
    }
    const confirmed =
      attempt && page.items.find((item) => item.settings.request_id === attempt.settings.request_id)
    if (confirmed) {
      setAttempt(null)
      setWriteError(null)
      setNotice('The start is confirmed. Following the saved run.')
    }
    return confirmed
  }

  function dismissConflict(followedId: string) {
    if (conflictId === followedId) setWriteError(null)
    setConflictId(null)
  }

  function discardAttempt() {
    setAttempt(null)
    setWriteError(null)
    setNotice('Retry discarded. Review recent runs before starting again.')
  }

  return {
    writeError, notice, conflictId, attempt, pendingStopId,
    setWriteError, setNotice, setConflictId, setAttempt, setPendingStopId,
    reconcile, dismissConflict, discardAttempt,
  }
}

function useSimulationRuns(writing: boolean, outcome: WriteOutcome) {
  const [history, setHistory] = useState<SimulationHistory | null>(null)
  const [run, setRun] = useState<SimulationRun | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [disabled, setDisabled] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [checkedAt, setCheckedAt] = useState<string | null>(null)
  const [missing, setMissing] = useState(false)

  const load = useCallback((signal: AbortSignal) => loadRuns(selectedId, signal), [selectedId])
  const polling = usePolling(!disabled && !writing ? load : null, {
    intervalMs: 3000,
    waitAfterCompletion: true,
    onSuccess: ({ page, selected, missingRun }) => {
      setHistory(page)
      setRun(selected)
      setMissing(missingRun)
      setLoadError(null)
      setCheckedAt(new Date().toISOString())
      const confirmed = outcome.reconcile(page, selected)
      if (confirmed) {
        setSelectedId(confirmed.id)
        setRun(confirmed)
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

  function follow(id: string) {
    polling.stop()
    setSelectedId(id)
    setMissing(false)
    outcome.dismissConflict(id)
    polling.refresh()
  }

  function recheck() {
    setDisabled(false)
    polling.refresh()
  }

  function show(started: SimulationRun) {
    setRun(started)
    setSelectedId(started.id)
  }

  return { history, run, disabled, loadError, checkedAt, missing, polling, activeId, available, setRun, follow, recheck, show }
}

async function loadRuns(selectedId: string | null, signal: AbortSignal) {
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
}

function useSimulationForm(available: string[]) {
  const [draft, setDraft] = useState<SimulationDraft>(defaultSimulationDraft)
  const [fields, setFields] = useState<Record<string, string>>({})
  const ref = useRef<HTMLFormElement>(null)

  function clearField(name: string) {
    setFields((current) => {
      const next = { ...current }
      delete next[name]
      return next
    })
  }

  function change(name: keyof SimulationDraft, value: string) {
    const nextDraft = { ...draft, [name]: value }
    setDraft(nextDraft)
    if (!validateSimulation(nextDraft, available).fields[name]) clearField(name)
  }

  function check(name: keyof SimulationDraft) {
    const error = validateSimulation(draft, available).fields[name]
    if (error) setFields((current) => ({ ...current, [name]: error }))
  }

  // validate shows every problem and moves focus to the first one. It returns
  // the settings only when there is no problem.
  function validate() {
    const validated = validateSimulation(draft, available)
    setFields(validated.fields)
    if (Object.keys(validated.fields).length || !validated.settings) {
      const name = Object.keys(validated.fields)[0]
      ref.current?.querySelector<HTMLElement>(`[name="${name}"]`)?.focus()
      return null
    }
    return validated.settings
  }

  return { draft, fields, ref, setFields, change, check, validate }
}

function submit(sim: Simulator, event: FormEvent) {
  const { form, writes, outcome, runs } = sim
  event.preventDefault()
  if (writes.busy.current || outcome.attempt || runs.disabled || !runs.history || runs.loadError || runs.activeId) return
  const settings = form.validate()
  if (!settings) return
  const saved = { settings: { ...settings, request_id: crypto.randomUUID() } }
  outcome.setAttempt(saved)
  void launch(sim, saved)
}

async function launch({ form, writes, outcome, runs }: Simulator, saved: Attempt) {
  if (writes.busy.current) return
  outcome.setWriteError(null)
  outcome.setConflictId(null)
  outcome.setNotice('')
  await writes.run({
    polling: runs.polling,
    send: (signal) => startSimulation(saved.settings, signal),
    onSaved: (result) => {
      runs.show(result)
      outcome.setAttempt(null)
      form.setFields({})
      outcome.setNotice('Start confirmed. Following the saved run.')
    },
    onFailed: (error) => {
      if (error instanceof ApiError && error.fields) form.setFields(startFieldErrors(error.fields))
      if (error instanceof ApiError && error.status === 409) {
        outcome.setAttempt(null)
        outcome.setConflictId(error.activeRunId ?? null)
        outcome.setWriteError(
          error.activeRunId
            ? 'Another run is active. View it to follow progress or stop it.'
            : `${error.message} Refresh recent runs before trying again.`,
        )
      } else if (uncertainStart(error)) {
        outcome.setAttempt(saved)
        outcome.setWriteError(
          'The start could not be confirmed. It may already be running. Check recent runs or retry this same start.',
        )
      } else {
        outcome.setAttempt(null)
        outcome.setWriteError(startErrorMessage(error))
      }
    },
  })
}

// The API reports the scenario list as "scenarios"; the form has one "scenario" field.
function startFieldErrors(fields: Record<string, string>): Record<string, string> {
  return Object.fromEntries(
    Object.entries(fields).map(([name, value]) => [name === 'scenarios' ? 'scenario' : name, value]),
  )
}

function startErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return 'The run could not be started.'
}

async function stopRun({ writes, outcome, runs }: Simulator) {
  const { run } = runs
  if (!run || !isActive(run.status) || run.status === 'stopping' || outcome.pendingStopId || writes.busy.current) return
  outcome.setWriteError(null)
  outcome.setNotice('')
  await writes.run({
    polling: runs.polling,
    send: (signal) => stopSimulation(run.id, signal),
    onSaved: (result) => {
      runs.setRun(result)
      outcome.setPendingStopId(null)
      outcome.setNotice('Stop request confirmed.')
    },
    onFailed: () => {
      outcome.setPendingStopId(run.id)
      outcome.setWriteError('The stop could not be confirmed. Refreshing progress; you can safely request Stop again if it is still active.')
    },
  })
}

function DisabledNotice({ onRecheck }: { onRecheck: () => void }) {
  return (
    <div className="panel" role="status">
      <h2>Simulations are disabled or unavailable</h2>
      <p>
        Ask the operator to enable <code>SIMULATION_ENABLED</code> on this backend and restart it.
      </p>
      <Button variant="secondary" onClick={onRecheck}>Check availability</Button>
    </div>
  )
}

function StartForm({ form, runs, writing, locked, onSubmit }: {
  form: Form
  runs: Runs
  writing: boolean
  locked: boolean
  onSubmit: (event: FormEvent) => void
}) {
  const { draft, fields } = form
  const { activeId, disabled, history } = runs
  return (
    <form
      ref={form.ref}
      className="panel simulator-form"
      aria-labelledby="simulation-settings-title"
      noValidate
      onSubmit={onSubmit}
    >
      <h2 id="simulation-settings-title">Start a run</h2>
      <fieldset disabled={locked || disabled || !history}>
        <legend>Traffic settings</legend>
        <ScenarioField value={draft.scenario} error={fields.scenario} available={runs.available}
          onChange={(scenario) => form.change('scenario', scenario)} />
        <ModeChoice mode={draft.mode} onChange={(mode) => form.change('mode', mode)} />
        <div className="simulation-numbers">
          <NumberField name="seed" label="Seed" hint="Whole number; determines the traffic sequence."
            min={0} max={Number.MAX_SAFE_INTEGER} form={form} disabled={locked} />
          <NumberField name="rate" label="Rate (purchases/second)" hint="From 1 to 50; fractional rates are allowed."
            min={1} max={50} form={form} disabled={locked} />
          {draft.mode === 'backfill' && (
            <NumberField name="count" label="Purchase count" hint="From 1 to 10,000 purchases."
              min={1} max={10000} form={form} disabled={locked} />
          )}
        </div>
      </fieldset>
      <div className="simulation-actions">
        <Button type="submit" disabled={locked || disabled || !history || Boolean(runs.loadError) || Boolean(activeId)}>
          {writing ? 'Working…' : 'Start run'}
        </Button>
        {activeId && (
          <>
            <span className="muted small">A run is already active.</span>
            <Button variant="secondary" size="sm" onClick={() => runs.follow(activeId)}>View active run</Button>
          </>
        )}
      </div>
    </form>
  )
}

function ScenarioField({ value, error, available, onChange }: {
  value: string
  error?: string
  available: string[]
  onChange: (scenario: string) => void
}) {
  return (
    <div className="field">
      <label htmlFor="simulation-scenario">Scenario</label>
      <select
        id="simulation-scenario"
        name="scenario"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={Boolean(error)}
        aria-describedby={error ? 'simulation-scenario-error' : undefined}
      >
        <option value="mixed">Mixed traffic</option>
        {available.map((name) => (
          <option key={name} value={name}>
            {scenarioLabel(name)}
          </option>
        ))}
      </select>
      {error && (
        <span id="simulation-scenario-error" className="error-text">
          {error}
        </span>
      )}
      <span className="field-hint">
        Mixed traffic includes normal purchases, legitimate edge cases and suspicious patterns. A
        scenario can produce several purchases; outcomes depend on rules, model and history.
      </span>
    </div>
  )
}

function ModeChoice({ mode, onChange }: {
  mode: SimulationDraft['mode']
  onChange: (mode: SimulationDraft['mode']) => void
}) {
  return (
    <fieldset className="simulation-modes">
      <legend>Mode</legend>
      {(['live', 'backfill'] as const).map((option) => (
        <label key={option}>
          <input
            type="radio"
            name="mode"
            value={option}
            checked={mode === option}
            onChange={() => onChange(option)}
          />
          {option === 'live' ? 'Live — until stopped' : 'Backfill — fixed purchase count'}
        </label>
      ))}
    </fieldset>
  )
}

function NumberField({ name, label, hint, min, max, form, disabled }: {
  name: 'seed' | 'rate' | 'count'
  label: string
  hint: string
  min: number
  max: number
  form: Form
  disabled: boolean
}) {
  const error = form.fields[name]
  return (
    <div className={error ? 'field invalid' : 'field'}>
      <label htmlFor={`simulation-${name}`}>{label}</label>
      <span id={`simulation-${name}-hint`} className="field-hint">
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
        value={form.draft[name]}
        disabled={disabled}
        onChange={(event) => form.change(name, event.target.value)}
        onBlur={() => form.check(name)}
        aria-invalid={Boolean(error)}
        aria-describedby={`simulation-${name}-hint${error ? ` simulation-${name}-error` : ''}`}
      />
      {error && (
        <span id={`simulation-${name}-error`} className="error-text">
          {error}
        </span>
      )}
    </div>
  )
}

function RunProgress({ runs, time, stopBlocked, pendingStopId, onStop }: {
  runs: Runs
  time: TimeDisplay
  stopBlocked: boolean
  pendingStopId: string | null
  onStop: () => void
}) {
  const { run, missing, history, checkedAt } = runs
  return (
    <section className="panel simulation-progress" aria-labelledby="simulation-progress-title">
      <h2 id="simulation-progress-title">Run progress</h2>
      {missing && (
        <ErrorState>
          The selected run is no longer available. Start a new run or choose another recent run.
        </ErrorState>
      )}
      {!run && !missing && history && (
        <EmptyState>No runs yet. Choose settings and start a run.</EmptyState>
      )}
      {run && (
        <RunDetails run={run} time={time} stopBlocked={stopBlocked || pendingStopId === run.id} onStop={onStop} />
      )}
      {checkedAt && (
        <p className="muted small">
          Last checked <Timestamp value={checkedAt} display={time} /> · Refreshes every 3 seconds.
        </p>
      )}
    </section>
  )
}

function RunDetails({ run, time, stopBlocked, onStop }: {
  run: SimulationRun
  time: TimeDisplay
  stopBlocked: boolean
  onStop: () => void
}) {
  return (
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
      <RunTiles run={run} />
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
      <Button variant="danger" disabled={stopBlocked || !isActive(run.status) || run.status === 'stopping'} onClick={onStop}>
        {run.status === 'stopping' ? 'Stopping…' : 'Stop run'}
      </Button>
    </>
  )
}

function RunTiles({ run }: { run: SimulationRun }) {
  const tiles = [
    ['Sent', run.sent, ''],
    ['Allowed', run.allowed, 'tile-allow'],
    ['Reviewed', run.reviewed, 'tile-review'],
    ['Blocked', run.blocked, 'tile-block'],
  ] as const
  return (
    <div className="tiles simulation-tiles">
      {tiles.map(([label, value, className]) => (
        <div className={`tile ${className}`} key={label}>
          <span className="tile-label">{label}</span>
          <strong className="tile-value">{number.format(value)}</strong>
        </div>
      ))}
    </div>
  )
}

function RetryPanel({ onRetry, onDiscard }: { onRetry: () => void; onDiscard: () => void }) {
  return (
    <div className="panel simulation-retry">
      <p>This retry keeps the original settings and request ID, so it cannot start a second run.</p>
      <div className="button-row">
        <Button onClick={onRetry}>Retry start</Button>
        <Button variant="secondary" onClick={onDiscard}>Discard retry</Button>
      </div>
    </div>
  )
}

function RecentRuns({ items, currentId, onFollow }: {
  items: SimulationRun[]
  currentId?: string
  onFollow: (id: string) => void
}) {
  return (
    <section className="panel simulation-history" aria-labelledby="simulation-history-title">
      <h2 id="simulation-history-title">Recent runs</h2>
      <ul>
        {items.map((item) => (
          <li key={item.id}>
            <button
              type="button"
              className="run-link"
              aria-current={currentId === item.id ? 'true' : undefined}
              onClick={() => onFollow(item.id)}
            >
              <span className="simulation-id">{item.id}</span>
              <span className="muted small">{statusNames[item.status]} · {number.format(item.sent)} sent</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}
