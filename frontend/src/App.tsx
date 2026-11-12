import wordmark from './assets/xsolla-wordmark.svg'
import Console from './Console'
import LoginPage from './pages/LoginPage'
import { useSession } from './sessionContext'
import { Button } from './ui/Button'

function App() {
  const { state, retryRestore } = useSession()
  if (state.status === 'signed-in') return <Console account={state.account} />
  if (state.status === 'signed-out') return <LoginPage notice={state.notice} />
  return (
    <main className="splash">
      <img src={wordmark} alt="Xsolla" width="141" height="38" />
      {state.status === 'restoring' && <p role="status">Checking your session…</p>}
      {state.status === 'unreachable' && (
        <>
          <p role="alert">{state.message}</p>
          <Button variant="secondary" onClick={retryRestore}>Try again</Button>
        </>
      )}
    </main>
  )
}

export default App
