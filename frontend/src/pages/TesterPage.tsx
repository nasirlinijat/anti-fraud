import { type FormEvent, useState } from 'react'
import { score, type ScoreResponse, type ScoreResult } from '../api'
import { ModelRiskBadge, OutcomeBadge, RuleScoreBadge } from '../Display'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

type Kind = 'text' | 'integer' | 'number' | 'time'

interface Field {
  name: string
  label: string
  kind: Kind
  required?: boolean
  hint?: string
  options?: string[]
}

interface Group {
  title: string
  fields: Field[]
}

// Every field of the score request in openapi.yaml, in the same order.
const groups: Group[] = [
  {
    title: 'Purchase',
    fields: [
      { name: 'transaction_id', label: 'Transaction ID', kind: 'text', required: true },
      { name: 'event_time', label: 'Event time', kind: 'time', required: true },
      { name: 'amount_minor', label: 'Amount (minor units)', kind: 'integer', required: true, hint: '1999 is 19.99' },
      { name: 'player_id', label: 'Player ID', kind: 'text', required: true },
      { name: 'product_code', label: 'Product code', kind: 'text', options: ['W', 'H', 'C', 'S', 'R'] },
    ],
  },
  {
    title: 'Card',
    fields: [
      { name: 'card_token', label: 'Card token', kind: 'text', required: true },
      { name: 'card_issuer', label: 'Card issuer', kind: 'text' },
      { name: 'card_network', label: 'Card network', kind: 'text', options: ['visa', 'mastercard', 'american express', 'discover'] },
      { name: 'card_type', label: 'Card type', kind: 'text', options: ['debit', 'credit', 'debit or credit', 'charge card'] },
      { name: 'card_country', label: 'Card country', kind: 'text', hint: 'Compared with IP country' },
      { name: 'card_category', label: 'Card category', kind: 'text' },
    ],
  },
  {
    title: 'Billing and email',
    fields: [
      { name: 'billing_region', label: 'Billing region', kind: 'text' },
      { name: 'billing_country', label: 'Billing country', kind: 'text' },
      { name: 'billing_distance', label: 'Billing distance', kind: 'number' },
      { name: 'email_domain', label: 'Email domain', kind: 'text' },
      { name: 'recipient_email_domain', label: 'Recipient email domain', kind: 'text' },
    ],
  },
  {
    title: 'Device and account',
    fields: [
      { name: 'device_type', label: 'Device type', kind: 'text', options: ['desktop', 'mobile'] },
      { name: 'device_name', label: 'Device name', kind: 'text' },
      { name: 'device_id', label: 'Device ID', kind: 'text' },
      { name: 'ip_address', label: 'IP address', kind: 'text' },
      { name: 'ip_country', label: 'IP country', kind: 'text' },
      { name: 'account_created_at', label: 'Account created at', kind: 'time' },
      { name: 'client_os', label: 'Client OS', kind: 'text', options: ['Windows 10', 'Mac OS X 10_13', 'iOS 11.2.1', 'Android 8.0'] },
      { name: 'client_browser', label: 'Client browser', kind: 'text', options: ['chrome 63.0', 'mobile safari 11.0', 'firefox 57.0', 'edge 16.0'] },
      { name: 'screen_resolution', label: 'Screen resolution', kind: 'text', hint: 'WIDTHxHEIGHT, for example 1920x1080' },
    ],
  },
]

const fields = groups.flatMap((g) => g.fields)

type Values = Record<string, string>

// localTime formats a date for a datetime-local input.
function localTime(date: Date): string {
  const shifted = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return shifted.toISOString().slice(0, 16)
}

function newTransactionId(): string {
  return `test-${Date.now()}`
}

function hoursAgo(hours: number): string {
  return localTime(new Date(Date.now() - hours * 3_600_000))
}

