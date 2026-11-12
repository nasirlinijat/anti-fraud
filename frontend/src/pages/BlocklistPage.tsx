import { type FormEvent, useState } from 'react'
import {
  addBlocklistEntry,
  entryTypes,
  listBlocklist,
  removeBlocklistEntry,
  type BlocklistEntry,
  type EntryType,
  type ErrorResponse,
} from '../api'
import { TimeDisplayToggle, Timestamp } from '../Display'
import { usePolling } from '../usePolling'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

const typeNames: Record<EntryType, string> = {
  card: 'Card token',
  device: 'Device ID',
  player: 'Player ID',
  ip: 'IP address',
}

const placeholders: Record<EntryType, string> = {
  card: 'card-7f3a',
  device: 'device-55c1',
  player: 'player-381',
  ip: '203.0.113.9',
}

function BlocklistPage() {
  const [entries, setEntries] = useState<BlocklistEntry[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [operatorKey, setOperatorKey] = useState('')
  const [removeError, setRemoveError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [removing, setRemoving] = useState<number | null>(null)
  const time = useTimeDisplay()

  const { refresh, retry, pending } = usePolling(listBlocklist, {
    onSuccess: (list) => {
      setEntries(list)
      setLoadError(null)
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'The blocklist cannot be loaded.'),
  })
  // While the list itself cannot be loaded, its own error offers the retry.
  const refreshEntries = loadError ? undefined : retry

  const remove = async (entry: BlocklistEntry) => {
    if (removing !== null || !operatorKey.trim()) return
    if (!window.confirm(`Remove ${typeNames[entry.type].toLowerCase()} ${entry.value} from the blocklist?`)) {
      return
    }
    setRemoving(entry.id)
    setRemoveError(null)
    setNotice(null)
    const res = await removeBlocklistEntry(entry.id, operatorKey.trim())
    setRemoving(null)
    if (res.ok) setNotice(`${typeNames[entry.type]} ${entry.value} is no longer blocked.`)
    else setRemoveError(res.error.error)
    refresh()
  }

  return (
    <div className="blocklist">
      <h1>Blocklist</h1>
      <p className="muted">
        A purchase whose card, device, player or IP address is on the blocklist is blocked before the rules run. A new
        entry applies to the next scored purchase.
      </p>

      <div className="field">
        <label htmlFor="blocklist-operator-key">Operator key</label>
        <input id="blocklist-operator-key" type="password" autoComplete="off" value={operatorKey}
          onChange={(event) => setOperatorKey(event.target.value)} />
      </div>

      <AddEntryForm operatorKey={operatorKey} pending={pending} onRefresh={refreshEntries} onNotice={setNotice} onAdded={refresh} />

      <p className="muted small" aria-live="polite">
        {notice}
      </p>

      <section>
        <h2>Entries{entries ? ` (${entries.length})` : ''}</h2>
        {loadError && <ErrorState onRetry={retry} pending={pending}>{loadError}</ErrorState>}
        {removeError && (
          <ErrorState onRetry={refreshEntries} retryLabel="Refresh entries" pending={pending}>
            {removeError}
          </ErrorState>
        )}
        {!entries && (!loadError || pending) && <LoadingSkeleton shape="table" label="Loading blocklist…" />}
        {entries && entries.length === 0 && <EmptyState>The blocklist is empty. Use Add an entry above to block a value.</EmptyState>}
        {entries && entries.length > 0 && <TimeDisplayToggle display={time} />}
        {entries && entries.length > 0 && (
          <EntriesTable entries={entries} time={time} disabled={removing !== null || !operatorKey.trim()} onRemove={remove} />
        )}
      </section>
    </div>
  )
}

function AddEntryForm({ operatorKey, pending, onRefresh, onNotice, onAdded }: {
  operatorKey: string
  pending: boolean
  onRefresh?: () => void
  onNotice: (notice: string | null) => void
  onAdded: () => void
}) {
  const [type, setType] = useState<EntryType>('card')
  const [value, setValue] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<ErrorResponse | null>(null)
  const fields = formError?.fields ?? {}

  const add = async (e: FormEvent) => {
    e.preventDefault()
    if (saving || !operatorKey.trim()) return
    setSaving(true)
    onNotice(null)
    const res = await addBlocklistEntry({ type, value, reason }, operatorKey.trim())
    setSaving(false)
    if (!res.ok) {
      setFormError(res.error)
      return
    }
    setFormError(null)
    setValue('')
    setReason('')
    onNotice(`${typeNames[res.value.type]} ${res.value.value} is blocked from the next purchase on.`)
    onAdded()
  }

  return (
    <form className="panel blocklist-form" onSubmit={add} noValidate>
      <h2>Add an entry</h2>
      <div className="fields">
        <div className={fields.type ? 'field invalid' : 'field'}>
          <label htmlFor="entry-type">Type</label>
          <select id="entry-type" value={type} disabled={saving} onChange={(e) => setType(e.target.value as EntryType)}>
            {entryTypes.map((t) => (
              <option key={t} value={t}>
                {typeNames[t]}
              </option>
            ))}
          </select>
          {fields.type && <span className="error-text">type {fields.type}</span>}
        </div>
        <RequiredField name="value" label="Value" value={value} error={fields.value} disabled={saving}
          placeholder={placeholders[type]} onChange={setValue} />
        <RequiredField name="reason" label="Reason" wide value={reason} error={fields.reason} disabled={saving}
          placeholder="Why is it blocked? For example: chargeback on 2026-11-08" onChange={setReason} />
      </div>
      {formError && (
        <ErrorState onRetry={Object.keys(fields).length === 0 ? onRefresh : undefined}
          retryLabel="Refresh entries" pending={pending}>
          {formError.error}
        </ErrorState>
      )}
      <button type="submit" disabled={saving || !operatorKey.trim()}>
        {saving ? 'Adding…' : 'Add to blocklist'}
      </button>
    </form>
  )
}

function RequiredField({ name, label, wide = false, value, error, disabled, placeholder, onChange }: {
  name: 'value' | 'reason'
  label: string
  wide?: boolean
  value: string
  error?: string
  disabled: boolean
  placeholder: string
  onChange: (value: string) => void
}) {
  const id = `entry-${name}`
  const className = ['field', error && 'invalid', wide && 'wide'].filter(Boolean).join(' ')
  return (
    <div className={className}>
      <label htmlFor={id}>
        {label}<span className="required"> *</span>
      </label>
      <input
        id={id}
        disabled={disabled}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
      />
      {error && (
        <span id={`${id}-error`} className="error-text">
          {name} {error}
        </span>
      )}
    </div>
  )
}

function EntriesTable({ entries, time, disabled, onRemove }: {
  entries: BlocklistEntry[]
  time: TimeDisplay
  disabled: boolean
  onRemove: (entry: BlocklistEntry) => void
}) {
  return (
    <div className="legacy-table-scroll" role="region" tabIndex={0} aria-label="Blocklist data"><table>
      <thead>
        <tr>
          <th scope="col">Type</th>
          <th scope="col">Value</th>
          <th scope="col">Reason</th>
          <th scope="col">Added</th>
          <th scope="col">
            <span className="visually-hidden">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {entries.map((entry) => (
          <tr key={entry.id}>
            <td>{typeNames[entry.type]}</td>
            <td>
              <code>{entry.value}</code>
            </td>
            <td>{entry.reason}</td>
            <td className="muted small"><Timestamp value={entry.created_at} display={time} /></td>
            <td className="num">
              <button
                type="button"
                className="secondary"
                disabled={disabled}
                onClick={() => onRemove(entry)}
                aria-label={`Remove ${typeNames[entry.type].toLowerCase()} ${entry.value}`}
              >
                Remove
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table></div>
  )
}

export default BlocklistPage
