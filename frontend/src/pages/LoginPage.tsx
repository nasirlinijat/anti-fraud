import { useCallback, useEffect, useState, type FormEvent } from 'react'
import wordmark from '../assets/xsolla-wordmark.svg'
import { ApiError } from '../request'
import { useSession } from '../sessionContext'
import { ThemeToggle } from '../ThemeToggle'
import { Button } from '../ui/Button'
import { PasswordField } from '../ui/PasswordField'

const highlights = [
  ['Score', 'Every purchase is checked against the blocklist, weighted rules, and a fraud model.'],
  ['Review', 'Claim flagged purchases and decide them with the evidence behind each score.'],
  ['Control', 'Publish rulesets, manage the blocklist, and follow decision quality.'],
] as const

type Failure = { message: string; retryAt?: number }

function LoginPage({ notice }: { notice: string | null }) {
  useEffect(() => { document.title = 'Sign in · Anti-Fraud' }, [])
  return (
    <div className="login">
      <aside className="login-hero">
        <img className="login-logo" src={wordmark} alt="Xsolla" width="141" height="38" />
        <div className="login-pitch">
          <p className="login-product">Anti-Fraud</p>
          <p className="login-tagline">Real-time fraud decisions for in-game purchases.</p>
          <ul className="login-highlights">
            {highlights.map(([title, text]) => (
              <li key={title}><strong>{title}</strong><span>{text}</span></li>
            ))}
          </ul>
        </div>
        <p className="login-footnote">Independent project. Not an official Xsolla product.</p>
      </aside>
      <main className="login-main">
        <div className="login-theme"><ThemeToggle /></div>
        <SignInForm notice={notice} />
      </main>
    </div>
  )
}

function SignInForm({ notice }: { notice: string | null }) {
  const { signIn } = useSession()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [sending, setSending] = useState(false)
  const [failure, setFailure] = useState<Failure | null>(null)
  const blocked = sending || failure?.retryAt !== undefined
  const allowRetry = useCallback(() => setFailure(null), [])

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (blocked || !username.trim() || !password) return
    setSending(true)
    setFailure(null)
    try {
      await signIn(username.trim(), password)
    } catch (error) {
      setFailure(describeFailure(error))
      setSending(false)
    }
  }

  return (
    <form className="login-form" onSubmit={submit} aria-labelledby="login-title">
      <img className="login-form-logo" src={wordmark} alt="Xsolla" width="141" height="38" />
      <h1 id="login-title">Sign in</h1>
      <p className="page-lead">Use your Anti-Fraud console account.</p>
      {notice && !failure && <p className="notice" role="status">{notice}</p>}
      {failure && (
        <p className="alert" role="alert">
          {failure.message}
          {failure.retryAt !== undefined && <RetryCountdown until={failure.retryAt} onElapsed={allowRetry} />}
        </p>
      )}
      <div className="field">
        <label htmlFor="login-username">Username</label>
        <input id="login-username" name="username" type="text" autoComplete="username" autoCapitalize="none"
          spellCheck={false} autoFocus required value={username} onChange={event => setUsername(event.target.value)} />
      </div>
      <PasswordField id="login-password" name="password" label="Password" autoComplete="current-password"
        required value={password} onChange={event => setPassword(event.target.value)} />
      <Button type="submit" className="login-submit" aria-disabled={blocked}>
        {sending ? 'Signing in…' : 'Sign in'}
      </Button>
      <p className="field-hint">Accounts are created by an administrator. Ask yours if you cannot sign in.</p>
    </form>
  )
}

// The backend answers the same way for a wrong username, a wrong password,
// and a disabled account, so the message does not say which one it was.
function describeFailure(error: unknown): Failure {
  if (error instanceof ApiError && error.status === 401) {
    return { message: 'That username and password do not match an active account.' }
  }
  if (error instanceof ApiError && error.status === 429) {
    const seconds = error.retryAfterSeconds ?? 0
    return { message: 'Too many sign-in attempts for this username.', retryAt: Date.now() + seconds * 1000 }
  }
  return { message: error instanceof Error ? error.message : 'Sign-in failed. Try again.' }
}

// Shown while the backend refuses sign-in attempts. It is mounted with the
// failure, so its clock starts from the moment the refusal arrived.
function RetryCountdown({ until, onElapsed }: { until: number; onElapsed: () => void }) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= until) onElapsed()
    }, 1000)
    return () => clearInterval(timer)
  }, [until, onElapsed])
  return <> Try again in {formatWait(Math.max(1, Math.ceil((until - now) / 1000)))}.</>
}

function formatWait(seconds: number): string {
  if (seconds < 60) return `${seconds} ${seconds === 1 ? 'second' : 'seconds'}`
  return `${Math.ceil(seconds / 60)} minutes`
}

export default LoginPage
