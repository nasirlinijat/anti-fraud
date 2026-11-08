import { useEffect, useState } from 'react'

type Status = 'checking' | 'ready' | 'unavailable'

function App() {
  const [status, setStatus] = useState<Status>('checking')

  useEffect(() => {
    fetch('/ready')
      .then((res) => setStatus(res.ok ? 'ready' : 'unavailable'))
      .catch(() => setStatus('unavailable'))
  }, [])

  return (
    <main>
      <h1>Anti-Fraud</h1>
      <p>
        Backend: <strong>{status}</strong>
      </p>
    </main>
  )
}

export default App
