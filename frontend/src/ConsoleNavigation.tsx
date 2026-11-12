import { useEffect, useRef, useState } from 'react'
import { SidebarContent, SidebarGroup, SidebarMenu, SidebarMenuItem, SidebarProvider, useSidebar, type SidebarLinkProps } from '@xsolla/xui-b2b-sidebar'
import wordmark from './assets/xsolla-wordmark.svg'
import { Home, Graph, Settings, Layer, Wallet, Flag } from '@xsolla/xui-icons-base'

interface Destination { path: string; title: string }
const icons = new Map([['/dashboard', Home], ['/reviews', Graph], ['/blocklist', Flag],
  ['/rulesets', Settings], ['/tester', Layer], ['/simulator', Wallet]])

interface NavigationProps { pages: Destination[]; path: string; open: boolean; onOpenChange: (open: boolean) => void }

export function ConsoleNavigation({ pages, path, open, onOpenChange }: NavigationProps) {
  const [wide, setWide] = useState(() => window.matchMedia('(min-width: 64rem)').matches)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const media = window.matchMedia('(min-width: 64rem)')
    const resize = () => {
      dialog.current?.close()
      setWide(media.matches)
      onOpenChange(false)
    }
    media.addEventListener('change', resize)
    return () => media.removeEventListener('change', resize)
  }, [onOpenChange])
  useEffect(() => { onOpenChange(false) }, [path, onOpenChange])
  useEffect(() => {
    if (open && !wide) dialog.current?.showModal()
    else dialog.current?.close()
  }, [open, wide])
  const menu = <SidebarProvider pathname={path} linkComponent={HashLink}>
    <NavigationMenu pages={pages} onNavigate={() => onOpenChange(false)} />
  </SidebarProvider>
  if (wide) return <aside className="console-sidebar"><Brand />{menu}<p className="sidebar-footnote">Independent project<br />Review console</p></aside>
  return (
    <dialog ref={dialog} id="console-menu" className="navigation-dialog" aria-label="Application navigation"
      onClose={() => { onOpenChange(false); document.getElementById('console-menu-trigger')?.focus() }} onClick={event => { if (event.target === event.currentTarget) dialog.current?.close() }}>
      <div className="drawer-sheet"><Brand /><button type="button" className="secondary drawer-close" onClick={() => dialog.current?.close()}>Close menu</button>{menu}</div>
    </dialog>
  )
}

export function Brand() {
  return <div className="console-brand"><img className="brand-logo" src={wordmark} alt="Xsolla" width="141" height="38" /><span>Anti-Fraud<span className="brand-caption">Decision workspace</span></span></div>
}

function NavigationMenu({ pages, onNavigate }: { pages: Destination[]; onNavigate: () => void }) {
  return <nav aria-label="Pages" className="console-navigation"><SidebarContent><SidebarGroup label="Workspace"><SidebarMenu>
    {pages.map(page => {
      const Icon = icons.get(page.path) ?? Home
      return <SidebarMenuItem key={page.path} to={page.path} label={page.title} icon={<Icon size={18} aria-hidden />}
        onClick={event => { if (!event || (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && event.button === 0)) onNavigate() }} />
    })}
  </SidebarMenu></SidebarGroup></SidebarContent></nav>
}

function HashLink({ to, className, activeClassName, onClick, dataId, children, 'aria-label': label }: SidebarLinkProps) {
  const { pathname } = useSidebar()
  const active = pathname === to
  return <a href={`#${to}`} className={[className, active && activeClassName].filter(Boolean).join(' ')}
    aria-current={active ? 'page' : undefined} aria-label={label} data-id={dataId} onClick={onClick}>{children}</a>
}
