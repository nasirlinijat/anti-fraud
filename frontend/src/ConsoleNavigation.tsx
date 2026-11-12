import { Logout } from '@xsolla/xui-icons-base'
import { useEffect, useRef, useState, type ComponentType, type MouseEvent } from 'react'
import type { Account } from './session'
import { useSession } from './sessionContext'
import { Brand } from './ui/Brand'
import { Button } from './ui/Button'

export interface Destination {
  path: string
  title: string
  group: string
  icon: ComponentType<{ size?: number; 'aria-hidden'?: boolean }>
}

interface NavigationProps {
  pages: Destination[]
  path: string
  account: Account
  open: boolean
  onOpenChange: (open: boolean) => void
}

const wideScreen = '(min-width: 64rem)'

export function ConsoleNavigation({ pages, path, account, open, onOpenChange }: NavigationProps) {
  const wide = useWideScreen(() => onOpenChange(false))
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => { onOpenChange(false) }, [path, onOpenChange])
  useEffect(() => {
    if (open && !wide) dialog.current?.showModal()
    else dialog.current?.close()
  }, [open, wide])

  const menu = <NavigationMenu pages={pages} path={path} onNavigate={() => onOpenChange(false)} />
  if (wide) {
    return <aside className="console-sidebar"><Brand />{menu}<AccountCard account={account} /></aside>
  }
  return (
    <dialog ref={dialog} id="console-menu" className="navigation-dialog" aria-label="Application navigation"
      onClose={() => { onOpenChange(false); document.getElementById('console-menu-trigger')?.focus() }}
      onClick={event => { if (event.target === event.currentTarget) dialog.current?.close() }}>
      <div className="drawer-sheet">
        <div className="drawer-head">
          <Brand compact />
          <Button variant="secondary" className="drawer-close" onClick={() => dialog.current?.close()}>Close menu</Button>
        </div>
        {menu}
        <AccountCard account={account} />
      </div>
    </dialog>
  )
}

function useWideScreen(onChange: () => void): boolean {
  const [wide, setWide] = useState(() => window.matchMedia(wideScreen).matches)
  useEffect(() => {
    const media = window.matchMedia(wideScreen)
    const resize = () => {
      setWide(media.matches)
      onChange()
    }
    media.addEventListener('change', resize)
    return () => media.removeEventListener('change', resize)
  }, [onChange])
  return wide
}

function NavigationMenu({ pages, path, onNavigate }: { pages: Destination[]; path: string; onNavigate: () => void }) {
  const groups = [...new Set(pages.map(page => page.group))]
  // A modified click opens the page elsewhere, so the drawer stays as it is.
  const navigate = (event: MouseEvent) => {
    if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && event.button === 0) onNavigate()
  }
  return (
    <nav aria-label="Pages" className="console-navigation">
      {groups.map(group => (
        <section key={group} aria-label={group}>
          <p className="nav-group" aria-hidden="true">{group}</p>
          <ul>
            {pages.filter(page => page.group === group).map(({ path: to, title, icon: Icon }) => (
              <li key={to}>
                <a href={`#${to}`} aria-current={to === path ? 'page' : undefined} onClick={navigate}>
                  <Icon size={18} aria-hidden />{title}
                </a>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </nav>
  )
}

const roleNames = { admin: 'Admin', reviewer: 'Reviewer' }

function AccountCard({ account }: { account: Account }) {
  const { signOut } = useSession()
  const [leaving, setLeaving] = useState(false)
  const leave = () => {
    if (leaving) return
    setLeaving(true)
    void signOut()
  }
  return (
    <div className="account-card">
      <span className="avatar" aria-hidden="true">{account.username.slice(0, 1).toUpperCase()}</span>
      <p className="account-identity">
        <span className="visually-hidden">Signed in as </span>
        <strong>{account.username}</strong>
        <span className="account-role">{roleNames[account.role]}</span>
      </p>
      <button type="button" className="icon-button" aria-label="Sign out" title="Sign out" aria-disabled={leaving} onClick={leave}>
        <Logout size={18} aria-hidden />
      </button>
    </div>
  )
}
