import { useEffect, useState } from 'react'
import { checkReadiness } from './api'
import BlocklistPage from './pages/BlocklistPage'
import DashboardPage from './pages/DashboardPage'
import ReviewQueue from './ReviewQueue'
import RulesetPage from './pages/RulesetPage'
import TesterPage from './pages/TesterPage'
import SimulatorPage from './pages/SimulatorPage'
import { usePolling } from './usePolling'
import { Status as BackendStatus } from '@xsolla/xui-status'
import { Brand, ConsoleNavigation } from './ConsoleNavigation'
import './console.css'

type Status = 'checking' | 'ready' | 'unavailable'
const readinessPalettes = { checking: 'neutral', ready: 'success', unavailable: 'alert' } as const

// Pages are chosen by the URL hash, for example #/tester. Add a page here to
// show it in the navigation.
const pages = [
  { path: '/dashboard', title: 'Dashboard', element: <DashboardPage /> },
  { path: '/reviews', title: 'Review queue', element: <ReviewQueue /> },
  { path: '/blocklist', title: 'Blocklist', element: <BlocklistPage /> },
  { path: '/rulesets', title: 'Rulesets', element: <RulesetPage /> },
  { path: '/tester', title: 'Tester', element: <TesterPage /> },
  { path: '/simulator', title: 'Simulator', element: <SimulatorPage /> },
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

  useEffect(() => { document.title = `${page.title} · Anti-Fraud` }, [page.title])

  return (
    <div className="console-shell">
      <a className="skip-link" href="#main-content" onClick={event => {
        event.preventDefault()
        document.getElementById('main-content')?.focus()
      }}>Skip to content</a>
      <ConsoleNavigation pages={pages} path={page.path} />
      <div className="console-workspace">
        <header className="console-topbar">
          <div className="mobile-brand"><Brand /></div>
          <span className="workspace-label">Review console <span>/ {page.title}</span></span>
          <BackendStatus palette={readinessPalettes[status]}
            aria-label={`Backend: ${status}`} aria-live="polite">Backend: {status}</BackendStatus>
        </header>
        <main id="main-content" tabIndex={-1} className={`console-content ${page.path === '/dashboard' ? '' : 'legacy-page'}`}>
          {page.element}
        </main>
      </div>
    </div>
  )
}

export default App
