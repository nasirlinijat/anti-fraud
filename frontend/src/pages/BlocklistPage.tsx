import { type FormEvent, useCallback, useEffect, useState } from 'react'
import {
  addBlocklistEntry,
  entryTypes,
  listBlocklist,
  removeBlocklistEntry,
  type BlocklistEntry,
  type EntryType,
  type ErrorResponse,
} from '../api'

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
  const [type, setType] = useState<EntryType>('card')
  const [value, setValue] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<ErrorResponse | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [removing, setRemoving] = useState<number | null>(null)

  const load = useCallback(() => {
    listBlocklist()
      .then((list) => {
        setEntries(list)
        setLoadError(null)
      })
      .catch((e: unknown) => setLoadError(e instanceof Error ? e.message : 'The blocklist cannot be loaded.'))
  }, [])

  useEffect(load, [load])

  const add = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setNotice(null)
    const res = await addBlocklistEntry({ type, value, reason })
    setSaving(false)
    if (!res.ok) {
      setFormError(res.error)
      return
    }
    setFormError(null)
    setValue('')
    setReason('')
    setNotice(`${typeNames[res.value.type]} ${res.value.value} is blocked from the next purchase on.`)
    load()
  }

  const remove = async (entry: BlocklistEntry) => {
    if (!window.confirm(`Remove ${typeNames[entry.type].toLowerCase()} ${entry.value} from the blocklist?`)) {
      return
    }
    setRemoving(entry.id)
    const res = await removeBlocklistEntry(entry.id)
    setRemoving(null)
    setNotice(res.ok ? `${typeNames[entry.type]} ${entry.value} is no longer blocked.` : res.error.error)
    load()
  }

  const fields = formError?.fields ?? {}

  return (
    <div className="blocklist">
      <h2>Blocklist</h2>
      <p className="muted">
        A purchase whose card, device, player or IP address is on the blocklist is blocked before the rules run. A new
        entry applies to the next scored purchase.
      </p>

      <form className="panel blocklist-form" onSubmit={add} noValidate>
        <h3>Add an entry</h3>
        <div className="fields">
          <div className={fields.type ? 'field invalid' : 'field'}>
            <label htmlFor="entry-type">Type</label>
            <select id="entry-type" value={type} onChange={(e) => setType(e.target.value as EntryType)}>
              {entryTypes.map((t) => (
                <option key={t} value={t}>
                  {typeNames[t]}
                </option>
              ))}
            </select>
            {fields.type && <span className="error-text">type {fields.type}</span>}
          </div>
          <div className={fields.value ? 'field invalid' : 'field'}>
            <label htmlFor="entry-value">
              Value<span className="required"> *</span>
            </label>
            <input
              id="entry-value"
              value={value}
              placeholder={placeholders[type]}
              onChange={(e) => setValue(e.target.value)}
              aria-invalid={fields.value ? true : undefined}
              aria-describedby={fields.value ? 'entry-value-error' : undefined}
            />
            {fields.value && (
              <span id="entry-value-error" className="error-text">
                value {fields.value}
              </span>
            )}
          </div>
          <div className={fields.reason ? 'field invalid wide' : 'field wide'}>
            <label htmlFor="entry-reason">
              Reason<span className="required"> *</span>
            </label>
            <input
              id="entry-reason"
              value={reason}
              placeholder="Why is it blocked? For example: chargeback on 2026-11-08"
              onChange={(e) => setReason(e.target.value)}
              aria-invalid={fields.reason ? true : undefined}
              aria-describedby={fields.reason ? 'entry-reason-error' : undefined}
            />
            {fields.reason && (
              <span id="entry-reason-error" className="error-text">
                reason {fields.reason}
              </span>
            )}
          </div>
        </div>
        {formError && Object.keys(fields).length === 0 && (
          <p className="error-text" role="alert">
            {formError.error}
          </p>
        )}
        <button type="submit" disabled={saving}>
          {saving ? 'Adding…' : 'Add to blocklist'}
        </button>
      </form>

      <p className="muted small" aria-live="polite">
        {notice}
      </p>

      <section>
        <h3>Entries{entries ? ` (${entries.length})` : ''}</h3>
        {loadError && (
          <div className="alert" role="alert">
            {loadError}
          </div>
        )}
        {entries && entries.length === 0 && <p className="muted">The blocklist is empty.</p>}
        {entries && entries.length > 0 && (
          <table>
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
                  <td className="muted small">{new Date(entry.created_at).toLocaleString()}</td>
                  <td className="num">
                    <button
                      type="button"
                      className="secondary"
                      disabled={removing === entry.id}
                      onClick={() => remove(entry)}
                      aria-label={`Remove ${typeNames[entry.type].toLowerCase()} ${entry.value}`}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  )
}

export default BlocklistPage
