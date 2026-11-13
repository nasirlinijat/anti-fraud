import { ChevronDown } from '@xsolla/xui-icons-base'
import { useEffect, useId, useRef, useState, type FocusEvent, type KeyboardEvent } from 'react'

interface SelectMenuProps<T extends string> {
  label: string
  value: T
  options: readonly (readonly [T, string])[]
  onChange: (value: T) => void
}

const itemSelector = '[role="menuitemradio"]'

// Each key maps the focused item's position to the position that gets focus next.
const moves: Record<string, (at: number, count: number) => number> = {
  ArrowDown: (at, count) => (at + 1) % count,
  ArrowUp: (at, count) => (Math.max(at, 0) - 1 + count) % count,
  Home: () => 0,
  End: (_, count) => count - 1,
}

function moveFocus(event: KeyboardEvent<HTMLElement>) {
  const move = moves[event.key]
  if (!move) return
  event.preventDefault()
  const items = [...event.currentTarget.querySelectorAll<HTMLElement>(itemSelector)]
  items[move(items.findIndex(item => item === document.activeElement), items.length)]?.focus()
}

// A native select lets the browser place its list, which can cover the control.
// This list is placed by CSS, so it always opens below its button.
export function SelectMenu<T extends string>({ label, value, options, onChange }: SelectMenuProps<T>) {
  const id = useId()
  const button = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (open) list.current?.querySelector<HTMLElement>(`${itemSelector}[aria-checked="true"]`)?.focus()
  }, [open])

  function close() {
    setOpen(false)
    button.current?.focus()
  }

  function choose(next: T) {
    close()
    if (next !== value) onChange(next)
  }

  function closeWhenFocusLeaves(event: FocusEvent<HTMLDivElement>) {
    if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false)
  }

  function closeOnEscape(event: KeyboardEvent<HTMLDivElement>) {
    if (!open || event.key !== 'Escape') return
    event.stopPropagation()
    close()
  }

  function openWithArrows(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
    event.preventDefault()
    setOpen(true)
  }

  return (
    <div className="inline-field select-menu" onBlur={closeWhenFocusLeaves} onKeyDown={closeOnEscape}>
      <span id={`${id}-label`}>{label}</span>
      <div className="select-menu-anchor">
        <button ref={button} id={`${id}-button`} type="button" className="select-menu-button" aria-haspopup="menu"
          aria-expanded={open} aria-labelledby={`${id}-label ${id}-button`}
          onClick={() => setOpen(!open)} onKeyDown={openWithArrows}>
          {options.find(([option]) => option === value)?.[1]}
          <ChevronDown size={16} aria-hidden />
        </button>
        {open && (
          // The list takes focus itself when a browser does not focus a clicked button, so the click is not read as focus leaving.
          <div ref={list} className="select-menu-list" role="menu" aria-labelledby={`${id}-label`} tabIndex={-1}
            onKeyDown={moveFocus}>
            {options.map(([option, text]) => (
              <button key={option} type="button" role="menuitemradio" aria-checked={option === value} tabIndex={-1}
                onClick={() => choose(option)}>{text}</button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
