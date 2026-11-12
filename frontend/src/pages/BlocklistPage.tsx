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
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { PageHeader } from '../ui/PageHeader'
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
  const [toRemove, setToRemove] = useState<BlocklistEntry | null>(null)
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
    setToRemove(null)
    if (removing !== null) return
    setRemoving(entry.id)
    setRemoveError(null)
    setNotice(null)
    const res = await removeBlocklistEntry(entry.id)
    setRemoving(null)
    if (res.ok) setNotice(`${typeNames[entry.type]} ${entry.value} is no longer blocked.`)
    else setRemoveError(res.error.error)
    refresh()
  }

  return (
    <div className="blocklist">
      <PageHeader eyebrow="Configuration" title="Blocklist"
        lead="A purchase whose card, device, player or IP address is on the blocklist is blocked before the rules run. A new entry applies to the next scored purchase." />
      <div className="split-layout">
        <section className="panel" aria-labelledby="blocklist-entries">
          <div className="panel-head">
            <h2 id="blocklist-entries">Entries{entries ? ` (${entries.length})` : ''}</h2>
            {entries && entries.length > 0 && <TimeDisplayToggle display={time} />}
          </div>
          <p className={notice ? 'notice' : 'visually-hidden'} role="status">{notice}</p>
          {loadError && <ErrorState onRetry={retry} pending={pending}>{loadError}</ErrorState>}
          {removeError && (
            <ErrorState onRetry={refreshEntries} retryLabel="Refresh entries" pending={pending}>
              {removeError}
            </ErrorState>
          )}
          {!entries && (!loadError || pending) && <LoadingSkeleton shape="table" label="Loading blocklist…" />}
          {entries && entries.length === 0 && <EmptyState>The blocklist is empty. Use Add an entry to block a value.</EmptyState>}
          {entries && entries.length > 0 && (
            <EntriesTable entries={entries} time={time} disabled={removing !== null} onRemove={setToRemove} />
          )}
        </section>
        <AddEntryForm pending={pending} onRefresh={refreshEntries} onNotice={setNotice} onAdded={refresh} />
      </div>
      <ConfirmDialog open={toRemove !== null} title="Remove this entry?" confirmLabel="Remove entry" danger
        onCancel={() => setToRemove(null)} onConfirm={() => { if (toRemove) void remove(toRemove) }}>
        <p>
          {toRemove && `${typeNames[toRemove.type]} ${toRemove.value}`} will no longer be blocked. Purchases that use it are
          scored by the rules again.
        </p>
      </ConfirmDialog>
    </div>
  )
}

function AddEntryForm({ pending, onRefresh, onNotice, onAdded }: {
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
    if (saving) return
    setSaving(true)
    onNotice(null)
    const res = await addBlocklistEntry({ type, value, reason })
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
    <form className="panel blocklist-form" onSubmit={add} noValidate aria-labelledby="blocklist-add">
      <h2 id="blocklist-add">Add an entry</h2>
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
          placeholder="Chargeback on 2026-11-08" onChange={setReason} />
      </div>
      {formError && (
        <ErrorState onRetry={Object.keys(fields).length === 0 ? onRefresh : undefined}
          retryLabel="Refresh entries" pending={pending}>
          {formError.error}
        </ErrorState>
      )}
      <Button type="submit" disabled={saving}>{saving ? 'Adding…' : 'Add to blocklist'}</Button>
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
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Blocklist data">
      <table className="stack-table" role="table">
        <thead role="rowgroup">
          <tr role="row">
            <th role="columnheader" scope="col">Value</th>
            <th role="columnheader" scope="col">Type</th>
            <th role="columnheader" scope="col">Reason</th>
            <th role="columnheader" scope="col">Added</th>
            <th role="columnheader" scope="col"><span className="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody role="rowgroup">
          {entries.map((entry) => (
            <tr role="row" key={entry.id}>
              <th role="rowheader" scope="row"><code>{entry.value}</code></th>
              <td role="cell" data-label="Type">{typeNames[entry.type]}</td>
              <td role="cell" data-label="Reason">{entry.reason}</td>
              <td role="cell" data-label="Added"><Timestamp value={entry.created_at} display={time} /></td>
              <td role="cell" className="cell-action">
                <Button variant="danger" size="sm" disabled={disabled} onClick={() => onRemove(entry)}
                  aria-label={`Remove ${typeNames[entry.type].toLowerCase()} ${entry.value}`}>
                  Remove
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export default BlocklistPage
