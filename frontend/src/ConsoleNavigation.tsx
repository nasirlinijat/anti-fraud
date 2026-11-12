import { useEffect, useRef, useState } from 'react'
import { SidebarContent, SidebarGroup, SidebarMenu, SidebarMenuItem, SidebarProvider, useSidebar, type SidebarLinkProps } from '@xsolla/xui-b2b-sidebar'
import { Home, Graph, Settings, Layer, Wallet, Flag } from '@xsolla/xui-icons-base'

interface Destination { path: string; title: string }
const icons = new Map([['/dashboard', Home], ['/reviews', Graph], ['/blocklist', Flag],
  ['/rulesets', Settings], ['/tester', Layer], ['/simulator', Wallet]])

export function ConsoleNavigation({ pages, path }: { pages: Destination[]; path: string }) {
  const [open, setOpen] = useState(false)
  const [wide, setWide] = useState(() => window.matchMedia('(min-width: 64rem)').matches)
  const dialog = useRef<HTMLDialogElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    const media = window.matchMedia('(min-width: 64rem)')
    const resize = () => {
      dialog.current?.close()
      setWide(media.matches)
    }
    media.addEventListener('change', resize)
    return () => media.removeEventListener('change', resize)
  }, [])
  useEffect(() => { dialog.current?.close() }, [path])
  const menu = <SidebarProvider pathname={path} linkComponent={HashLink}>
    <NavigationMenu pages={pages} onNavigate={() => dialog.current?.close()} />
  </SidebarProvider>
  if (wide) return <aside className="console-sidebar"><Brand />{menu}<p className="sidebar-footnote">Independent project<br />Review console</p></aside>
  return <>
    <button ref={trigger} type="button" className="menu-trigger secondary" aria-haspopup="dialog" aria-controls="console-menu" aria-expanded={open}
      onClick={() => { dialog.current?.showModal(); setOpen(true) }}><span aria-hidden="true">☰</span> Menu</button>
    <dialog ref={dialog} id="console-menu" className="navigation-dialog" aria-label="Application navigation"
      onClose={() => { setOpen(false); trigger.current?.focus() }} onClick={event => { if (event.target === event.currentTarget) dialog.current?.close() }}>
      <div className="drawer-sheet"><Brand /><button type="button" className="secondary drawer-close" onClick={() => dialog.current?.close()}>Close menu</button>{menu}</div>
    </dialog>
  </>
}

export function Brand() {
  return <div className="console-brand"><span className="brand-mark" aria-hidden="true">a<span>f</span></span><span>Anti-Fraud<span className="brand-caption">Decision workspace</span></span></div>
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
