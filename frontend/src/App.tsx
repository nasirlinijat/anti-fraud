import { useEffect, useState } from 'react'
import { checkReadiness } from './api'
import BlocklistPage from './pages/BlocklistPage'
import DashboardPage from './pages/DashboardPage'
import ReviewQueue from './ReviewQueue'
import RulesetPage from './pages/RulesetPage'
import TesterPage from './pages/TesterPage'
import SimulatorPage from './pages/SimulatorPage'
import { usePolling } from './usePolling'

type Status = 'checking' | 'ready' | 'unavailable'

// Pages are chosen by the URL hash, for example #/tester. Add a page here to
// show it in the navigation.
const pages = [
  { path: '/dashboard', title: 'Dashboard', element: <DashboardPage /> },
  { path: '/blocklist', title: 'Blocklist', element: <BlocklistPage /> },
  { path: '/tester', title: 'Tester', element: <TesterPage /> },
  { path: '/simulator', title: 'Simulator', element: <SimulatorPage /> },
  { path: '/reviews', title: 'Review queue', element: <ReviewQueue /> },
  { path: '/rulesets', title: 'Rulesets', element: <RulesetPage /> },
]

function currentPath(): string {
  return window.location.hash.replace(/^#/, '') || pages[0].path
}

function App() {
  const [status, setStatus] = useState<Status>('checking')
  const [path, setPath] = useState(currentPath)

  usePolling(checkReadiness, {
    intervalMs: 10_000,
    waitAfterCompletion: true,
    onSuccess: () => setStatus('ready'),
    onError: () => setStatus('unavailable'),
  })

  useEffect(() => {
    const onHashChange = () => setPath(currentPath())
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  const page = pages.find((p) => p.path === path) ?? pages[0]

  return (
    <>
      <header className="topbar">
        <strong className="brand">Anti-Fraud</strong>
        <nav aria-label="Pages">
          {pages.map((p) => (
            <a key={p.path} href={`#${p.path}`} aria-current={p === page ? 'page' : undefined}>
              {p.title}
            </a>
          ))}
        </nav>
        <span className={`status status-${status}`}>Backend: {status}</span>
      </header>
      <main>{page.element}</main>
    </>
  )
}

export default App
