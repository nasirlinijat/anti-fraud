import { Crosshair, Flag, Home, ListBoxes, Play, Settings, User, Users } from '@xsolla/xui-icons-base'
import { useEffect, useState, type ReactNode } from 'react'
import { checkReadiness } from './api'
import { ConsoleNavigation, type Destination } from './ConsoleNavigation'
import AccountsPage from './pages/AccountsPage'
import AssignmentsPage from './pages/AssignmentsPage'
import BlocklistPage from './pages/BlocklistPage'
import DashboardPage from './pages/DashboardPage'
import RulesetPage from './pages/RulesetPage'
import SimulatorPage from './pages/SimulatorPage'
import TesterPage from './pages/TesterPage'
import ReviewQueue from './reviews/ReviewQueue'
import type { Account, Role } from './session'
import { ThemeSaveStatus, ThemeToggle } from './ThemeToggle'
import { Brand } from './ui/Brand'
import { usePolling } from './usePolling'

type Readiness = 'checking' | 'ready' | 'unavailable'

interface ConsolePage extends Destination {
  roles: readonly Role[]
  element: ReactNode
}

const everyone = ['reviewer', 'admin'] as const
const admins = ['admin'] as const

// Pages are chosen by the URL hash, for example #/tester. Add a page here to
// show it in the navigation of the roles that may use its endpoints.
const pages: ConsolePage[] = [
  { path: '/dashboard', title: 'Dashboard', group: 'Operations', icon: Home, roles: admins, element: <DashboardPage /> },
  { path: '/reviews', title: 'Review queue', group: 'Operations', icon: ListBoxes, roles: everyone, element: <ReviewQueue /> },
  { path: '/assignments', title: 'Assignments', group: 'Operations', icon: Users, roles: admins, element: <AssignmentsPage /> },
  { path: '/blocklist', title: 'Blocklist', group: 'Configuration', icon: Flag, roles: admins, element: <BlocklistPage /> },
  { path: '/rulesets', title: 'Rulesets', group: 'Configuration', icon: Settings, roles: admins, element: <RulesetPage /> },
  { path: '/tester', title: 'Tester', group: 'Tools', icon: Crosshair, roles: admins, element: <TesterPage /> },
  { path: '/simulator', title: 'Simulator', group: 'Tools', icon: Play, roles: admins, element: <SimulatorPage /> },
  { path: '/accounts', title: 'Accounts', group: 'Administration', icon: User, roles: admins, element: <AccountsPage /> },
]

function currentPath(): string {
  return window.location.hash.replace(/^#/, '')
}

function useHashPath(): string {
  const [path, setPath] = useState(currentPath)
  useEffect(() => {
    const onHashChange = () => setPath(currentPath())
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])
  return path
}

function useReadiness(): Readiness {
  const [status, setStatus] = useState<Readiness>('checking')
  usePolling(checkReadiness, {
    intervalMs: 10_000,
    waitAfterCompletion: true,
    onSuccess: () => setStatus('ready'),
    onError: () => setStatus('unavailable'),
  })
  return status
}

function Console({ account }: { account: Account }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const readiness = useReadiness()
  const path = useHashPath()
  const allowed = pages.filter(page => page.roles.includes(account.role))
  const home = allowed[0]
  const requested = pages.find(page => page.path === path)
  const page = requested ?? home
  const permitted = allowed.includes(page)
  const title = permitted ? page.title : 'Not available'

  useEffect(() => { document.title = `${title} · Anti-Fraud` }, [title])

  return (
    <div className="console-shell">
      <a className="skip-link" href="#main-content" onClick={event => {
        event.preventDefault()
        document.getElementById('main-content')?.focus()
      }}>Skip to content</a>
      <ConsoleNavigation pages={allowed} path={page.path} account={account} open={menuOpen} onOpenChange={setMenuOpen} />
      <div className="console-workspace">
        <header className="console-topbar">
          <div className="mobile-brand"><Brand compact /></div>
          <p className="workspace-label">Anti-Fraud <span aria-hidden="true">/</span> <strong>{title}</strong></p>
          <span className={`status-pill status-${readiness}`} role="status" aria-label={`Backend: ${readiness}`}>
            Backend: {readiness}
          </span>
          <div className="console-actions">
            <ThemeToggle />
            <button id="console-menu-trigger" type="button" className="btn btn-secondary menu-trigger" aria-haspopup="dialog"
              aria-controls="console-menu" aria-expanded={menuOpen} onClick={() => setMenuOpen(true)}>
              <span aria-hidden="true">☰</span> Menu
            </button>
          </div>
          <ThemeSaveStatus />
        </header>
        <main id="main-content" tabIndex={-1} className="console-content">
          {permitted ? page.element : <NotAvailable role={account.role} home={home} />}
        </main>
      </div>
    </div>
  )
}

function NotAvailable({ role, home }: { role: Role; home: Destination }) {
  return (
    <section className="panel empty-panel">
      <h1>This page is not available for your role</h1>
      <p className="muted">
        You are signed in as {role === 'admin' ? 'an admin' : 'a reviewer'}. Ask an admin if you need access to this page.
      </p>
      <a className="btn btn-primary" href={`#${home.path}`}>Go to {home.title}</a>
    </section>
  )
}

export default Console