const presets: Record<string, () => Values> = {
  Normal: () => ({
    amount_minor: '1999',
    player_id: 'player-normal',
    card_token: 'card-normal',
    card_network: 'visa',
    card_type: 'debit',
    card_country: 'AZ',
    product_code: 'W',
    email_domain: 'gmail.com',
    device_type: 'mobile',
    device_name: 'SM-G991B',
    ip_address: '203.0.113.9',
    ip_country: 'AZ',
    account_created_at: hoursAgo(24 * 200),
    client_os: 'Windows 10',
    client_browser: 'chrome 63.0',
    screen_resolution: '1920x1080',
  }),
  'New account, foreign IP, large amount': () => ({
    amount_minor: '75000',
    player_id: 'player-new',
    card_token: 'card-new',
    card_network: 'visa',
    card_type: 'credit',
    card_country: 'AZ',
    product_code: 'W',
    email_domain: 'mail.ru',
    device_type: 'desktop',
    device_name: 'Windows',
    ip_address: '198.51.100.7',
    ip_country: 'TR',
    account_created_at: hoursAgo(2),
    client_os: 'Android 8.0',
    client_browser: 'chrome 62.0',
    screen_resolution: '720x1280',
  }),
  Empty: () => ({}),
}

function withRequired(values: Values): Values {
  return { transaction_id: newTransactionId(), event_time: localTime(new Date()), ...values }
}

// toRequest turns the form values into a score request. Empty optional fields
// are left out. A value that is not a number is sent as typed, so the API
// reports it.
function toRequest(values: Values): Record<string, unknown> {
  const request: Record<string, unknown> = {}
  for (const field of fields) {
    const value = (values[field.name] ?? '').trim()
    if (value === '') continue
    if (field.kind === 'integer' || field.kind === 'number') {
      const n = Number(value)
      request[field.name] = Number.isFinite(n) ? n : value
    } else if (field.kind === 'time') {
      const date = new Date(value)
      request[field.name] = Number.isNaN(date.getTime()) ? value : date.toISOString()
    } else {
      request[field.name] = value
    }
  }
  return request
}

