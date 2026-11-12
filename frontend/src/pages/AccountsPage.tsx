import { useState, type FormEvent } from 'react'
import { createAccount, disableAccount, listAccounts, type AccountDetails } from '../accountApi'
import type { ErrorResponse } from '../api'
import { TimeDisplayToggle, Timestamp } from '../Display'
import type { Role } from '../session'
import { useAccount } from '../sessionContext'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { PageHeader } from '../ui/PageHeader'
import { PasswordField } from '../ui/PasswordField'
import { usePolling } from '../usePolling'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'
import { ErrorState, LoadingSkeleton } from '../ViewState'

const roleNames: Record<Role, string> = { reviewer: 'Reviewer', admin: 'Admin' }

function AccountsPage() {
  const me = useAccount()
  const [accounts, setAccounts] = useState<AccountDetails[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [disableError, setDisableError] = useState<string | null>(null)
  const [toDisable, setToDisable] = useState<AccountDetails | null>(null)
  const time = useTimeDisplay()

  const { refresh, retry, pending } = usePolling(listAccounts, {
    onSuccess: (list) => {
      setAccounts(list)
      setLoadError(null)
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'The accounts cannot be loaded.'),
  })

  const disable = async (account: AccountDetails) => {
    setToDisable(null)
    setNotice(null)
    setDisableError(null)
    const result = await disableAccount(account.username)
    if (result.ok) setNotice(`${account.username} is disabled and signed out everywhere.`)
    else setDisableError(result.error.error)
    refresh()
  }

  return (
    <div className="accounts">
      <PageHeader eyebrow="Administration" title="Accounts"
        lead="Reviewers work the review queue. Admins also see metrics, change configuration, and manage accounts." />
      <div className="split-layout">
        <section className="panel" aria-labelledby="accounts-list">
          <div className="panel-head">
            <h2 id="accounts-list">All accounts{accounts ? ` (${accounts.length})` : ''}</h2>
            {accounts && <TimeDisplayToggle display={time} />}
          </div>
          <p className={notice ? 'notice' : 'visually-hidden'} role="status">{notice}</p>
          {loadError && <ErrorState onRetry={retry} pending={pending}>{loadError}</ErrorState>}
          {disableError && <ErrorState>{disableError}</ErrorState>}
          {!accounts && (!loadError || pending) && <LoadingSkeleton shape="table" label="Loading accounts…" />}
          {accounts && <AccountsTable accounts={accounts} ownUsername={me.username} time={time} onDisable={setToDisable} />}
        </section>
        <CreateAccountForm onNotice={setNotice} onCreated={refresh} />
      </div>
      <ConfirmDialog open={toDisable !== null} title={`Disable ${toDisable?.username ?? 'account'}?`} confirmLabel="Disable account"
        danger onCancel={() => setToDisable(null)} onConfirm={() => { if (toDisable) void disable(toDisable) }}>
        <p>The account can no longer sign in and all its sessions end now. Its open claims stay until an admin releases them.</p>
      </ConfirmDialog>
    </div>
  )
}

function AccountsTable({ accounts, ownUsername, time, onDisable }: {
  accounts: AccountDetails[]
  ownUsername: string
  time: TimeDisplay
  onDisable: (account: AccountDetails) => void
}) {
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Accounts">
      <table className="stack-table" role="table">
        <thead role="rowgroup">
          <tr role="row">
            <th role="columnheader" scope="col">Username</th>
            <th role="columnheader" scope="col">Role</th>
            <th role="columnheader" scope="col">Status</th>
            <th role="columnheader" scope="col">Created</th>
            <th role="columnheader" scope="col"><span className="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody role="rowgroup">
          {accounts.map(account => (
            <tr role="row" key={account.username}>
              <th role="rowheader" scope="row">
                {account.username}{account.username === ownUsername && <span className="cell-note">You</span>}
              </th>
              <td role="cell" data-label="Role">{roleNames[account.role]}</td>
              <td role="cell" data-label="Status">
                <span className={account.disabled ? 'badge badge-muted' : 'badge badge-success'}>
                  {account.disabled ? 'Disabled' : 'Active'}
                </span>
              </td>
              <td role="cell" data-label="Created"><Timestamp value={account.created_at} display={time} /></td>
              <td role="cell" className="cell-action">
                {!account.disabled && account.username !== ownUsername && (
                  <Button variant="danger" size="sm" aria-label={`Disable ${account.username}`}
                    onClick={() => onDisable(account)}>Disable</Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const emptyDraft = { username: '', password: '', role: 'reviewer' as Role }

function CreateAccountForm({ onNotice, onCreated }: { onNotice: (notice: string | null) => void; onCreated: () => void }) {
  const [draft, setDraft] = useState(emptyDraft)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ErrorResponse | null>(null)
  const fields = error?.fields ?? {}

  const create = async (event: FormEvent) => {
    event.preventDefault()
    if (saving) return
    setSaving(true)
    onNotice(null)
    const result = await createAccount({ ...draft, username: draft.username.trim() })
    setSaving(false)
    if (!result.ok) {
      setError(result.error)
      return
    }
    setError(null)
    setDraft(emptyDraft)
    onNotice(`${roleNames[result.value.role]} account ${result.value.username} is ready to sign in.`)
    onCreated()
  }

  return (
    <form className="panel" onSubmit={create} noValidate aria-labelledby="accounts-create">
      <h2 id="accounts-create">Create an account</h2>
      <div className={fields.username ? 'field invalid' : 'field'}>
        <label htmlFor="account-username">Username</label>
        <input id="account-username" name="username" autoComplete="off" autoCapitalize="none" spellCheck={false}
          disabled={saving} value={draft.username} onChange={event => setDraft({ ...draft, username: event.target.value })}
          aria-invalid={fields.username ? true : undefined} aria-describedby={fields.username ? 'account-username-error' : undefined} />
        {fields.username && <span id="account-username-error" className="error-text">{fields.username}</span>}
      </div>
      <div className={fields.role ? 'field invalid' : 'field'}>
        <label htmlFor="account-role">Role</label>
        <select id="account-role" name="role" disabled={saving} value={draft.role}
          onChange={event => setDraft({ ...draft, role: event.target.value === 'admin' ? 'admin' : 'reviewer' })}>
          <option value="reviewer">Reviewer: review queue only</option>
          <option value="admin">Admin: every page</option>
        </select>
        {fields.role && <span className="error-text">{fields.role}</span>}
      </div>
      <PasswordField id="account-password" name="password" label="Password" autoComplete="new-password" disabled={saving}
        hint="12 to 72 characters. Share it with the account owner over a private channel."
        error={fields.password} value={draft.password} onChange={event => setDraft({ ...draft, password: event.target.value })} />
      {error && Object.keys(fields).length === 0 && <ErrorState>{error.error}</ErrorState>}
      <Button type="submit" disabled={saving}>{saving ? 'Creating…' : 'Create account'}</Button>
    </form>
  )
}

export default AccountsPage
