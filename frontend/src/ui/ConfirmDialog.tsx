import { useEffect, useRef, type ReactNode } from 'react'
import { Button } from './Button'

interface ConfirmDialogProps {
  open: boolean
  title: string
  confirmLabel: string
  danger?: boolean
  children: ReactNode
  onConfirm: () => void
  onCancel: () => void
}

// A modal question before an action that cannot be undone. Cancel has the
// initial focus, so Enter never confirms by accident.
export function ConfirmDialog({ open, title, confirmLabel, danger = false, children, onConfirm, onCancel }: ConfirmDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    if (open && !dialog.current?.open) dialog.current?.showModal()
    if (!open && dialog.current?.open) dialog.current.close()
  }, [open])

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby="confirm-dialog-title"
      onClose={event => {
        const confirmed = event.currentTarget.returnValue === 'confirm'
        event.currentTarget.returnValue = ''
        if (confirmed) onConfirm()
        else onCancel()
      }}>
      <h2 id="confirm-dialog-title">{title}</h2>
      <div className="dialog-body">{children}</div>
      <form method="dialog" className="dialog-actions">
        <Button type="submit" value="cancel" variant="secondary" autoFocus>Cancel</Button>
        <Button type="submit" value="confirm" variant={danger ? 'danger' : 'primary'}>{confirmLabel}</Button>
      </form>
    </dialog>
  )
}