function TesterPage() {
  const [values, setValues] = useState<Values>(() => withRequired(presets.Normal()))
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<ScoreResponse | null>(null)
  const [failure, setFailure] = useState<Extract<ScoreResult, { ok: false }> | null>(null)
  const [retryPurchase, setRetryPurchase] = useState<Record<string, unknown> | null>(null)

  const set = (name: string, value: string) => {
    setRetryPurchase(null)
    setValues(current => ({ ...current, [name]: value }))
  }

  const applyPreset = (name: string) => {
    setValues(withRequired(presets[name]()))
    setResult(null)
    setFailure(null)
    setRetryPurchase(null)
  }

  async function scorePurchase(purchase: Record<string, unknown>) {
    if (sending) return
    setSending(true)
    setResult(null)
    setFailure(null)
    setRetryPurchase(null)
    const res = await score(purchase)
    setSending(false)
    if (res.ok) {
      setResult(res.response)
      // The next submit gets a new ID, because a transaction can be scored only once.
      setValues(current => ({ ...current, transaction_id: newTransactionId() }))
    } else {
      setFailure(res)
      const { status } = res
      if (status === 0 || status === 408 || status === 429 || status >= 500 || (status >= 200 && status < 300)) {
        setRetryPurchase(purchase)
      }
    }
  }

  function submit(event: FormEvent) {
    event.preventDefault()
    void scorePurchase(toRequest(values))
  }

  const fieldErrors = failure?.error.fields ?? {}

  return (
    <div className="tester">
      <section>
        <h1>Score a purchase</h1>
        <p className="muted">
          Fill in a purchase and send it to <code>POST /score</code>. Optional fields can stay empty.
        </p>
        <div className="presets" role="group" aria-label="Fill the form with an example">
          {Object.keys(presets).map((name) => (
            <button key={name} type="button" className="secondary" disabled={sending} onClick={() => applyPreset(name)}>
              {name}
            </button>
          ))}
        </div>

        <form onSubmit={submit} noValidate>
          {groups.map((group) => (
            <fieldset key={group.title} disabled={sending}>
              <legend>{group.title}</legend>
              <div className="fields">
                {group.fields.map((field) => {
                  const id = `field-${field.name}`
                  const problem = fieldErrors[field.name]
                  const listId = field.options ? `${id}-options` : undefined
                  return (
                    <div key={field.name} className={problem ? 'field invalid' : 'field'}>
                      <label htmlFor={id}>
                        {field.label}
                        {field.required && <span className="required"> *</span>}
                      </label>
                      <input
                        id={id}
                        name={field.name}
                        type={field.kind === 'time' ? 'datetime-local' : 'text'}
                        inputMode={field.kind === 'integer' ? 'numeric' : field.kind === 'number' ? 'decimal' : undefined}
                        list={listId}
                        value={values[field.name] ?? ''}
                        onChange={(e) => set(field.name, e.target.value)}
                        aria-invalid={problem ? true : undefined}
                        aria-describedby={problem ? `${id}-error` : field.hint ? `${id}-hint` : undefined}
                      />
                      {field.options && (
                        <datalist id={listId}>
                          {field.options.map((o) => (
                            <option key={o} value={o} />
                          ))}
                        </datalist>
                      )}
                      {problem ? (
                        <span id={`${id}-error`} className="error-text">
                          {field.name} {problem}
                        </span>
                      ) : (
                        field.hint && (
                          <span id={`${id}-hint`} className="muted small">
                            {field.hint}
                          </span>
                        )
                      )}
                    </div>
                  )
                })}
              </div>
            </fieldset>
          ))}
          <button type="submit" disabled={sending}>
            {sending ? 'Scoring…' : 'Score purchase'}
          </button>
        </form>
      </section>

      <section className="result">
        <h2>Result</h2>
        {!result && !failure && (sending
          ? <LoadingSkeleton shape="result" label="Scoring purchase…" />
          : <EmptyState>Choose Normal or fill out the form, then select Score purchase to see the decision.</EmptyState>)}
        {failure && (
          <ErrorState onRetry={retryPurchase ? () => void scorePurchase(retryPurchase) : undefined}>
            <strong>{failure.error.error}</strong>
            {failure.status === 409 && <p>Use a new transaction ID for a separate test.</p>}
            {Object.keys(fieldErrors).length > 0 && (
              <ul>
                {Object.entries(fieldErrors).map(([name, problem]) => (
                  <li key={name}>
                    <code>{name}</code> {problem}
                  </li>
                ))}
              </ul>
            )}
          </ErrorState>
        )}
        <div aria-live="polite">{result && <ResultView result={result} />}</div>
      </section>
    </div>
  )
}

function ResultView({ result }: { result: ScoreResponse }) {
  return (
    <div>
      <p className="result-outcome"><OutcomeBadge outcome={result.outcome} /></p>
      <dl className="facts">
        <dt>Rule score</dt>
        <dd><RuleScoreBadge value={result.score} /></dd>
        <dt>Model risk</dt>
        <dd><ModelRiskBadge value={result.model_score} /></dd>
        <dt>Model version</dt>
        <dd>{result.model_version ?? 'none'}</dd>
        <dt>Ruleset</dt>
        <dd>version {result.ruleset_version}</dd>
        <dt>Transaction</dt>
        <dd>
          <code>{result.transaction_id}</code>
        </dd>
      </dl>
      <h3>Fired rules</h3>
      {result.fired_rules.length === 0 ? (
        <EmptyState>No rule fired.</EmptyState>
      ) : (
        <div className="legacy-table-scroll" role="region" tabIndex={0} aria-label="Tester data"><table>
          <thead>
            <tr>
              <th scope="col">Rule</th>
              <th scope="col">Why it fired</th>
              <th scope="col" className="num">
                Points
              </th>
            </tr>
          </thead>
          <tbody>
            {result.fired_rules.map((rule) => (
              <tr key={rule.name}>
                <td>
                  <code>{rule.name}</code>
                </td>
                <td>{rule.message}</td>
                <td className="num">{rule.points}</td>
              </tr>
            ))}
          </tbody>
        </table></div>
      )}
    </div>
  )
}

export default TesterPage
